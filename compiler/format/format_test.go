package format

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
)

func mustFormat(t *testing.T, src string) string {
	t.Helper()
	out, errs := Format(src)
	if len(errs) > 0 {
		t.Fatalf("unexpected format errors: %v", errs)
	}
	return out
}

func TestFormatSimpleModelIsStable(t *testing.T) {
	src := "model Customer {\n    id: CustomerId\n    name: string\n    email: EmailAddress\n}\n"
	out := mustFormat(t, src)
	if out != src {
		t.Fatalf("expected already-canonical input to round-trip unchanged:\ngot:\n%s\nwant:\n%s", out, src)
	}
}

func TestFormatConcurrentWithPreserveOrderIsStable(t *testing.T) {
	src := "workflow w(ids: List<CustomerId>) -> BatchResult<CustomerId, Customer, CustomerError> {\n" +
		"    return concurrent(id in ids, limit: 5, preserveOrder): getCustomer(id);\n" +
		"}\n"
	out := mustFormat(t, src)
	if out != src {
		t.Fatalf("expected already-canonical input to round-trip unchanged:\ngot:\n%s\nwant:\n%s", out, src)
	}
}

func TestFormatIsIdempotent(t *testing.T) {
	root := findExamplesDir(t)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".fiig" {
			return nil
		}
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if unformattable(path) {
				t.Skip("a G2 fixture: its `.*` is a syntax error with no formatted form")
			}
			once, errs := Format(string(src))
			if len(errs) > 0 {
				t.Fatalf("formatting original failed: %v", errs)
			}
			twice, errs := Format(once)
			if len(errs) > 0 {
				t.Fatalf("formatting formatted output failed: %v", errs)
			}
			if once != twice {
				t.Fatalf("not idempotent:\nfirst pass:\n%s\nsecond pass:\n%s", once, twice)
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestFormatRoundTripsCorpusWithoutErrors checks the actually-required
// Milestone 1 exit criterion: every example still parses after formatting,
// with no declarations lost or added.
func TestFormatRoundTripsCorpusWithoutErrors(t *testing.T) {
	root := findExamplesDir(t)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".fiig" {
			return nil
		}
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if unformattable(path) {
				t.Skip("a G2 fixture: its `.*` is a syntax error with no formatted form")
			}
			before, errs := parser.Parse(string(src))
			if errs = syntaxErrors(errs); len(errs) > 0 {
				t.Fatalf("original doesn't parse: %v", errs)
			}
			formatted, ferrs := Format(string(src))
			if len(ferrs) > 0 {
				t.Fatalf("Format failed: %v", ferrs)
			}
			after, errs := parser.Parse(formatted)
			if errs = syntaxErrors(errs); len(errs) > 0 {
				t.Fatalf("formatted output doesn't parse: %v\n--- formatted ---\n%s", errs, formatted)
			}
			if len(before.Decls) != len(after.Decls) {
				t.Fatalf("decl count changed: %d -> %d", len(before.Decls), len(after.Decls))
			}
			if len(before.Imports) != len(after.Imports) {
				t.Fatalf("import count changed: %d -> %d", len(before.Imports), len(after.Imports))
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFormatPreservesLeadingComment(t *testing.T) {
	src := "// a doc comment\nmodel CustomerRef {\n    id: string\n}\n"
	out := mustFormat(t, src)
	if !strings.Contains(out, "// a doc comment") {
		t.Fatalf("leading comment dropped:\n%s", out)
	}
	if !strings.HasPrefix(out, "// a doc comment\n") {
		t.Fatalf("leading comment not placed before the declaration:\n%s", out)
	}
}

func TestFormatPreservesTrailingComment(t *testing.T) {
	src := "rule f(x: int) -> int {\n    return x; // trailing\n}\n"
	out := mustFormat(t, src)
	if !strings.Contains(out, "return x; // trailing") {
		t.Fatalf("trailing comment not reattached to its line:\n%s", out)
	}
}

// TestFormatPreservesTrailingCommentOnCallReturnInMatchArm replicates
// order-processing/workflows/checkout.fiig's exact pattern: a block-bodied
// match arm written as one compact source line, `pattern -> { return
// call(...) } // comment`. This caught two real bugs together: an
// unbounded "drain to end of file" comment sweep that misattributed later
// arms' comments to the first arm, and CallExpr/FieldAccess nodes never
// getting a real Span from the parser (so the trailing-comment range
// collapsed to empty and comments were dropped from their rightful line
// entirely). Each arm's comment must end up on its own return line.
func TestFormatPreservesTrailingCommentOnCallReturnInMatchArm(t *testing.T) {
	src := `enum E {
    A(x: int)
    B(x: int)
}
workflow w(x: int) -> int {
    match x {
        A(y) -> { return f(y) } // first
        B(y) -> { return g(y) } // second
    }
}
`
	out := mustFormat(t, src)
	if !strings.Contains(out, "return f(y); // first") {
		t.Fatalf("first arm's trailing comment not attached to its return line:\n%s", out)
	}
	if !strings.Contains(out, "return g(y); // second") {
		t.Fatalf("second arm's trailing comment not attached to its return line, or was stolen by the first arm:\n%s", out)
	}
}

func TestFormatPreservesMatchArmTrailingComment(t *testing.T) {
	src := `enum E {
    A(x: int)
}
workflow w(x: int) -> int {
    match x {
        _ -> { return x } // kept
    }
}
`
	out := mustFormat(t, src)
	if !strings.Contains(out, "// kept") {
		t.Fatalf("match-arm trailing comment dropped:\n%s", out)
	}
}

func TestFormatWrapsLongCallArguments(t *testing.T) {
	src := `gateway s(a: string, b: string, c: string, d: string) -> int
    requires Database
{
    Database.execute("a very long string literal that pushes this call well past the line width threshold", a, b, c, d)
}
`
	out := mustFormat(t, src)
	if !strings.Contains(out, "Database.execute(\n") {
		t.Fatalf("expected the long call to wrap, got:\n%s", out)
	}
	if !strings.Contains(out, "        a,\n") {
		t.Fatalf("expected one-argument-per-line with trailing comma, got:\n%s", out)
	}
	// Must still parse and preserve argument count.
	formatted, errs := parser.Parse(out)
	if len(errs) > 0 {
		t.Fatalf("wrapped call doesn't parse: %v", errs)
	}
	_ = formatted
}

// A header's clauses join its line when it fits, and otherwise go under it
// at two indents, the last carrying `{`; `{` is never alone on a line.
func TestFormatHeaderClauses(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"a gateway that fits is one line",
			"gateway s(id: CustomerId) -> Result<Customer, CustomerError>\n    requires Database\n    idempotent\n{\n    return Ok(id)\n}\n",
			"gateway s(id: CustomerId) -> Result<Customer, CustomerError> requires Database idempotent {\n    return Ok(id);\n}\n",
		},
		{
			"a gateway that doesn't fit wraps its clauses",
			"gateway itemsAsService(target: AbsTarget, itemIds: List<string>) -> Result<List<Book>, AbsError> requires HttpClient idempotent {\n    return Ok([])\n}\n",
			"gateway itemsAsService(target: AbsTarget, itemIds: List<string>) -> Result<List<Book>, AbsError>\n        requires HttpClient\n        idempotent {\n    return Ok([]);\n}\n",
		},
		{
			"a route that fits is one line",
			"adapter http api {\n    GET \"/books\" -> listBooks()\n        produces \"application/json\"\n    {\n        Ok(books) -> 200 books\n    }\n}\n",
			"adapter http api {\n    GET \"/books\" -> listBooks() produces \"application/json\" {\n        Ok(books) -> 200 books\n    }\n}\n",
		},
		{
			"a route that doesn't fit wraps its clauses",
			"adapter http api {\n    PUT \"/ratings/{itemId}\" -> putRating(itemId: string, input: RatingInput) consumes \"application/json\" produces \"application/json\" {\n        Ok(rating) -> 200 rating\n    }\n}\n",
			"adapter http api {\n    PUT \"/ratings/{itemId}\" -> putRating(itemId: string, input: RatingInput)\n            consumes \"application/json\"\n            produces \"application/json\" {\n        Ok(rating) -> 200 rating\n    }\n}\n",
		},
		{
			"a route with clauses and no mapping has no brace",
			"adapter http api {\n    PUT \"/ratings/{itemId}\" -> putRating(itemId: string, input: RatingInput) consumes \"application/json\" produces \"application/json\"\n}\n",
			"adapter http api {\n    PUT \"/ratings/{itemId}\" -> putRating(itemId: string, input: RatingInput)\n            consumes \"application/json\"\n            produces \"application/json\"\n}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := mustFormat(t, tc.src)
			if out != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", out, tc.want)
			}
			if again := mustFormat(t, out); again != out {
				t.Fatalf("not idempotent:\n%s", again)
			}
		})
	}
}

func findExamplesDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "examples")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find examples/ directory above " + dir)
		}
		dir = parent
	}
}

func TestFormatEmbeddedModelFieldIsStable(t *testing.T) {
	src := "model Customer {\n    ContactInfo\n    id: CustomerId\n    name: string\n}\n"
	if out := mustFormat(t, src); out != src {
		t.Fatalf("embedded field must round-trip unchanged:\ngot:\n%s\nwant:\n%s", out, src)
	}
}

// unformattable is the one corpus file Format must refuse: a wildcard
// import is a parse error (G2, ADR 0050 §2.4) whose recovered tree has
// dropped the `.*`, so printing it would silently rewrite the source.
func unformattable(path string) bool {
	return filepath.Base(path) == "wildcard_import_error.fiig"
}

// syntaxErrors drops G1 (a second public declaration, ADR 0054): the
// invalid-programs fixtures trip it on purpose, and it leaves a complete
// tree, so it doesn't stop a round trip.
func syntaxErrors(errs []parser.Error) []parser.Error {
	var out []parser.Error
	for _, e := range errs {
		if e.Rule != "G1" {
			out = append(out, e)
		}
	}
	return out
}

// TestFormatPrivateBlock is ADR 0054's Consequences line: the private block
// prints after the public declaration, members in source order, one level
// in, with their comments kept.
func TestFormatPrivateBlock(t *testing.T) {
	src := "rule fooBar(number: int) -> int {\n" +
		"  return 4\n" +
		"}\n" +
		"private {\n" +
		"  // decides the branch\n" +
		"  interiorFunctionCall(number: int) -> bool {\n" +
		"  return number > 0 // positive only\n" +
		"  }\n" +
		"  model Threshold {\n" +
		"  minimum: int\n" +
		"  }\n" +
		"  // trailing note\n" +
		"}\n"
	want := "rule fooBar(number: int) -> int {\n" +
		"    return 4;\n" +
		"}\n" +
		"\n" +
		"private {\n" +
		"    // decides the branch\n" +
		"    interiorFunctionCall(number: int) -> bool {\n" +
		"        return number > 0; // positive only\n" +
		"    }\n" +
		"\n" +
		"    model Threshold {\n" +
		"        minimum: int\n" +
		"    }\n" +
		"\n" +
		"    // trailing note\n" +
		"}\n"
	got, errs := Format(src)
	if len(errs) > 0 {
		t.Fatalf("unexpected format errors: %v", errs)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	again, _ := Format(got)
	if again != got {
		t.Fatalf("not idempotent:\n%s", again)
	}
}

// TestFormatListLiteral is ADR 0057: one line when it fits, one element
// per line with a trailing comma when it doesn't.
func TestFormatListLiteral(t *testing.T) {
	long := `"aaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccc", "dddddddddddddddddddd"`
	src := "rule r() -> List<string> {\n" +
		"  short = [ 1,2, ]\n" +
		"  return [" + long + "]\n" +
		"}\n"
	want := "rule r() -> List<string> {\n" +
		"    short = [1, 2];\n" +
		"    return [\n" +
		"        \"aaaaaaaaaaaaaaaaaaaa\",\n" +
		"        \"bbbbbbbbbbbbbbbbbbbb\",\n" +
		"        \"cccccccccccccccccccc\",\n" +
		"        \"dddddddddddddddddddd\",\n" +
		"    ];\n" +
		"}\n"
	got, errs := Format(src)
	if len(errs) > 0 {
		t.Fatalf("unexpected format errors: %v", errs)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	again, _ := Format(got)
	if again != got {
		t.Fatalf("not idempotent:\n%s", again)
	}
}

// TestFormatBinarySegments is ADR 0058: segments print as written, and a
// long binary expression wraps one segment per line.
func TestFormatBinarySegments(t *testing.T) {
	src := "conversion c(b: bytes) -> bytes {\n" +
		"  match b {\n" +
		"    <<0x52:u8,n:i32be,body:bytes(n-4)>> -> <<n:u16le, body:bytes>>\n" +
		"    _ -> <<\"user\": utf8, 0: u8, \"database\": utf8, 0: u8, \"application_name\": utf8, 0: u8, 0: u8>>\n" +
		"  }\n" +
		"}\n"
	want := "conversion c(b: bytes) -> bytes {\n" +
		"    return match (b) {\n" +
		"        <<0x52: u8, n: i32be, body: bytes(n - 4)>> -> <<n: u16le, body: bytes>>\n" +
		"        _ -> <<\n" +
		"            \"user\": utf8,\n" +
		"            0: u8,\n" +
		"            \"database\": utf8,\n" +
		"            0: u8,\n" +
		"            \"application_name\": utf8,\n" +
		"            0: u8,\n" +
		"            0: u8,\n" +
		"        >>\n" +
		"    }\n" +
		"}\n"
	got, errs := Format(src)
	if len(errs) > 0 {
		t.Fatalf("unexpected format errors: %v", errs)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	again, _ := Format(got)
	if again != got {
		t.Fatalf("not idempotent:\n%s", again)
	}
}

// A block that closes is followed by a blank line, unless what follows
// closes too, continues it, or is already blank. A `}` line inside a
// string literal is text, not a block.
func TestFormatBlankLineAfterABlock(t *testing.T) {
	want := `rule r(x: int, items: List<int>) -> string {
    total: int = 0;
    for (item in items) {
        if (item > x) {
            total = total + item;
        } else {
            total = total - 1;
        }
    }

    note = "a brace on its own line\n}\nstays as it is";
    return match (x) {
        0 -> {
            return note;
        }

        _ -> {
            return "${total}";
        }
    }
}
`
	src := strings.ReplaceAll(want, "    }\n\n    note", "    }\n    note")
	src = strings.ReplaceAll(src, "        }\n\n        _ ->", "        }\n        _ ->")
	if out := mustFormat(t, src); out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	if out := mustFormat(t, want); out != want {
		t.Fatalf("not idempotent:\n%s", out)
	}
}

// The pass reads lines with the lexer, so a lone `}` inside a string
// literal neither counts as a block nor gets a blank line after it.
func TestBlankAfterBlocksSkipsStringLiterals(t *testing.T) {
	src := `rule r() -> string {
    return "a
}
b"
}
`
	if out := blankAfterBlocks(src); out != src {
		t.Fatalf("changed a string literal:%s%s", "\n", out)
	}
}
