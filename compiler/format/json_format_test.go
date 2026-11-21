package format

import "testing"

// ADR 0073 §7: keyword field names and `from json` names print as written.
func TestFormatJsonFieldNames(t *testing.T) {
	want := "model Page {\n" +
		"    limit: int\n" +
		"    contentType: string from json \"content-type\"\n" +
		"}\n"
	got, errs := Format("model Page {\n  limit:int\n    contentType : string  from json \"content-type\"\n}")
	if len(errs) > 0 {
		t.Fatalf("format errors: %v", errs)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// ADR 0078 §6: `base64url` prints after the member name, if any, and a
// second format changes nothing.
func TestFormatBase64URLFields(t *testing.T) {
	want := "model Message {\n" +
		"    raw: bytes from json base64url\n" +
		"    data: bytes from json \"raw_data\" base64url\n" +
		"}\n\n" +
		"enum Upload {\n" +
		"    Sent(body: bytes from json base64url)\n" +
		"}\n"
	got, errs := Format("model Message {\n  raw:bytes   from json  base64url\n    data : bytes from json \"raw_data\"  base64url\n}\nenum Upload {\n Sent(body: bytes from json base64url)\n}")
	if len(errs) > 0 {
		t.Fatalf("format errors: %v", errs)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	again, _ := Format(got)
	if again != got {
		t.Errorf("not idempotent:\n%s", again)
	}
}
