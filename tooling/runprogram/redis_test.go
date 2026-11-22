package runprogram

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/types"
	"fiig-lang/internal/fixture"
)

// ADR 0110: fiig.redis's client against the in-memory Redis.

var redisProgram = map[string]string{
	"fiig.pkg.json": `{
    "path": "redistest.app",
    "kind": "application",
    "version": "1.0.0",
    "dependencies": { "fiig.redis": "1.0.0" },
    "capabilities": ["Socket"]
}`,
	"src/counters/Bump.fiig": `use fiig.redis
use fiig.redis.Redis

gateway bump(key: string) -> Result<redis.RespValue, redis.RedisError>
    requires Redis
{
    return Redis.send(["INCR", key]);
}`,
}

// redisCall runs counters.bump against url and returns what it gave.
func redisCall(t *testing.T, url string) runtime.Value {
	t.Helper()
	root := t.TempDir()
	for name, src := range fixture.Module(redisProgram) {
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
	pkg := loaded.Packages["redistest.app.counters"]
	if pkg == nil {
		t.Fatalf("no package redistest.app.counters among %v", loaded.Packages)
	}
	env := map[string]string{"REDIS_URL": url}
	lookup := func(name string) (string, bool) { v, ok := env[name]; return v, ok }
	program := Program{Universe: u, Imports: in.Imports, Groups: in.Groups}
	if problems := in.LoadConfig(ReachableConfigFrom(program, []Entry{{Package: pkg, Name: "bump"}}), lookup); len(problems) > 0 {
		t.Fatal(problems)
	}
	v, fault := in.Call(in.EnterPackage(context.Background(), pkg), "bump", map[string]runtime.Value{"key": runtime.Str("hits")})
	if fault != nil {
		t.Fatal(fault)
	}
	return v
}

// Review of ADR 0110 §3: a new connection's SELECT is answered before the
// command is sent, so a database the server refuses doesn't let the
// command run on database 0.
func TestRefusedSelectDoesNotRunTheCommand(t *testing.T) {
	redis := startFakeRedis(t, "pw")
	got := redisCall(t, redis.url()+"/99").String()
	if !strings.Contains(got, "DB index is out of range") {
		t.Errorf("bump = %s, want the SELECT's refusal", got)
	}
	if n := redis.counter("hits"); n != 0 {
		t.Errorf("the command ran %d time(s) after a refused SELECT", n)
	}
	if got := redisCall(t, redis.url()+"/2").String(); !strings.Contains(got, "Integer(1)") {
		t.Errorf("bump on database 2 = %s", got)
	}
}
