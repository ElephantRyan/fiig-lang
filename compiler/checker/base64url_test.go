package checker

import "testing"

// ADR 0078 §6: `from json base64url` only on a field holding bytes (J4).

func TestJ4RefusesBase64URLOnAFieldThatIsntBytes(t *testing.T) {
	wantDiag(t, checkSrc(t, `model Message {
    raw: string from json base64url
}`), "J4", "Message.raw is written `from json base64url`, but its type string isn't bytes")
	wantDiag(t, checkSrc(t, `model Part {
    body: bytes
}`, `model Message {
    part: Part from json "p" base64url
}`), "J4", "its type Part isn't bytes")
	wantDiag(t, checkSrc(t, `model Message {
    sizes: List<int> from json base64url
}`), "J4", "List<int>")
	wantDiag(t, checkSrc(t, `enum Upload {
    Sent(name: string from json base64url)
}`), "J4", "Upload.Sent.name")
}

func TestBase64URLIsLegalOnBytesAllTheWayDown(t *testing.T) {
	if diags := checkSrc(t, `model Message {
    raw: bytes from json base64url
    renamed: bytes from json "raw_data" base64url
    thumb: bytes | None from json base64url
    parts: List<bytes> from json base64url
    keys: Map<string, bytes> from json base64url
    maybe: List<bytes | None> | None from json base64url
}`, `enum Upload {
    Sent(body: bytes from json base64url)
}`); len(diags) > 0 {
		t.Errorf("diags = %v", diags)
	}
}
