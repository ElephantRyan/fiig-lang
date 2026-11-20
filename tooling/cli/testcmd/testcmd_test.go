package testcmd

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writePackage writes files, keyed by path, under a new directory.
func writePackage(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, src := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// runJSON runs `fiig test -json` on root and returns the exit code and
// the results by test name.
func runJSON(t *testing.T, root string) (int, map[string]Result) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	code := run(root, "", "", "", 5*time.Second, false, true, nil, nil)
	os.Stdout = stdout
	w.Close()
	results := map[string]Result{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		var res Result
		if err := json.Unmarshal(scanner.Bytes(), &res); err != nil {
			t.Fatalf("not a result line: %q", scanner.Text())
		}
		results[res.Name] = res
	}
	io.Copy(io.Discard, r)
	return code, results
}

var program = map[string]string{
	"enums/problem.fiig": `enum Problem {
    TooBig(limit: int)
    Blank
}
`,
	"rules/check_size.fiig": `rule checkSize(n: int) -> Result<int, Problem> {
    if n > 10 {
        return Err(Problem.TooBig(10))
    }
    return Ok(n)
}
`,
	"capabilities/ledger.fiig": `capability Ledger {
    gateway record(entry: string) -> Result<None, string>
        requires Socket
    {
        return Ok(None)
    }
}
`,
	"activities/pay.fiig": `activity pay(entry: string) -> Result<None, string> {
    Ledger.record(entry)?
    return Ok(None)
}
`,
}

func withTests(tests map[string]string) map[string]string {
	files := map[string]string{}
	for k, v := range program {
		files[k] = v
	}
	for k, v := range tests {
		files[k] = v
	}
	return files
}

func TestPassingTestsExitZero(t *testing.T) {
	root := writePackage(t, withTests(map[string]string{
		"tests/too_big.fiig": `test tooBigIsRefused {
    expect checkSize(11) is Err(problem)
    expect problem == Problem.TooBig(10)
    expect checkSize(3) is Ok(_)
}
`,
		"tests/pays.fiig": `test paysTheLedger {
    fake Ledger {
        gateway record(entry: string) -> Result<None, string> {
            expect entry == "o-1"
            return Ok(None)
        }
    }
    expect pay("o-1") is Ok(_)
    expect calls(Ledger.record) == 1
    expect Clock.now() == 1767225600000
}
`,
	}))
	code, results := runJSON(t, root)
	if code != 0 || len(results) != 2 {
		t.Fatalf("want 2 passing tests and exit 0, got %d: %+v", code, results)
	}
}

func TestFailuresAreReportedWithTheirValues(t *testing.T) {
	root := writePackage(t, withTests(map[string]string{
		"tests/fails.fiig": `test failsOnPurpose {
    expect checkSize(3) == checkSize(3) is Ok(_)
}
`,
	}))
	if code, _ := runJSON(t, root); code != 2 {
		t.Fatalf("a test that doesn't check: want exit 2, got %d", code)
	}

	root = writePackage(t, withTests(map[string]string{
		"tests/fails.fiig": `test failsOnPurpose {
    expect checkSize(3) is Err(_)
    expect checkSize(11) is Ok(n)
    expect n == 99
}
`,
		"tests/compares.fiig": `test comparesWrongly {
    expect checkSize(11) is Err(problem)
    expect problem == Problem.Blank
    expect 1 + 1 == 3
}
`,
	}))
	code, results := runJSON(t, root)
	if code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
	fails := results["failsOnPurpose"].Failures
	// The failed `is Err(_)` binds nothing, so the test goes on; the failed
	// `is Ok(n)` binds n, so it stops before `expect n == 99`.
	if len(fails) != 2 || fails[0].Value != "Ok(3)" || fails[1].Line != 3 || fails[1].Source != "expect checkSize(11) is Ok(n)" {
		t.Fatalf("want two `is` failures with their values, got %+v", fails)
	}
	compares := results["comparesWrongly"].Failures
	if len(compares) != 2 || compares[0].Left != "Problem.TooBig(10)" || compares[0].Right != "Problem.Blank" || compares[1].Left != "2" {
		t.Fatalf("want both comparisons reported with each side, got %+v", compares)
	}
}

func TestAConfigIsFakedPerTest(t *testing.T) {
	root := writePackage(t, withTests(map[string]string{
		"config/shop.fiig": `config Shop {
    greeting: string from env "SHOP_GREETING"
    token: Secret from env "SHOP_TOKEN"
}
`,
		"activities/greet.fiig": `activity greet(name: string) -> string {
    if Shop.token.matches("t-1") {
        return "${Shop.greeting}, ${name}"
    }
    return "who are you?"
}
`,
		"tests/greets.fiig": `test greetsByConfig {
    fake Shop {
        greeting: "Hello"
        token: "t-1"
    }
    expect greet("Ada") == "Hello, Ada"
}
`,
		"tests/unfaked.fiig": `test readsUnfakedConfig {
    expect greet("Ada") == "Hello, Ada"
}
`,
	}))
	code, results := runJSON(t, root)
	if code != 1 || results["greetsByConfig"].Outcome != "pass" {
		t.Fatalf("want greetsByConfig to pass, got %d: %+v", code, results)
	}
	unfaked := results["readsUnfakedConfig"].Failures
	if len(unfaked) != 1 || !strings.Contains(unfaked[0].Message, "SHOP_TOKEN") {
		t.Fatalf("want one failure naming SHOP_TOKEN, got %+v", unfaked)
	}
}

// ADR 0102 §2: a test file holds many tests, sharing its private block.
func TestManyTestsInOneFileShareHelpers(t *testing.T) {
	root := writePackage(t, withTests(map[string]string{
		"tests/check_size.fiig": `test smallIsFine {
    expect checkSize(small()) is Ok(_)
}

test bigIsRefused {
    expect checkSize(small() + 8) is Err(_)
}

private {
    small() -> int {
        return 3
    }
}
`,
	}))
	code, results := runJSON(t, root)
	if code != 0 || len(results) != 2 {
		t.Fatalf("want 2 passing tests and exit 0, got %d: %+v", code, results)
	}
}
