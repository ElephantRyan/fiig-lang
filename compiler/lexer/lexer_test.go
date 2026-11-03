package lexer

import (
	"os"
	"path/filepath"
	"testing"
)

func kinds(toks []Token) []Kind {
	ks := make([]Kind, len(toks))
	for i, t := range toks {
		ks[i] = t.Kind
	}
	return ks
}

func assertKinds(t *testing.T, src string, want ...Kind) {
	t.Helper()
	toks := New(src).AllTokens()
	got := kinds(toks)
	if len(got) != len(want) {
		t.Fatalf("%q: got %d tokens %v, want %d %v", src, len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%q: token %d = %v, want %v (full: %v)", src, i, got[i], want[i], got)
		}
	}
}

func TestKeywordsAndIdent(t *testing.T) {
	assertKinds(t, "model Customer", KwModel, Ident, EOF)
	assertKinds(t, "workflow requires transaction concurrent retry idempotent",
		KwWorkflow, KwRequires, KwTransaction, KwConcurrent, KwRetry, KwIdempotent, EOF)
	assertKinds(t, "preserveOrder", KwPreserveOrder, EOF)
	assertKinds(t, "activity gateway", KwActivity, KwGateway, EOF)
	// Prelude names (Result, Ok, Err, None, List, BatchResult) are NOT
	// keywords (docs/specification/grammar.md §1) — ordinary identifiers.
	assertKinds(t, "Result Ok Err None List BatchResult",
		Ident, Ident, Ident, Ident, Ident, Ident, EOF)
}

func TestUnderscoreIsNotAnIdentifier(t *testing.T) {
	// Regression test: an earlier version of isLetter wrongly included '_',
	// which made scanIdentOrKeyword swallow bare '_' as Ident("_") before
	// the dedicated Underscore case in scanToken could ever run. Every
	// wildcard pattern in the corpus (Err(_), CustomerError.NotFound(_))
	// depends on '_' lexing as its own token, not as an identifier.
	assertKinds(t, "_", Underscore, EOF)
	assertKinds(t, "Err(_)", Ident, LParen, Underscore, RParen, EOF)

	toks := New("_").AllTokens()
	if toks[0].Text != "_" {
		t.Fatalf("Underscore token Text = %q, want \"_\"", toks[0].Text)
	}
}

func TestNumbers(t *testing.T) {
	toks := New("3").AllTokens()
	if toks[0].Kind != Int || toks[0].Text != "3" {
		t.Fatalf("got %v, want Int(3)", toks[0])
	}
}

// TestFloatLiterals is ADR 0048 §2.4: a fraction needs digits on both sides
// of its dot, and an exponent needs a digit after its optional sign, so
// neither swallows a field access or an identifier.
func TestFloatLiterals(t *testing.T) {
	for _, text := range []string{"3.14", "0.5", "1e9", "1E9", "6.02e23", "1e-9", "2.5E+3"} {
		toks := New(text).AllTokens()
		if len(toks) != 2 || toks[0].Kind != Float || toks[0].Text != text {
			t.Errorf("%q lexed as %v, want one Float", text, toks)
		}
	}
	assertKinds(t, "3.abs", Int, Dot, Ident, EOF)
	assertKinds(t, "3.", Int, Dot, EOF)
	assertKinds(t, "2e", Int, Ident, EOF)
	assertKinds(t, "2e+x", Int, Ident, Plus, Ident, EOF)
	assertKinds(t, "0x1e5", Int, EOF)
	assertKinds(t, "x.y", Ident, Dot, Ident, EOF)
}

func TestPunctuationAndOperators(t *testing.T) {
	assertKinds(t, "{}()<>,:.?=|*->==!=<=>=",
		LBrace, RBrace, LParen, RParen, LAngle, RAngle, Comma, Colon, Dot,
		Question, Equals, Pipe, Star, Arrow, EqEq, NotEq, LtEq, GtEq, EOF)
}

// TestArithmeticOperators is ADR 0019: +, -, /, % are new tokens; * is
// reused from its existing wildcard-import/generic-args role rather than
// gaining a second Kind, so it's already covered above.
func TestArithmeticOperators(t *testing.T) {
	assertKinds(t, "+-/%", Plus, Minus, Slash, Percent, EOF)
}

// TestMinusIsNotConfusedWithArrow guards the two-char lookahead in
// scanPunct: "->" must still win over a bare "-" followed by something
// else entirely, now that "-" is also its own token.
func TestMinusIsNotConfusedWithArrow(t *testing.T) {
	assertKinds(t, "-> -", Arrow, Minus, EOF)
}

func TestLineComment(t *testing.T) {
	assertKinds(t, "model // a comment\nCustomer", KwModel, Ident, EOF)
}

func TestSimpleString(t *testing.T) {
	toks := New(`"hello"`).AllTokens()
	assertKinds(t, `"hello"`, StringStart, StringText, StringEnd, EOF)
	if toks[1].Text != "hello" {
		t.Fatalf("StringText = %q, want \"hello\"", toks[1].Text)
	}
}

func TestEmptyString(t *testing.T) {
	assertKinds(t, `""`, StringStart, StringEnd, EOF)
}

func TestStringEscapes(t *testing.T) {
	toks := New(`"a\"b\\c\nd\te"`).AllTokens()
	if toks[1].Kind != StringText || toks[1].Text != "a\"b\\c\nd\te" {
		t.Fatalf("got %v, want decoded escape text", toks[1])
	}
}

func TestStringInterpolation(t *testing.T) {
	// "Hello, ${name}!" from send_welcome_email.fiig.
	src := `"Hello, ${name}!"`
	assertKinds(t, src,
		StringStart, StringText, InterpStart, Ident, InterpEnd, StringText, StringEnd, EOF)

	toks := New(src).AllTokens()
	if toks[1].Text != "Hello, " {
		t.Fatalf("first StringText = %q", toks[1].Text)
	}
	if toks[3].Text != "name" {
		t.Fatalf("interpolated Ident = %q", toks[3].Text)
	}
	if toks[5].Text != "!" {
		t.Fatalf("trailing StringText = %q", toks[5].Text)
	}
}

func TestStringInterpolationLeadingAndTrailingEmpty(t *testing.T) {
	// No text immediately before '${' or immediately after the matching
	// '}' should not produce spurious empty StringText tokens.
	assertKinds(t, `"${x}"`, StringStart, InterpStart, Ident, InterpEnd, StringEnd, EOF)
}

func TestInterpolationWithNestedBraces(t *testing.T) {
	// The interpolated expression itself contains a '{' '}' pair (an
	// anonymous record literal); the lexer must track brace depth so the
	// inner '}' doesn't prematurely end the interpolation.
	src := `"${f({ id: x })}"`
	toks := New(src).AllTokens()
	if len(New(src).Errors()) != 0 {
		t.Fatalf("unexpected lexer errors: %v", New(src).Errors())
	}
	want := []Kind{
		StringStart, InterpStart,
		Ident, LParen, LBrace, Ident, Colon, Ident, RBrace, RParen,
		InterpEnd, StringEnd, EOF,
	}
	got := kinds(toks)
	if len(got) != len(want) {
		t.Fatalf("got %d tokens %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("token %d = %v, want %v (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestNestedStringInsideInterpolation(t *testing.T) {
	// An interpolated expression can itself contain a string literal.
	src := `"${greet("x")}"`
	toks := New(src).AllTokens()
	want := []Kind{
		StringStart, InterpStart,
		Ident, LParen, StringStart, StringText, StringEnd, RParen,
		InterpEnd, StringEnd, EOF,
	}
	got := kinds(toks)
	if len(got) != len(want) {
		t.Fatalf("got %d tokens %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("token %d = %v, want %v (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestUnterminatedStringReportsError(t *testing.T) {
	l := New(`"abc`)
	l.AllTokens()
	if len(l.Errors()) == 0 {
		t.Fatal("expected an error for an unterminated string literal")
	}
}

func TestIllegalCharacter(t *testing.T) {
	l := New("model # oops")
	l.AllTokens()
	if len(l.Errors()) == 0 {
		t.Fatal("expected an error for an illegal character")
	}
}

func TestPositionsTrackLineAndColumn(t *testing.T) {
	toks := New("model\nCustomer").AllTokens()
	if toks[0].Start.Line != 1 || toks[0].Start.Column != 1 {
		t.Fatalf("model token start = %v, want 1:1", toks[0].Start)
	}
	if toks[1].Start.Line != 2 || toks[1].Start.Column != 1 {
		t.Fatalf("Customer token start = %v, want 2:1", toks[1].Start)
	}
}

// TestCorpusLexesWithoutErrors is a smoke test: every real .fiig file under
// examples/ must lex cleanly (no Illegal tokens, no lexer errors). It
// doesn't validate parsing/semantics — just that every character in the
// actual example corpus is accounted for by the lexical grammar.
func TestCorpusLexesWithoutErrors(t *testing.T) {
	root := findExamplesDir(t)

	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".fiig" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(files) == 0 {
		t.Fatalf("no .fiig files found under %s", root)
	}

	for _, f := range files {
		f := f
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("reading %s: %v", f, err)
			}
			l := New(string(src))
			toks := l.AllTokens()
			if errs := l.Errors(); len(errs) > 0 {
				t.Fatalf("%d lexer error(s) in %s: %v", len(errs), f, errs)
			}
			for _, tok := range toks {
				if tok.Kind == Illegal {
					t.Fatalf("illegal token %v in %s", tok, f)
				}
			}
		})
	}
}

// findExamplesDir walks upward from the current package directory to find
// the repo's examples/ folder, so the test works regardless of the working
// directory `go test` is invoked from.
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

// ADR 0061: `\r` and `\$`.
func TestCarriageReturnAndDollarEscapes(t *testing.T) {
	src := `"GET\r\n\${x} costs $5"`
	l := New(src)
	toks := l.AllTokens()
	assertKinds(t, src, StringStart, StringText, StringEnd, EOF)
	if want := "GET\r\n${x} costs $5"; toks[1].Text != want {
		t.Fatalf("StringText = %q, want %q", toks[1].Text, want)
	}
	if len(l.Errors()) > 0 {
		t.Fatalf("unexpected errors: %v", l.Errors())
	}
}

// ADR 0061 §2: a string's value doesn't depend on the file's line endings.
func TestCRLFInsideStringReadsAsLF(t *testing.T) {
	lf := New("\"a\nb\"").AllTokens()[1].Text
	l := New("\"a\r\nb\"")
	crlf := l.AllTokens()[1].Text
	if len(l.Errors()) > 0 {
		t.Fatalf("unexpected errors: %v", l.Errors())
	}
	if lf != "a\nb" || crlf != lf {
		t.Fatalf("LF file gives %q, CRLF file gives %q; want both %q", lf, crlf, "a\nb")
	}
}

func TestLoneCarriageReturnInsideStringIsAnError(t *testing.T) {
	l := New("\"a\rb\"")
	l.AllTokens()
	if len(l.Errors()) != 1 {
		t.Fatalf("errors = %v, want one lone-CR error", l.Errors())
	}
}

// Outside a string, CR is still whitespace.
func TestCRLFBetweenTokensIsWhitespace(t *testing.T) {
	assertKinds(t, "model\r\nOrder", KwModel, Ident, EOF)
}
