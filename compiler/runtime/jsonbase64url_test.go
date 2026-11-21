package runtime

import (
	"strings"
	"testing"
)

// ADR 0078 §6: a field written `from json base64url` sends its bytes as
// unpadded base64url; every bytes field reads either alphabet, padded or
// not, whatever its clause.

var base64URLProgram = []string{
	`model Message {
    id: string
    raw: bytes from json base64url
    chunks: List<bytes> from json "parts" base64url
    thumb: bytes | None from json base64url
    plain: bytes
}`,
	`enum Upload {
    Sent(body: bytes from json "data" base64url)
}`,
	`rule sample(b: bytes, small: bytes, thumb: bytes | None) -> Message {
    return Message(id: "m1", raw: b, chunks: [b, small], thumb: thumb, plain: b)
}`,
	`conversion writeMessage(b: bytes, small: bytes, thumb: bytes | None) -> bytes {
    return json.encode(sample(b: b, small: small, thumb: thumb))
}`,
	`conversion writePretty(b: bytes, small: bytes, thumb: bytes | None) -> bytes {
    return json.encodePretty(sample(b: b, small: small, thumb: thumb))
}`,
	`rule writeUpload(b: bytes) -> bytes {
    return json.encode(Upload.Sent(body: b))
}`,
	`rule readMessage(body: string) -> Result<Message, JsonError> {
    return json.decode(body)
}`,
}

// pngBytes is bytes whose base64 uses the characters the alphabets differ
// in: standard "iVBORw0KGgr7/w==", base64url "iVBORw0KGgr7_w".
var pngBytes = Bytes{B: []byte("\x89PNG\r\n\x1a\n\xfb\xff")}

func messageArgs(thumb Value) map[string]Value {
	return map[string]Value{"b": pngBytes, "small": Bytes{B: []byte{0xfb}}, "thumb": thumb}
}

func TestABase64URLFieldIsWrittenUnpadded(t *testing.T) {
	in := checkedInterp(t, base64URLProgram...)
	want := `{"id":"m1","raw":"iVBORw0KGgr7_w","parts":["iVBORw0KGgr7_w","-w"],"thumb":"iVBORw0KGgr7_w","plain":"iVBORw0KGgr7/w=="}`
	if got := string(callJSON(t, in, "writeMessage", messageArgs(pngBytes)).(Bytes).B); got != want {
		t.Errorf("json.encode = %s\nwant %s", got, want)
	}
	if got := string(callJSON(t, in, "writeMessage", messageArgs(None{})).(Bytes).B); !strings.Contains(got, `"thumb":null`) {
		t.Errorf("None: %s", got)
	}
	if got := string(callJSON(t, in, "writePretty", messageArgs(pngBytes)).(Bytes).B); !strings.Contains(got, `"raw": "iVBORw0KGgr7_w",`) || !strings.Contains(got, `"plain": "iVBORw0KGgr7/w=="`) {
		t.Errorf("json.encodePretty = %s", got)
	}
	if got := string(callJSON(t, in, "writeUpload", map[string]Value{"b": pngBytes}).(Bytes).B); got != `{"variant":"Sent","data":"iVBORw0KGgr7_w"}` {
		t.Errorf("payload field: %s", got)
	}
}

func TestEveryBytesFieldReadsEitherAlphabet(t *testing.T) {
	in := checkedInterp(t, base64URLProgram...)
	want := string(pngBytes.B)
	for _, text := range []string{
		`iVBORw0KGgr7_w`,   // base64url, unpadded: what the field writes
		`iVBORw0KGgr7_w==`, // base64url, padded
		`iVBORw0KGgr7/w==`, // standard, padded
		`iVBORw0KGgr7/w`,   // standard, unpadded
	} {
		body := `{"id": "m1", "raw": "` + text + `", "parts": ["` + text + `"], "thumb": null, "plain": "` + text + `"}`
		r := callJSON(t, in, "readMessage", map[string]Value{"body": Str(body)}).(Result)
		if !r.Ok {
			t.Errorf("%s: got %v", text, r)
			continue
		}
		f := r.Value.(Record).Fields
		if string(f["raw"].(Bytes).B) != want || string(f["plain"].(Bytes).B) != want || string(f["chunks"].(List).Elems[0].(Bytes).B) != want {
			t.Errorf("%s: got %v", text, r)
		}
	}
	for text, reason := range map[string]string{
		`iVBO+w0K_Ggo`:  "mixes the standard alphabet",
		`iVBO w0KGgr7w`: "isn't base64",
	} {
		body := `{"id": "m1", "raw": "` + text + `", "parts": [], "thumb": null, "plain": "AA"}`
		r := callJSON(t, in, "readMessage", map[string]Value{"body": Str(body)}).(Result)
		if r.Ok || !strings.Contains(r.Value.(EnumValue).String(), reason) || !strings.Contains(r.Value.(EnumValue).String(), "raw") {
			t.Errorf("%s: got %v, want a Mismatch at raw saying %q", text, r, reason)
		}
	}
}
