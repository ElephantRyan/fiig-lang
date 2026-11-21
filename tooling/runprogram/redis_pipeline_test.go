package runprogram

import (
	"context"
	"fmt"
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

// ADR 0110 open question 1, as amended 2026-11-21: Redis.sendAll and
// sendAllBytes pipeline their commands. Against the in-memory Redis, or a
// real one when FIIG_REDIS_URL names it (redis://127.0.0.1:56379 for the
// fiig-redis-dogfood container).

var pipelineProgram = map[string]string{
	"fiig.pkg.json": `{
    "path": "pipelinetest.app",
    "kind": "application",
    "version": "1.0.0",
    "dependencies": { "fiig.redis": "1.0.0" },
    "capabilities": ["Socket"]
}`,
	"pipeline.fiig": `use fiig.redis
use fiig.redis.Redis

gateway pipeline(commands: List<List<string>>) -> Result<List<redis.RespValue>, redis.RedisError>
    requires Redis
{
    return Redis.sendAll(commands)
}`,
	"pipeline_bytes.fiig": `use fiig.redis
use fiig.redis.Redis

gateway pipelineBytes(key: string, value: bytes) -> Result<List<redis.RespValue>, redis.RedisError>
    requires Redis
{
    return Redis.sendAllBytes([["XADD".utf8(), key.utf8(), "*".utf8(), "payload".utf8(), value], ["XLEN".utf8(), key.utf8()]])
}`,
}

// pipelineCaller loads pipelineProgram against url and returns a function
// calling one of its gateways, every call through the same bindings, so a
// kept connection serves the next.
func pipelineCaller(t *testing.T, url string) func(name string, args map[string]runtime.Value) runtime.Value {
	t.Helper()
	root := t.TempDir()
	for name, src := range fixture.Module(pipelineProgram) {
		os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755)
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loaded, errs := loader.Load(root, nil)
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
	t.Cleanup(caps.Close)
	in := runtime.New(u, caps)
	in.Imports = BuildImports(files, loaded.Packages)
	in.Groups = BuildGroups(files, loaded.Packages)
	in.Packages = loaded.Packages
	env := map[string]string{"REDIS_URL": url}
	lookup := func(name string) (string, bool) { v, ok := env[name]; return v, ok }
	program := Program{Universe: u, Imports: in.Imports, Groups: in.Groups}
	if problems := in.LoadConfig(ReachableConfigFrom(program, []Entry{{Name: "pipeline"}, {Name: "pipelineBytes"}}), lookup); len(problems) > 0 {
		t.Fatal(problems)
	}
	return func(name string, args map[string]runtime.Value) runtime.Value {
		t.Helper()
		v, fault := in.Call(context.Background(), name, args)
		if fault != nil {
			t.Fatal(fault)
		}
		return v
	}
}

func commandList(commands ...[]string) runtime.Value {
	out := runtime.List{}
	for _, c := range commands {
		words := runtime.List{}
		for _, w := range c {
			words.Elems = append(words.Elems, runtime.Str(w))
		}
		out.Elems = append(out.Elems, words)
	}
	return out
}

func TestPipelinedCommandsShareOneRoundTrip(t *testing.T) {
	url := os.Getenv("FIIG_REDIS_URL")
	var fake *fakeRedis
	if url == "" {
		fake = startFakeRedis(t, "pw")
		url = fake.url()
	}
	call := pipelineCaller(t, url)
	key := fmt.Sprintf("fiig-pipeline-%d", time.Now().UnixNano())

	// The refused command is an Error in its place; the INCRs either side
	// of it both ran.
	got := call("pipeline", map[string]runtime.Value{"commands": commandList(
		[]string{"INCR", key}, []string{"NOSUCHCOMMAND", key}, []string{"INCR", key},
	)}).String()
	if !strings.HasPrefix(got, "Ok([RespValue.Integer(1), RespValue.Error(") || !strings.HasSuffix(got, "), RespValue.Integer(2)])") || !strings.Contains(got, "unknown command") {
		t.Errorf("sendAll = %s", got)
	}

	// Empty is nothing sent; a command with no name sends none of them.
	if got := call("pipeline", map[string]runtime.Value{"commands": commandList()}).String(); got != "Ok([])" {
		t.Errorf("sendAll([]) = %s", got)
	}
	got = call("pipeline", map[string]runtime.Value{"commands": commandList([]string{"INCR", key}, []string{})}).String()
	if !strings.Contains(got, "InvalidCommand") || !strings.Contains(got, "command 2") {
		t.Errorf("sendAll with an empty command = %s", got)
	}

	// Bytes go as they are.
	stream := key + "-stream"
	got = call("pipelineBytes", map[string]runtime.Value{"key": runtime.Str(stream), "value": runtime.Bytes{B: []byte{0xff, 0x00, 0x0d, 0x0a}}}).String()
	if !strings.HasPrefix(got, "Ok([RespValue.Bulk(") || !strings.HasSuffix(got, "), RespValue.Integer(1)])") {
		t.Errorf("sendAllBytes = %s", got)
	}

	if fake != nil {
		if n := fake.counter(key); n != 2 {
			t.Errorf("%s = %d, want 2: the INCR after the refused command didn't run", key, n)
		}
		if got := fake.payloads(stream); len(got) != 1 || got[0] != "\xff\x00\r\n" {
			t.Errorf("%s holds %q", stream, got)
		}
		// Every call went over the one kept connection (ADR 0109).
		if n := fake.accepted.Load(); n != 1 {
			t.Errorf("%d connections, want 1", n)
		}
	}

	// MULTI changes the connection, so it isn't kept after.
	if fake != nil {
		call("pipeline", map[string]runtime.Value{"commands": commandList([]string{"MULTI"})})
		call("pipeline", map[string]runtime.Value{"commands": commandList([]string{"INCR", key})})
		if n := fake.accepted.Load(); n != 2 {
			t.Errorf("%d connections after MULTI, want 2", n)
		}
	}
}
