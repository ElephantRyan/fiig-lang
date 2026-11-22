package runtime

import (
	"encoding/json"
	"strings"
	"testing"
)

// ADR 0078: bytes in JSON is standard base64, padded, and reads either
// alphabet with or without padding.

var bytesProgram = []string{
	`model Attachment {
    name: string
    content: bytes
}`,
	`rule readAttachment(body: string) -> Result<Attachment, JsonError> {
    return json.decode(body);
}`,
	`rule writeAttachment(name: string, content: bytes) -> bytes {
    return json.encode(Attachment(name: name, content: content));
}`,
	`rule writeBytes(b: bytes) -> bytes {
    return json.encode(b);
}`,
	`rule fromTree(text: string) -> Result<bytes, JsonError> {
    return json.decode(JsonValue.Text(value: text));
}`,
}

func TestBytesEncodeAsPaddedStandardBase64(t *testing.T) {
	in := checkedInterp(t, bytesProgram...)
	png := Bytes{B: []byte("\x89PNG\r\n\x1a\n\xfb\xff")}
	if got := string(callJSON(t, in, "writeAttachment", map[string]Value{"name": Str("logo.png"), "content": png}).(Bytes).B); got != `{"name":"logo.png","content":"iVBORw0KGgr7/w=="}` {
		t.Errorf("json.encode = %s", got)
	}
	if got := string(callJSON(t, in, "writeBytes", map[string]Value{"b": png}).(Bytes).B); got != `"iVBORw0KGgr7/w=="` {
		t.Errorf("json.encode of bytes = %s, want a base64 string", got)
	}
	// fiig run -json's tagged form printed bytes as null before.
	if got, _ := json.Marshal(EncodeJSON(png)); string(got) != `"iVBORw0KGgr7/w=="` {
		t.Errorf("EncodeJSON = %s", got)
	}
}

func TestBytesDecodeFromEitherAlphabet(t *testing.T) {
	in := checkedInterp(t, bytesProgram...)
	want := "\x89PNG\r\n\x1a\n\xfb\xff"
	for _, content := range []string{
		`iVBORw0KGgr7/w==`,     // standard, padded
		`iVBORw0KGgr7/w`,       // standard, unpadded
		`iVBORw0KGgr7_w`,       // URL-safe, as Gmail sends it
		`iVBORw0K\nGgr7/w==\n`, // wrapped, as GitHub's contents API sends it
	} {
		r := callJSON(t, in, "readAttachment", map[string]Value{"body": Str(`{"name": "logo.png", "content": "` + content + `"}`)}).(Result)
		if !r.Ok || string(r.Value.(Record).Fields["content"].(Bytes).B) != want {
			t.Errorf("%s: got %v", content, r)
		}
	}
	for content, reason := range map[string]string{
		`"iVBO+w0K_Ggo="`: "mixes the standard alphabet",
		`"iVBO w0K"`:      "isn't base64",
		`[137, 80]`:       "expected a base64 string, got an array",
	} {
		r := callJSON(t, in, "readAttachment", map[string]Value{"body": Str(`{"name": "x", "content": ` + content + `}`)}).(Result)
		if r.Ok || !strings.Contains(r.Value.(EnumValue).String(), reason) {
			t.Errorf("%s: got %v, want a Mismatch saying %q", content, r, reason)
		}
	}
}

func TestBytesDecodeFromAJsonValue(t *testing.T) {
	in := checkedInterp(t, bytesProgram...)
	r := callJSON(t, in, "fromTree", map[string]Value{"text": Str("AQI=")}).(Result)
	if !r.Ok || string(r.Value.(Bytes).B) != "\x01\x02" {
		t.Errorf("got %v", r)
	}
}
