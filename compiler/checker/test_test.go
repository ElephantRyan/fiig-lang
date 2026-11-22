package checker

import (
	"testing"
)

// ADR 0091: tests, fakes, and expect.

// testProgram is a small package for tests to check against: a rule, a
// capability group with two operations, and an activity that calls one.
var testProgram = map[string]string{
	"rules/double.fiig": `rule double(n: int) -> int {
    return n * 2;
}
`,
	"capabilities/ledger.fiig": `capability Ledger {
    gateway record(entry: string) -> Result<None, string>
        requires Socket
    {
        return Ok(None);
    }
    gateway total() -> Result<int, string>
        requires Socket
    {
        return Ok(0);
    }
}
`,
	"activities/pay.fiig": `activity pay(entry: string) -> Result<None, string> {
    Ledger.record(entry)?;
    return Ok(None);
}
`,
}

// checkWithTests checks testProgram plus extra, keyed by path.
func checkWithTests(extra map[string]string) []Diagnostic {
	sources := map[string]string{}
	for k, v := range testProgram {
		sources[k] = v
	}
	for k, v := range extra {
		sources[k] = v
	}
	diags, _ := CheckSources(sources, nil, "", nil)
	return diags
}

func TestATestThatFitsChecksClean(t *testing.T) {
	got := checkWithTests(map[string]string{"tests/pays.fiig": `test paysLedger {
    fake Ledger {
        gateway record(entry: string) -> Result<None, string> {
            expect entry == "a";
            return Ok(None);
        }
    }
    expect pay("a") is Ok(_);
    result = pay("b");
    expect result is Ok(nothing);
    expect nothing == None;
    expect double(2) == 4;
    expect calls(Ledger.record) == 2;
}
`})
	if len(got) != 0 {
		t.Fatalf("want a clean build, got %v", got)
	}
}

func TestTestBugClasses(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string]string
		rule  string
		n     int
	}{
		{"a program file names a test helper", map[string]string{
			"tests/helper.fiig": `rule helper() -> int {
    return 1;
}
`,
			"activities/user.fiig": `activity user() -> int {
    return helper();
}
`,
		}, "T1", 1},
		{"expect outside tests/", map[string]string{"rules/bad.fiig": `rule bad() -> bool {
    expect true;
    return true;
}
`}, "T2", 1},
		{"expect takes a bool, and is a pattern that fits", map[string]string{"tests/t3.fiig": `test t3 {
    expect 1;
    expect double(1) is Ok(x);
    expect pay("a") is Some(x);
    expect pay("a") is Err(None);
}
`}, "T3", 4},
		{"a fake doesn't match what it replaces", map[string]string{"tests/t4.fiig": `test t4 {
    fake Ledger {
        gateway record(entry: int) -> Result<None, string> {
            return Ok(None);
        }
    }
    expect pay("a") is Ok(_);
}
`}, "T4", 1},
		{"a fake the test never reaches", map[string]string{"tests/t5.fiig": `test t5 {
    fake Ledger {
        gateway record(entry: string) -> Result<None, string> {
            return Ok(None);
        }
    }
    expect double(1) == 2;
}
`}, "T5", 1},
		{"a fake lacks an operation the test reaches", map[string]string{"tests/t6.fiig": `test t6 {
    fake Ledger {
        gateway total() -> Result<int, string> {
            return Ok(1);
        }
    }
    expect pay("a") is Ok(_);
}
`}, "T6", 1},
		{"a fake of a capability with no declaration", map[string]string{"tests/t7.fiig": `test t7 {
    fake Mailer {
        gateway send(to: string) -> Result<None, string> {
            return Ok(None);
        }
    }
    expect double(1) == 2;
}
`}, "T7", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := checkWithTests(c.extra)
			if counts := rules(got); counts[c.rule] != c.n || len(got) != c.n {
				t.Fatalf("want %d %s and nothing else, got %v", c.n, c.rule, got)
			}
		})
	}
}

// ADR 0091 §5: only a test sleeps; program code would hold a request.
func TestClockSleepIsForTests(t *testing.T) {
	diags := checkSrc(t, "activity wait() -> int {\n    Clock.sleep(1000);\n    return Clock.now();\n}")
	wantDiag(t, diags, "T2", "Clock.sleep is for tests")
}
