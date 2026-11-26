package runprogram

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/types"
	"fiig-lang/internal/fixture"
)

// ADR 0146 §5a: a session that connects only to write. The protocol here
// is a cache over the line server: an answer it has is given from its
// state, with no connection; one it hasn't is asked of the server, which
// opens one. "stats" answers how many times opened ran and each reason
// ended was given, and "clear" forgets the answers.

var onWriteLibrary = map[string]string{
	"fiig.pkg.json": `{
    "path": "sessiontest.cache",
    "kind": "library",
    "version": "1.0.0",
    "dependencies": { "fiig.session": "1.0.0" },
    "provides": { "fiig.session.SessionProtocol": "CacheSession" }
}`,
	"cache.fiig": `capability Cache {
    gateway ask(port: int, text: string) -> Result<string, SessionError> requires Session {
        settings: Map<string, string> = ["host": "127.0.0.1", "port": "${port}", "connect": "onWrite"];
        parts = Session.call(settings, text.utf8(), deadline: Clock.now() + 2000)?;
        return Ok(parts.first().utf8() ?? "");
    }
}`,
	"cache_state.fiig": `model CacheState {
    next: int;
    opens: int;
    answers: Map<string, string>;
    pending: Map<string, string>;
    endings: List<string>;
    buffered: string;
}`,
	"cache_session.fiig": `use fiig.session;

capability CacheSession provides session.SessionProtocol {
    gateway opened(settings: Map<string, string>, now: int) -> session.Step requires Session {
        answers: Map<string, string> = [:];
        pending: Map<string, string> = [:];
        endings: List<string> = [];
        return cacheStep(CacheState(next: 1, opens: 1, answers: answers, pending: pending, endings: endings, buffered: ""), <<>>, None, []);
    }

    gateway asked(state: bytes, message: bytes, now: int) -> session.Step requires Session {
        s = stateOf(state);
        text = message.utf8() ?? "";
        key = s.next;
        moved = copy(s, next: key + 1);
        if (text == "stats") {
            return cacheStep(moved, <<>>, key, [cacheReply(key, "${s.opens} ${s.endings.join(",")}")]);
        }
        if (text == "clear") {
            answers: Map<string, string> = [:];
            return cacheStep(copy(moved, answers: answers), <<>>, key, [cacheReply(key, "cleared")]);
        }
        if (s.answers.containsKey(text)) {
            return cacheStep(moved, <<>>, key, [cacheReply(key, s.answers.at(text))]);
        }
        return cacheStep(copy(moved, pending: s.pending.put("${key}", text)), "${key} ${text}\n".utf8(), key, []);
    }

    gateway received(state: bytes, data: bytes, now: int) -> session.Step requires Session {
        s = stateOf(state);
        rest = "${s.buffered}${data.utf8() ?? ""}";
        lines = rest.split("\n");
        answers: Map<string, string> = s.answers;
        pending: Map<string, string> = s.pending;
        replies: List<session.SessionReply> = [];
        i: int = 0;
        for (line in lines) {
            if (i < lines.length() - 1) {
                pieces = line.split(" ");
                key = pieces.first();
                if (pending.containsKey(key)) {
                    answer = line.removePrefix("${key} ");
                    answers = answers.put(pending.at(key), answer);
                    pending = pending.remove(key);
                    match (key.toIntOrNone()) {
                        None -> None,
                        k -> {
                            replies = append(replies, cacheReply(k, answer));
                        }
                    }
                }
            }
            i = i + 1;
        }
        kept = copy(s, answers: answers, pending: pending, buffered: lines.last());
        return cacheStep(kept, <<>>, None, replies);
    }

    gateway tick(state: bytes, now: int) -> session.Step requires Session {
        return cacheStep(stateOf(state), <<>>, None, []);
    }

    gateway closing(state: bytes, now: int) -> session.Step requires Session {
        return cacheStep(stateOf(state), <<>>, None, []);
    }

    gateway ended(state: bytes, reason: string, now: int) -> session.Step requires Session {
        s = stateOf(state);
        replies: List<session.SessionReply> = [];
        for (key in s.pending.keys()) {
            match (key.toIntOrNone()) {
                None -> None,
                k -> {
                    replies = append(replies, cacheReply(k, "ended"));
                }
            }
        }
        pending: Map<string, string> = [:];
        why = reason.split(":").first();
        return cacheStep(copy(s, pending: pending, endings: append(s.endings, why), buffered: ""), <<>>, None, replies);
    }
}`,
	"state_of.fiig": `conversion stateOf(state: bytes) -> CacheState {
    decoded: Result<CacheState, JsonError> = json.decode(state);
    answers: Map<string, string> = [:];
    return match (decoded) {
        Ok(s) -> s,
        Err(_) -> CacheState(next: 1, opens: 0, answers: answers, pending: answers, endings: ["bad state"], buffered: "")
    }
}`,
	"cache_step.fiig": `use fiig.session;

conversion cacheStep(state: CacheState, write: bytes, accepted: int | None, replies: List<session.SessionReply>) -> session.Step {
    return session.Step(state: json.encode(state), write: write, accepted: accepted, replies: replies, nextTickAt: None, close: None);
}`,
	"cache_reply.fiig": `use fiig.session;

conversion cacheReply(key: int, text: string) -> session.SessionReply {
    return session.SessionReply(key: key, message: text.utf8(), last: true);
}`,
}

var onWriteApp = map[string]string{
	"fiig.pkg.json": `{
    "path": "sessiontest.cacheapp",
    "kind": "application",
    "version": "1.0.0",
    "dependencies": { "sessiontest.cache": "1.0.0" },
    "capabilities": ["Session"]
}`,
	"ask.fiig": `use sessiontest.cache.Cache;

activity ask(port: int, text: string) -> Result<string, SessionError> {
    return Cache.ask(port, text);
}`,
}

func onWriteInterp(t *testing.T) (*runtime.Interp, *runtime.CapabilityRegistry) {
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
	write(root, onWriteApp)
	write(filepath.Join(ws, "cache"), onWriteLibrary)
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

func ask(t *testing.T, in *runtime.Interp, port int, text string) runtime.Result {
	t.Helper()
	v, f := in.Call(context.Background(), "ask", map[string]runtime.Value{"port": runtime.Int(port), "text": runtime.Str(text)})
	if f != nil {
		t.Fatalf("ask %q: %s", text, f.Message)
	}
	return v.(runtime.Result)
}

func askOK(t *testing.T, in *runtime.Interp, port int, text, want string) {
	t.Helper()
	if r := ask(t, in, port, text); !r.Ok || r.Value != runtime.Str(want) {
		t.Fatalf("ask %q = %v, want %q", text, r, want)
	}
}

func TestASessionThatConnectsOnlyToWrite(t *testing.T) {
	server := startLineServer(t)
	in, caps := onWriteInterp(t)
	defer caps.Close()
	port := server.port()

	// opened runs with no connection: the first ask the protocol answers
	// from its state opens none.
	askOK(t, in, port, "stats", "1 ")
	if _, conns := server.seen(); conns != 0 {
		t.Fatalf("an answer from state opened %d connections", conns)
	}

	// An ask it can't answer writes, which opens the connection.
	askOK(t, in, port, "alpha", "ALPHA")
	if _, conns := server.seen(); conns != 1 {
		t.Fatalf("the first write opened %d connections", conns)
	}

	// The server hangs up. ended runs and the state stays: the answer it
	// learned is still there, given with no new connection, and opened
	// didn't run again.
	// The ask waiting on that connection is answered by ended, not failed.
	if r := ask(t, in, port, "drop"); !r.Ok || r.Value != runtime.Str("ended") {
		t.Fatalf("a dropped connection gave %v", r)
	}
	askOK(t, in, port, "alpha", "ALPHA")
	askOK(t, in, port, "stats", "1 the connection broke")
	if _, conns := server.seen(); conns != 1 {
		t.Fatalf("answers from state after the drop opened %d connections", conns)
	}

	// The next write opens a new connection.
	askOK(t, in, port, "beta", "BETA")
	if _, conns := server.seen(); conns != 2 {
		t.Fatalf("a write after the drop gave %d connections, want 2", conns)
	}
}

// A lazy session whose server can't be reached keeps its state: ended runs
// for the failed connect, and the asks it answers from state still work.
func TestASessionThatConnectsOnlyToWriteSurvivesAnUnreachableServer(t *testing.T) {
	server := startLineServer(t)
	in, caps := onWriteInterp(t)
	defer caps.Close()
	port := server.port()
	askOK(t, in, port, "alpha", "ALPHA")
	server.ln.Close()
	if r := ask(t, in, port, "drop"); !r.Ok || r.Value != runtime.Str("ended") {
		t.Fatalf("drop gave %v", r)
	}
	// The listener is closed, so the next write can't connect: ended runs,
	// the ask is answered by it, and the cached answer stays.
	if r := ask(t, in, port, "gamma"); !r.Ok || r.Value != runtime.Str("ended") {
		t.Fatalf("an unreachable server gave %v", r)
	}
	askOK(t, in, port, "alpha", "ALPHA")
	r := ask(t, in, port, "stats")
	if !r.Ok || !strings.HasPrefix(string(r.Value.(runtime.Str)), "1 the connection broke,can't connect to ") {
		t.Fatalf("stats = %v", r)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, conns := server.seen(); conns == 1 {
			return
		}
	}
	_, conns := server.seen()
	t.Fatalf("server saw %d connections, want 1", conns)
}
