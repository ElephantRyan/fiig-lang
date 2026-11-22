package checker

import (
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
)

// ADR 0112: schedule routes, bug classes I5-I7.

const scheduleErrors = `enum RefreshError {
    Unreachable(reason: string)
    Stale
}`

const refreshRates = `workflow refreshRates() -> Result<None, RefreshError> {
    return Ok(None);
}`

const purgeSessions = `workflow purgeSessions(firedAt: int) -> int {
    return firedAt;
}`

const badJob = `workflow badJob(name: string) -> Result<None, RefreshError> {
    return Ok(None);
}`

func checkSchedule(t *testing.T, adapter string) []Diagnostic {
	t.Helper()
	return checkSrc(t, scheduleErrors, refreshRates, purgeSessions, badJob, adapter)
}

func TestScheduleRoutesCheck(t *testing.T) {
	src := `adapter schedule Maintenance {
    every "15m" -> refreshRates() {
        Ok(_) -> done
        Err(RefreshError.Unreachable(_)) -> done
        Err(RefreshError.Stale) -> halt
    }
    cron "0 3 * * *" -> purgeSessions(firedAt: int) {
        _ -> done
    }
}`
	if diags := checkSchedule(t, src); len(diags) > 0 {
		t.Errorf("diags = %v", diags)
	}
}

func TestScheduleRouteBugClasses(t *testing.T) {
	cases := []struct{ name, route, rule, fragment string }{
		{"bad duration", `every "7m" -> refreshRates() { _ -> done }`, "I5", "doesn't divide a day"},
		{"bad cron", `cron "0 3 * *" -> refreshRates() { _ -> done }`, "I5", "has 4 fields"},
		{"never fires", `cron "0 0 31 2 *" -> refreshRates() { _ -> done }`, "I5", "never fires"},
		{"wrong parameter", `every "1h" -> badJob(name: string) { _ -> done }`, "I6", "takes nothing, or one int"},
		{"argument missing", `every "1h" -> purgeSessions() { _ -> done }`, "I6", "takes 1 parameter(s), and the route gives 0"},
		{"no mapping", `every "1h" -> refreshRates()`, "I7", "has no mapping"},
		{"missing variant", `every "1h" -> refreshRates() {
            Ok(_) -> done
            Err(RefreshError.Stale) -> halt
        }`, "I7", "Err(Unreachable)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			diags := checkSchedule(t, "adapter schedule Maintenance {\n    "+c.route+"\n}")
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

func TestScheduleMappingTakesOnlyDoneOrHalt(t *testing.T) {
	_, errs := parser.Parse(`adapter schedule Maintenance {
    every "1h" -> refreshRates() {
        _ -> ack
    }
}`)
	if len(errs) == 0 || !strings.Contains(errs[0].Msg, "expected 'done' or 'halt'") {
		t.Errorf("errs = %v", errs)
	}
}
