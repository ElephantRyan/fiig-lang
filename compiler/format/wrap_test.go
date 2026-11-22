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
		"    return Ok(SettingsChange(absUrl: checkUrl(\"absUrl\", change.absUrl)?, serviceToken: checkText(\"serviceToken\", change.serviceToken)?, mirrorEnabled: change.mirrorEnabled));\n}\n"
	want := "rule check(change: SettingsChange) -> Result<SettingsChange, SettingsChangeError> {\n" +
		"    return Ok(\n" +
		"        SettingsChange(\n" +
		"            absUrl: checkUrl(\"absUrl\", change.absUrl)?,\n" +
		"            serviceToken: checkText(\"serviceToken\", change.serviceToken)?,\n" +
		"            mirrorEnabled: change.mirrorEnabled,\n" +
		"        ),\n" +
		"    );\n" +
		"}\n"
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
	src := "rule books() -> List<Book> {\n    return [Book(itemId: \"dune-here\", libraryId: \"lib-1\", title: \"Dune\", authors: \"Frank Herbert\", asin: \"B0DUNE\"), Book(itemId: \"b\", libraryId: \"l\", title: \"t\", authors: \"a\", asin: \"x\")];\n}\n"
	out := mustFormat(t, src)
	if !strings.Contains(out, "        Book(\n            itemId: \"dune-here\",\n") || !strings.Contains(out, "        Book(itemId: \"b\", libraryId: \"l\", title: \"t\", authors: \"a\", asin: \"x\"),\n") {
		t.Fatalf("got:\n%s", out)
	}
	if again := mustFormat(t, out); again != out {
		t.Fatalf("not stable:\n%s", again)
	}
}

// A statement whose value is a call followed by `?`, or a list form like
// mapErr, wraps at its opening `(` too (Hearsay's absclient: lines of 120 to
// 140 columns that the formatter used to leave whole).
func TestAStatementWrapsAtItsOpeningParen(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"a call and ?",
			"rule r(abs: Target, itemIdSegment: string) -> Result<Reply, AbsError> {\n" +
				"    reply = absclient.absReply(HttpClient.send(absclient.absGet(abs.url, \"/api/items/${itemIdSegment}\", abs.token.reveal())))?;\n" +
				"    return Ok(reply);\n}\n",
			"rule r(abs: Target, itemIdSegment: string) -> Result<Reply, AbsError> {\n" +
				"    reply = absclient.absReply(\n" +
				"        HttpClient.send(\n" +
				"            absclient.absGet(abs.url, \"/api/items/${itemIdSegment}\", abs.token.reveal()),\n" +
				"        ),\n" +
				"    )?;\n" +
				"    return Ok(reply);\n" +
				"}\n",
		},
		{
			"mapErr breaks after its `:`",
			"rule r(reply: Reply) -> Result<List<AbsLibrary>, AbsError> {\n" +
				"    found: AbsLibrariesReply = mapErr(e in json.decode(reply.body)): absclient.AbsError.Unreadable(failure: e)?;\n" +
				"    return Ok(found.libraries);\n}\n",
			"rule r(reply: Reply) -> Result<List<AbsLibrary>, AbsError> {\n" +
				"    found: AbsLibrariesReply = mapErr(e in json.decode(reply.body)):\n" +
				"            absclient.AbsError.Unreadable(failure: e)?;\n" +
				"    return Ok(found.libraries);\n" +
				"}\n",
		},
		{
			// Hearsay: the body used to wrap at its own `(`, leaving a lone
			// `e,` on a line of its own.
			"a short mapErr body stays whole",
			"workflow w(checked: SettingsChange, userId: string) -> Result<None, AdminError> {\n" +
				"    mapErr(e in settings.applySettingsChange(checked, admin.identity.userId)): adminErrorOfChange(e)?;\n" +
				"    return Ok(None);\n}\n",
			"workflow w(checked: SettingsChange, userId: string) -> Result<None, AdminError> {\n" +
				"    mapErr(e in settings.applySettingsChange(checked, admin.identity.userId)):\n" +
				"            adminErrorOfChange(e)?;\n" +
				"    return Ok(None);\n" +
				"}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := mustFormat(t, tc.src)
			if out != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", out, tc.want)
			}
			if again := mustFormat(t, out); again != out {
				t.Fatalf("not stable:\n%s", again)
			}
			for _, line := range strings.Split(out, "\n") {
				if len(line) > maxLineWidth {
					t.Errorf("line over %d: %q", maxLineWidth, line)
				}
			}
		})
	}
}

// A signature too long for one line puts `-> Type` under it, with the
// clauses at two indents, so the parameters stay on one line when they fit
// (Hearsay: `changeSettings(`, a parameter per line, then `) -> ...`). When
// they don't, they go one per line with `)` ending the last.
func TestALongSignatureWrapsAtItsResultType(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"the parameters fit",
			"workflow changeSettings(admin: accounts.Admin, change: settings.SettingsChange) -> Result<settings.SettingsView, AdminError> {\n" +
				"    return Ok(settings.describeSettings());\n}\n",
			"workflow changeSettings(admin: accounts.Admin, change: settings.SettingsChange)\n" +
				"        -> Result<settings.SettingsView, AdminError> {\n" +
				"    return Ok(settings.describeSettings());\n" +
				"}\n",
		},
		{
			"the parameters fit, with a clause",
			"capability AbsCatalog {\n" +
				"    gateway pageAsService(abs: absclient.AbsTarget, libraryId: string, page: int, size: int) -> Result<BookPage, absclient.AbsError> requires HttpClient {\n" +
				"        return Ok(page);\n    }\n}\n",
			"capability AbsCatalog {\n" +
				"    gateway pageAsService(abs: absclient.AbsTarget, libraryId: string, page: int, size: int)\n" +
				"            -> Result<BookPage, absclient.AbsError>\n" +
				"            requires HttpClient {\n" +
				"        return Ok(page);\n" +
				"    }\n" +
				"}\n",
		},
		{
			"the parameters don't fit",
			"gateway chargeCustomer(orderId: string, amount: int, customer: customers.Customer, reference: string) -> Result<string, PaymentError> requires PaymentGateway {\n" +
				"    return Ok(reference);\n}\n",
			"gateway chargeCustomer(\n" +
				"    orderId: string,\n" +
				"    amount: int,\n" +
				"    customer: customers.Customer,\n" +
				"    reference: string)\n" +
				"        -> Result<string, PaymentError>\n" +
				"        requires PaymentGateway {\n" +
				"    return Ok(reference);\n" +
				"}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := mustFormat(t, tc.src)
			if out != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", out, tc.want)
			}
			if again := mustFormat(t, out); again != out {
				t.Fatalf("not stable:\n%s", again)
			}
		})
	}
}
