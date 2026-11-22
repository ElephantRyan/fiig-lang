package format

import "testing"

// TestFormatHeadedEnumAlignsRows is ADR 0046's formatter consequence: the
// header on one line, variant rows aligned by column so the declaration
// reads as a table.
func TestFormatHeadedEnumAlignsRows(t *testing.T) {
	src := "enum DeductionSource(key code: string, layout: FieldLayout) {\n" +
		"    Acme(code: \"ACME\", layout: FieldLayout.FixedWidth),\n" +
		"    Globex(code: \"GLOBEX\", layout: FieldLayout.Csv),\n" +
		"}\n"
	want := "enum DeductionSource(key code: string, layout: FieldLayout) {\n" +
		"    Acme(  code: \"ACME\",   layout: FieldLayout.FixedWidth),\n" +
		"    Globex(code: \"GLOBEX\", layout: FieldLayout.Csv),\n" +
		"}\n"
	once, errs := Format(src)
	if len(errs) > 0 {
		t.Fatalf("format errors: %v", errs)
	}
	if once != want {
		t.Errorf("got:\n%s\nwant:\n%s", once, want)
	}
	if twice, _ := Format(once); twice != once {
		t.Errorf("formatter is not idempotent:\n%s\nvs:\n%s", once, twice)
	}
}

// ADR 0080 §1: a variant's spelling prints after its payload, and after a
// headed enum's row, and survives a second pass.
func TestFormatVariantSpellings(t *testing.T) {
	src := "enum Outcome {\n" +
		"    Paid(receipt: string)   from json   \"paid\",\n" +
		"    PastDue from json \"past_due\",\n" +
		"    Legacy,\n" +
		"}\n"
	want := "enum Outcome {\n" +
		"    Paid(receipt: string) from json \"paid\",\n" +
		"    PastDue from json \"past_due\",\n" +
		"    Legacy,\n" +
		"}\n"
	headed := "enum Region(key code: string) {\n" +
		"    UsEast(code: \"us-east\") from json \"us_east\",\n" +
		"    EuWest(code: \"eu-west\"),\n" +
		"}\n"
	for in, out := range map[string]string{src: want, headed: headed} {
		once, errs := Format(in)
		if len(errs) > 0 {
			t.Fatalf("format errors: %v", errs)
		}
		if once != out {
			t.Errorf("got:\n%s\nwant:\n%s", once, out)
		}
		if twice, _ := Format(once); twice != once {
			t.Errorf("formatter is not idempotent:\n%s\nvs:\n%s", once, twice)
		}
	}
}
