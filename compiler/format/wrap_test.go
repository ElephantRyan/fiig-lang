package format

import (
	"strings"
	"testing"
)

// Hearsay: a long call inside a wrapped call wrapped too, one level further
// in, including one followed by `?`; before, the inner call printed flat,
// as one 575-character line.
func TestANestedLongCallWraps(t *testing.T) {
	src := "rule check(change: SettingsChange) -> Result<SettingsChange, SettingsChangeError> {\n" +
		"    return Ok(SettingsChange(absUrl: checkUrl(\"absUrl\", change.absUrl)?, serviceToken: checkText(\"serviceToken\", change.serviceToken)?, mirrorEnabled: change.mirrorEnabled))\n}\n"
	want := "rule check(change: SettingsChange) -> Result<SettingsChange, SettingsChangeError> {\n" +
		"    return Ok(\n" +
		"        SettingsChange(\n" +
		"            absUrl: checkUrl(\"absUrl\", change.absUrl)?,\n" +
		"            serviceToken: checkText(\"serviceToken\", change.serviceToken)?,\n" +
		"            mirrorEnabled: change.mirrorEnabled,\n" +
		"        ),\n" +
		"    )\n}\n"
	out := mustFormat(t, src)
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	if again := mustFormat(t, out); again != out {
		t.Fatalf("not stable:\n%s", again)
	}
	for _, line := range strings.Split(out, "\n") {
		if len(line) > maxLineWidth {
			t.Errorf("line over %d: %q", maxLineWidth, line)
		}
	}
}

// A long element of a wrapped list wraps the same way.
func TestALongCallInAWrappedListWraps(t *testing.T) {
	src := "rule books() -> List<Book> {\n    return [Book(itemId: \"dune-here\", libraryId: \"lib-1\", title: \"Dune\", authors: \"Frank Herbert\", asin: \"B0DUNE\"), Book(itemId: \"b\", libraryId: \"l\", title: \"t\", authors: \"a\", asin: \"x\")]\n}\n"
	out := mustFormat(t, src)
	if !strings.Contains(out, "        Book(\n            itemId: \"dune-here\",\n") || !strings.Contains(out, "        Book(itemId: \"b\", libraryId: \"l\", title: \"t\", authors: \"a\", asin: \"x\"),\n") {
		t.Fatalf("got:\n%s", out)
	}
	if again := mustFormat(t, out); again != out {
		t.Fatalf("not stable:\n%s", again)
	}
}
