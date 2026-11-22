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
	return runJSONWith(t, root, "")
}

// runJSONWith is runJSON with -env-file envFile.
func runJSONWith(t *testing.T, root, envFile string) (int, map[string]Result) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// Read while the run writes, so a package with many tests doesn't
	// fill the pipe and block it.
	lines := make(chan []string, 1)
	go func() {
		var read []string
		scanner := bufio.NewScanner(r)
		scanner.Buffer(nil, 1<<20)
		for scanner.Scan() {
			read = append(read, scanner.Text())
		}
		io.Copy(io.Discard, r)
		lines <- read
	}()
	stdout := os.Stdout
	os.Stdout = w
	code := run(root, "", "", envFile, 5*time.Second, false, true, nil, nil)
	os.Stdout = stdout
	w.Close()
	results := map[string]Result{}
	for _, line := range <-lines {
		var res Result
		if err := json.Unmarshal([]byte(line), &res); err != nil {
			t.Fatalf("not a result line: %q", line)
		}
		results[res.Name] = res
	}
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
		"config/shop.fiig": `wire Shop {
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

// Hearsay G53 and G54: a `Secret | None` field is faked by a literal or
// None, and -env-file gives a test what it reads but doesn't fake.
func TestOptionalConfigInTests(t *testing.T) {
	files := withTests(map[string]string{
		"config/shop.fiig": `wire Shop {
    url: string | None from env "SHOP_URL"
    token: Secret | None from env "SHOP_TOKEN"
}
`,
		"activities/token_set.fiig": `activity tokenSet() -> bool {
    match Shop.token {
        None -> false
        given -> true
    }
}
`,
		"activities/url_set.fiig": `activity urlSet() -> bool {
    match Shop.url {
        None -> false
        given -> true
    }
}
`,
		"tests/token.fiig": `test aLiteralIsTheToken {
    fake Shop { token: "t-1" }
    expect tokenSet()
}

test noneIsNoToken {
    fake Shop { token: None }
    expect not tokenSet()
}
`,
		"tests/url.fiig": `test urlFromTheEnvFile {
    expect urlSet()
}
`,
	})
	root := writePackage(t, files)
	envFile := filepath.Join(root, "test.env")
	if err := os.WriteFile(envFile, []byte("SHOP_URL=https://x.test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, results := runJSONWith(t, root, envFile)
	if code != 0 || len(results) != 3 {
		t.Fatalf("want 3 passing tests and exit 0, got %d: %+v", code, results)
	}
	// Without the file, reading the optional url fails the test naming it
	// (ADR 0091 §5, Hearsay G68), rather than reading None.
	code, results = runJSON(t, root)
	failed := results["urlFromTheEnvFile"]
	if code != 1 || failed.Outcome == "pass" || len(failed.Failures) == 0 || !strings.Contains(failed.Failures[0].Message, "this test reads Shop.url (SHOP_URL), which it doesn't fake") {
		t.Fatalf("want urlFromTheEnvFile to fail naming Shop.url, got %d: %+v", code, results)
	}
}

// Hearsay G68: a test that expects an optional config to be unset says so,
// with `fake ... { field: None }` or `NAME=` in the env file. Leaving it out
// isn't one of those ways: the read fails, as a required one does.
func TestAnUnfakedOptionalConfigFailsTheTest(t *testing.T) {
	files := withTests(map[string]string{
		"config/plain.fiig": `wire Plain {
    url: string | None from env "PLAIN_URL"
}
`,
		"activities/url_set.fiig": `activity urlSet() -> bool {
    match Plain.url {
        None -> false
        given -> true
    }
}
`,
		"tests/plain.fiig": `test plainIsUnset {
    expect not urlSet()
}
`,
	})
	root := writePackage(t, files)
	if code, results := runJSON(t, root); code != 1 || results["plainIsUnset"].Outcome == "pass" {
		t.Fatalf("an unfaked optional config passed: %d %+v", code, results)
	}
	envFile := filepath.Join(root, "test.env")
	if err := os.WriteFile(envFile, []byte("PLAIN_URL=\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, results := runJSONWith(t, root, envFile); code != 0 {
		t.Fatalf("PLAIN_URL= in the env file is unset on purpose: %d %+v", code, results)
	}
	files["tests/plain.fiig"] = "test plainIsUnset {\n    fake Plain { url: None }\n    expect not urlSet()\n}\n"
	if code, results := runJSON(t, writePackage(t, files)); code != 0 {
		t.Fatalf("fake Plain { url: None } is unset on purpose: %d %+v", code, results)
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

// ADR 0091 §5 (Hearsay): Clock.sleep in a test moves the test clock forward
// at once, and code under test sees the later time.
func TestClockSleepMovesTheTestClock(t *testing.T) {
	root := writePackage(t, withTests(map[string]string{
		"tests/sleep.fiig": `test aDayLater {
    expect Clock.now() == 1767225600000
    Clock.sleep(24 * 60 * 60 * 1000)
    expect Clock.now() == 1767312000000
}
`,
	}))
	code, results := runJSON(t, root)
	if code != 0 || len(results) != 1 {
		t.Fatalf("want 1 passing test and exit 0, got %d: %+v", code, results)
	}
}
