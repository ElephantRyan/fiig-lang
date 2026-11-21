package runprogram

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/types"
	"fiig-lang/internal/fixture"
)

// ADR 0114, ADR 0117 §2: a session runs its package's protocol, written in
// fiig, over a connection the runtime owns. The protocol here is lines:
// an ask is "<key> <text>", the server answers "<key> <TEXT>", and the
// protocol replies with what came back.

var sessionLibrary = map[string]string{
	"fiig.pkg.json": `{
    "path": "sessiontest.lines",
    "kind": "library",
    "version": "1.0.0",
    "dependencies": { "fiig.session": "1.0.0" },
    "provides": { "fiig.session.SessionProtocol": "LineSession" }
}`,
	"lines.fiig": `capability Lines {
    gateway shout(port: int, movePort: int, text: string) -> Result<string, SessionError>
        requires Session
    {
        settings: Map<string, string> = ["host": "127.0.0.1", "port": "${port}", "movePort": "${movePort}"]
        parts = Session.call(settings, text.utf8(), deadline: Clock.now() + 2000)?
        return Ok(parts.first().utf8() or "")
    }

    gateway shoutVia(port: int, alternates: string, text: string) -> Result<string, SessionError>
        requires Session
    {
        settings: Map<string, string> = ["host": "127.0.0.1", "port": "${port}", "movePort": "0", "alternates": alternates]
        parts = Session.call(settings, text.utf8(), deadline: Clock.now() + 8000)?
        return Ok(parts.first().utf8() or "")
    }
}`,
	"line_session.fiig": `use fiig.session

capability LineSession provides session.SessionProtocol {
    gateway opened(settings: Map<string, string>, now: int) -> session.Step
        requires Session
    {
        movePort = settings.at("movePort")
        if movePort != "0" and settings.at("port") != movePort {
            match movePort.toIntOrNone() {
                None -> None
                port -> {
                    return lineStep(<<1: i64be>>, <<>>, None, [], session.SessionClose.MoveTo(host: settings.at("host"), port: port))
                }
            }
        }
        return lineStep(<<1: i64be>>, "HELLO ${settings.at("sessionId")} ${settings.at("port")}\n".utf8(), None, [], None)
    }

    gateway received(state: bytes, data: bytes, now: int) -> session.Step
        requires Session
    {
        match state {
            <<next: i64be, buffered: bytes>> -> {
                rest: bytes = <<buffered: bytes, data: bytes>>
                replies: List<session.SessionReply> = []
                start: int = 0
                i: int = 0
                for b in rest {
                    if b == 10 {
                        line = rest.slice(start, i).utf8() or ""
                        start = i + 1
                        if line == "CLOSE" {
                            return lineStep(state, <<>>, None, replies, session.SessionClose.Failed(reason: "the server said CLOSE"))
                        }
                        pieces = line.split(" ")
                        match pieces.first().toIntOrNone() {
                            None -> None
                            key -> {
                                replies = append(replies, session.SessionReply(key: key, message: line.removePrefix("${key} ").utf8(), last: true))
                            }
                        }
                    }
                    i = i + 1
                }
                return lineStep(<<next: i64be, rest.slice(start, rest.length()): bytes>>, <<>>, None, replies, None)
            }
            _ -> {
                return lineStep(state, <<>>, None, [], session.SessionClose.Failed(reason: "no state"))
            }
        }
    }

    gateway asked(state: bytes, message: bytes, now: int) -> session.Step
        requires Session
    {
        match state {
            <<next: i64be, buffered: bytes>> -> {
                return lineStep(<<next + 1: i64be, buffered: bytes>>, <<"${next} ".utf8(): bytes, message: bytes, 10: u8>>, next, [], None)
            }
            _ -> {
                return lineStep(state, <<>>, None, [], session.SessionClose.Failed(reason: "no state"))
            }
        }
    }

    gateway tick(state: bytes, now: int) -> session.Step
        requires Session
    {
        return lineStep(state, <<>>, None, [], None)
    }

    gateway closing(state: bytes, now: int) -> session.Step
        requires Session
    {
        return lineStep(state, "BYE\n".utf8(), None, [], None)
    }
}`,
	"line_step.fiig": `use fiig.session

conversion lineStep(state: bytes, write: bytes, accepted: int | None, replies: List<session.SessionReply>, close: session.SessionClose | None) -> session.Step {
    return session.Step(state: state, write: write, accepted: accepted, replies: replies, nextTickAt: None, close: close)
}`,
}

var sessionApp = map[string]string{
	"fiig.pkg.json": `{
    "path": "sessiontest.app",
    "kind": "application",
    "version": "1.0.0",
    "dependencies": { "sessiontest.lines": "1.0.0" },
    "capabilities": ["Session"]
}`,
	"shout.fiig": `use sessiontest.lines.Lines

activity shout(port: int, movePort: int, text: string) -> Result<string, SessionError> {
    return Lines.shout(port, movePort, text)
}`,
	"shout_via.fiig": `use sessiontest.lines.Lines

activity shoutVia(port: int, alternates: string, text: string) -> Result<string, SessionError> {
    return Lines.shoutVia(port, alternates, text)
}`,
}

// lineServer answers "<key> <text>" with "<key> <TEXT>", says CLOSE for
// "close", hangs up on "drop", and records every line it reads.
type lineServer struct {
	ln    net.Listener
	mu    sync.Mutex
	lines []string
	conns int
}

func startLineServer(t *testing.T) *lineServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &lineServer{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns++
			s.mu.Unlock()
			go s.serve(c)
		}
	}()
	return s
}

func (s *lineServer) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewScanner(c)
	for r.Scan() {
		line := r.Text()
		s.mu.Lock()
		s.lines = append(s.lines, line)
		s.mu.Unlock()
		key, text, _ := strings.Cut(line, " ")
		switch text {
		case "drop":
			return
		case "close":
			c.Write([]byte("CLOSE\n"))
			continue
		}
		if _, err := strconv.Atoi(key); err == nil {
			// Split across writes, so the protocol buffers a partial line.
			reply := key + " " + strings.ToUpper(text) + "\n"
			c.Write([]byte(reply[:2]))
			time.Sleep(time.Millisecond)
			c.Write([]byte(reply[2:]))
		}
	}
}

func (s *lineServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *lineServer) seen() ([]string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lines...), s.conns
}

func sessionInterp(t *testing.T) (*runtime.Interp, *runtime.CapabilityRegistry) {
	t.Helper()
	root, ws := t.TempDir(), t.TempDir()
	write := func(dir string, files map[string]string) {
		for name, src := range fixture.Module(files) {
			os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
			if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(root, sessionApp)
	write(filepath.Join(ws, "lines"), sessionLibrary)
	loaded, errs := loader.Load(root, []string{ws})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	files, _, err := LoadFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if diags := Check(root, files, loaded.Packages); len(diags) > 0 {
		t.Fatal(diags)
	}
	u, _ := types.BuildUniverse(files)
	caps := runtime.NewCapabilityRegistry()
	RegisterDefaults(caps)
	in := runtime.New(u, caps)
	in.Imports = BuildImports(files, loaded.Packages)
	in.Groups = BuildGroups(files, loaded.Packages)
	in.Packages = loaded.Packages
	return in, caps
}

func shout(t *testing.T, in *runtime.Interp, port, movePort int, text string) runtime.Result {
	t.Helper()
	v, f := in.Call(context.Background(), "shout", map[string]runtime.Value{
		"port": runtime.Int(port), "movePort": runtime.Int(movePort), "text": runtime.Str(text),
	})
	if f != nil {
		t.Fatalf("shout %q: %s", text, f.Message)
	}
	return v.(runtime.Result)
}

func TestSessionRunsItsPackagesProtocol(t *testing.T) {
	server := startLineServer(t)
	in, caps := sessionInterp(t)

	// Many calls at once share one connection, each getting its own reply.
	var wg sync.WaitGroup
	for _, word := range []string{"alpha", "beta", "gamma", "delta"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := shout(t, in, server.port(), 0, word)
			if !r.Ok || r.Value != runtime.Str(strings.ToUpper(word)) {
				t.Errorf("shout %s = %v", word, r)
			}
		}()
	}
	wg.Wait()
	lines, conns := server.seen()
	if conns != 1 || !strings.HasPrefix(lines[0], "HELLO ") || len(lines) != 5 {
		t.Fatalf("server saw %d connections, lines %q", conns, lines)
	}

	// A connection the server drops fails the call waiting on it, and the
	// next call opens another with the same session id.
	if r := shout(t, in, server.port(), 0, "drop"); r.Ok || !strings.Contains(r.Value.String(), "Broken") {
		t.Fatalf("a dropped connection gave %v", r)
	}
	if r := shout(t, in, server.port(), 0, "again"); !r.Ok || r.Value != runtime.Str("AGAIN") {
		t.Fatalf("after the drop: %v", r)
	}
	// The protocol closing the connection fails the waiting call too.
	if r := shout(t, in, server.port(), 0, "close"); r.Ok || !strings.Contains(r.Value.String(), "the server said CLOSE") {
		t.Fatalf("a closed connection gave %v", r)
	}
	lines, conns = server.seen()
	var hellos []string
	for _, l := range lines {
		if strings.HasPrefix(l, "HELLO ") {
			hellos = append(hellos, l)
		}
	}
	if conns != 2 || len(hellos) != 2 || hellos[0] != hellos[1] {
		t.Fatalf("server saw %d connections, hellos %q", conns, hellos)
	}

	// Close runs closing, which says BYE before the connection ends.
	shout(t, in, server.port(), 0, "last")
	caps.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if lines, _ = server.seen(); lines[len(lines)-1] == "BYE" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if lines[len(lines)-1] != "BYE" {
		t.Fatalf("closing wrote nothing; the server's last lines %q", lines[len(lines)-2:])
	}
}

func TestSessionMovesAndAsksAgain(t *testing.T) {
	first, second := startLineServer(t), startLineServer(t)
	in, caps := sessionInterp(t)
	defer caps.Close()

	// opened on the first server moves to the second; the call never sees
	// it, and its message is asked of the second.
	if r := shout(t, in, first.port(), second.port(), "moved"); !r.Ok || r.Value != runtime.Str("MOVED") {
		t.Fatalf("a moved session gave %v", r)
	}
	if lines, conns := first.seen(); conns != 1 || len(lines) != 0 {
		t.Fatalf("the first server saw %d connections and %q", conns, lines)
	}
	if lines, conns := second.seen(); conns != 1 || len(lines) != 2 || lines[1] != "1 moved" {
		t.Fatalf("the second server saw %d connections and %q", conns, lines)
	}
}

func TestSessionCallTimesOut(t *testing.T) {
	// A server that accepts and never answers.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	in, caps := sessionInterp(t)
	defer caps.Close()
	start := time.Now()
	r := shout(t, in, ln.Addr().(*net.TCPAddr).Port, 0, "anyone")
	if r.Ok || !strings.Contains(r.Value.String(), "TimedOut") || time.Since(start) > 4*time.Second {
		t.Fatalf("a silent server gave %v after %s", r, time.Since(start))
	}
}

// closedPort is a port nothing listens on.
func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func shoutVia(t *testing.T, in *runtime.Interp, port int, alternates, text string) runtime.Result {
	t.Helper()
	v, f := in.Call(context.Background(), "shoutVia", map[string]runtime.Value{
		"port": runtime.Int(port), "alternates": runtime.Str(alternates), "text": runtime.Str(text),
	})
	if f != nil {
		t.Fatalf("shoutVia %q: %s", text, f.Message)
	}
	return v.(runtime.Result)
}

// ADR 0117 §2, as amended 2026-11-21: a host that can't be reached is
// passed over for the settings' alternates, in order, and opened is told
// the one reached; when none can be, the call is Broken, naming each.
func TestSessionFailsOverToAnAlternate(t *testing.T) {
	server := startLineServer(t)
	dead, deadToo := closedPort(t), closedPort(t)
	in, caps := sessionInterp(t)
	defer caps.Close()

	alternates := "127.0.0.1:" + strconv.Itoa(deadToo) + ", 127.0.0.1:" + strconv.Itoa(server.port())
	if r := shoutVia(t, in, dead, alternates, "over"); !r.Ok || r.Value != runtime.Str("OVER") {
		t.Fatalf("a session with a live alternate gave %v", r)
	}
	lines, conns := server.seen()
	if conns != 1 || len(lines) != 2 || !strings.HasSuffix(lines[0], " "+strconv.Itoa(server.port())) || lines[1] != "1 over" {
		t.Fatalf("the alternate saw %d connections and %q, want opened told its own port", conns, lines)
	}

	r := shoutVia(t, in, dead, "127.0.0.1:"+strconv.Itoa(deadToo), "nobody")
	if got := r.Value.String(); r.Ok || !strings.Contains(got, "Broken") || !strings.Contains(got, strconv.Itoa(dead)) || !strings.Contains(got, strconv.Itoa(deadToo)) {
		t.Fatalf("a session reaching nothing gave %v", r)
	}
}

// An alternate that isn't a host:port faults the call: a defect in the
// package that wrote it.
func TestSessionRefusesAMalformedAlternate(t *testing.T) {
	in, caps := sessionInterp(t)
	defer caps.Close()
	_, f := in.Call(context.Background(), "shoutVia", map[string]runtime.Value{
		"port": runtime.Int(1), "alternates": runtime.Str("::1:9092"), "text": runtime.Str("x"),
	})
	if f == nil || !strings.Contains(f.Message, "alternates") {
		t.Fatalf("got %v", f)
	}
}
