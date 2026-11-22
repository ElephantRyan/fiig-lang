package format

import "testing"

// ADR 0091: a test, its fakes, and both forms of expect print stably; a
// fake gateway with no `requires` prints its header on one line.
func TestFormatTestDecl(t *testing.T) {
	src := `test chargeIsRecorded {
    fake Ledger {
        gateway record(entry: string) -> Result<None, string> {
            expect entry == "a";
            return Ok(None);
        }
    }

    fake Account {
        apiKey: "sk_test_1"
    }

    result = pay("a");
    expect result is Ok(_);
    expect findRating("dune") is Err(RatingError.Invalid(field, _));
    expect field == "score";
    expect calls(Ledger.record) == 1;
}
`
	if got := mustFormat(t, src); got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
	if got := mustFormat(t, "test t {\n    expect  x   is   Ok( y );\n}\n"); got != "test t {\n    expect x is Ok(y);\n}\n" {
		t.Errorf("got:\n%s", got)
	}
}
