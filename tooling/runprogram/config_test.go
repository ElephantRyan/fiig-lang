package runprogram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0071 §3a: -env-file's format, and the process environment winning.
func TestEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	src := "# local development\n\nexport FIIG_A=1\nFIIG_B = \"two words\"\nFIIG_C='x=y'\nFIIG_D=\nFIIG_WIN=file\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FIIG_WIN", "process")
	env, err := NewEnvironment(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"FIIG_A": "1", "FIIG_B": "two words", "FIIG_C": "x=y", "FIIG_D": "", "FIIG_WIN": "process"} {
		if got, ok := env.Lookup(name); !ok || got != want {
			t.Errorf("%s = %q, %v; want %q", name, got, ok, want)
		}
	}
	if _, ok := env.Lookup("FIIG_NOT_THERE"); ok {
		t.Error("an unlisted variable read as set")
	}
	if err := os.WriteFile(path, []byte("FIIG_A=1\nnot a line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEnvironment(path); err == nil || !strings.Contains(err.Error(), ".env:2: expected NAME=value") {
		t.Fatalf("bad line: %v", err)
	}
	if _, err := NewEnvironment(filepath.Join(t.TempDir(), "missing.env")); err == nil {
		t.Fatal("a missing -env-file read as empty")
	}
}

// §3: only config an entry point reaches is read, and a capability group
// used as a value makes every gateway in it reachable.
func TestReachableConfig(t *testing.T) {
	srcs := []string{
		"wire Stripe {\n    key: Secret from env \"STRIPE_KEY\"\n}",
		"wire Square {\n    key: Secret from env \"SQUARE_KEY\"\n}",
		"wire Setup {\n    admin: string from env \"ADMIN\"\n}",
		"interface Pay {\n    charge(amount: int) -> string\n}",
		"capability StripePay {\n    gateway charge(amount: int) -> string\n        requires Socket\n    {\n        return Stripe.key.reveal();\n    }\n}",
		"capability SquarePay {\n    gateway charge(amount: int) -> string\n        requires Socket\n    {\n        return Square.key.reveal();\n    }\n}",
		"rule choose(eu: bool) -> Pay {\n    if (eu) {\n        return SquarePay;\n    }\n    return StripePay;\n}",
		"activity pay(eu: bool) -> string {\n    Provider = choose(eu);\n    return Provider.charge(1);\n}",
		"workflow checkout(eu: bool) -> string {\n    return pay(eu);\n}",
		"activity setup() -> string {\n    return Setup.admin;\n}",
	}
	var files []*syntax.File
	for _, src := range srcs {
		f, errs := parser.Parse(src)
		if len(errs) != 0 {
			t.Fatal(errs)
		}
		files = append(files, f)
	}
	u, _ := types.BuildUniverse(files)
	names := func(entries ...string) string {
		var out []string
		for _, s := range ReachableConfig(Program{Universe: u}, entries) {
			out = append(out, s.Decl.Name)
		}
		return strings.Join(out, ",")
	}
	if got := names("checkout"); got != "Square,Stripe" {
		t.Errorf("checkout reaches %q, want both providers' config", got)
	}
	if got := names("setup"); got != "Setup" {
		t.Errorf("setup reaches %q", got)
	}
}
