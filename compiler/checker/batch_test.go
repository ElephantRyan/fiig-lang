package checker

import "testing"

// ADR 0148: a BatchResult is read with `for (key, outcome in batch)` and
// `.length()`, and nothing else.
func TestReadingABatch(t *testing.T) {
	const half = "conversion half(n: int) -> Result<int, string> {\n    return Ok(n);\n}"
	fn := func(body string) string {
		return "conversion f(ns: List<int>) -> int {\n    halves = concurrent(n in ns): half(n);\n" + body + "\n}"
	}

	// The two names are the element, K, and its outcome, Result<S, F>, so
	// both type-check where those types are wanted; `_` discards either.
	ok := fn(`    total: int = halves.length();
    for (n, outcome in halves) {
        match (outcome) {
            Ok(h) -> {
                total = total + n + h;
            },
            Err(why) -> {
                total = total + why.length();
            }
        }
    }
    for (_, outcome in halves) {
        match (outcome) {
            Ok(_) -> {
            },
            Err(_) -> {
            }
        }
    }
    for (n, _ in halves) {
        total = total + n;
    }
    return total;`)
	if diags := checkSrc(t, half, ok); len(diags) > 0 {
		t.Fatalf("reading a batch: %v", diags)
	}

	// The key is K, not the outcome.
	wantDiag(t, checkSrc(t, half, fn(`    for (n, _ in halves) {
        s: string = n;
    }
    return 0;`)), "A1", "")

	// One name is I1, naming the two-name form.
	wantDiag(t, checkSrc(t, half, fn(`    for (item in halves) {
    }
    return 0;`)), "I1", "for over a BatchResult binds two names, `for (key, outcome in batch)`")

	// length is its only method.
	wantDiag(t, checkSrc(t, half, fn(`    return halves.first();`)), "K1", "")
}

// Two names over a List is I1, and the message names both pair kinds.
func TestTwoNamesOverAList(t *testing.T) {
	wantDiag(t, checkSrc(t, "conversion f(ns: List<int>) -> int {\n    for (a, b in ns) {\n    }\n    return 0;\n}"),
		"I1", "which is for a Map or a BatchResult")
}
