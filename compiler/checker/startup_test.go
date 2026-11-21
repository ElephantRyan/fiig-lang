package checker

import (
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
)

// ADR 0124: startup routes, bug classes I7 and I9.

const startupErrors = `enum StorageError {
    Missing
    Unreachable(reason: string)
}`

const verifyStorage = `workflow verifyStorage() -> Result<None, StorageError> {
    return Ok(None)
}`

const verifyNamed = `workflow verifyNamed(bucket: string) -> Result<None, StorageError> {
    return Ok(None)
}`

func checkStartup(t *testing.T, adapter string) []Diagnostic {
	t.Helper()
	return checkSrc(t, startupErrors, verifyStorage, verifyNamed, adapter)
}

func TestStartupRoutesCheck(t *testing.T) {
	src := `adapter startup Preflight {
    run -> verifyStorage() {
        Ok(_) -> proceed
        Err(StorageError.Missing) -> halt
        Err(StorageError.Unreachable(_)) -> proceed
    }
}`
	if diags := checkStartup(t, src); len(diags) > 0 {
		t.Errorf("diags = %v", diags)
	}
}

func TestStartupRouteOnceAndWithinCheck(t *testing.T) {
	src := `adapter startup Preflight {
    run once within "2m" -> verifyStorage() {
        Ok(_) -> proceed
        Err(_) -> halt
    }
    run within "500ms" -> verifyStorage() {
        _ -> proceed
    }
    run once -> verifyStorage() {
        _ -> proceed
    }
}`
	if diags := checkStartup(t, src); len(diags) > 0 {
		t.Errorf("diags = %v", diags)
	}
}

func TestStartupRouteBugClasses(t *testing.T) {
	cases := []struct{ name, route, rule, fragment string }{
		{"workflow takes an argument", `run -> verifyNamed(bucket: string) { _ -> halt }`, "I9", "takes nothing"},
		{"no mapping", `run -> verifyStorage()`, "I7", "has no mapping"},
		{"deadline is not a duration", `run within "soon" -> verifyStorage() { _ -> halt }`, "I10", "positive duration"},
		{"deadline is zero", `run once within "0s" -> verifyStorage() { _ -> halt }`, "I10", "positive duration"},
		{"deadline is negative", `run within "-5s" -> verifyStorage() { _ -> halt }`, "I10", "positive duration"},
		{"missing variant", `run -> verifyStorage() {
            Ok(_) -> proceed
            Err(StorageError.Missing) -> halt
        }`, "I7", "Err(Unreachable)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			diags := checkStartup(t, "adapter startup Preflight {\n    "+c.route+"\n}")
			found := false
			for _, d := range diags {
				found = found || (d.Rule == c.rule && strings.Contains(d.Msg, c.fragment))
			}
			if !found {
				t.Errorf("diags = %v, want %s containing %q", diags, c.rule, c.fragment)
			}
		})
	}
}

func TestStartupRouteCallsAWorkflow(t *testing.T) {
	src := `adapter startup Preflight {
    run -> startupHelper() { _ -> halt }
}`
	diags := checkSrc(t, `activity startupHelper() -> int {
    return 1
}`, src)
	found := false
	for _, d := range diags {
		found = found || d.Rule == "B6"
	}
	if !found {
		t.Errorf("diags = %v, want B6", diags)
	}
}

func TestStartupMappingTakesOnlyProceedOrHalt(t *testing.T) {
	for _, word := range []string{"ack", "done", "retry"} {
		_, errs := parser.Parse("adapter startup Preflight {\n    run -> verifyStorage() {\n        _ -> " + word + "\n    }\n}")
		if len(errs) == 0 || !strings.Contains(errs[0].Msg, "expected 'proceed' or 'halt'") {
			t.Errorf("%s: errs = %v", word, errs)
		}
	}
}

func TestAStartupAdapterTakesOnlyRunRoutes(t *testing.T) {
	_, errs := parser.Parse("adapter startup Preflight {\n    every \"1h\" -> verifyStorage() { _ -> proceed }\n}")
	if len(errs) == 0 || !strings.Contains(errs[0].Msg, "expected 'run' to start a startup route") {
		t.Errorf("errs = %v", errs)
	}
}
