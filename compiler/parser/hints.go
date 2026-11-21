package parser

import (
	"sort"
	"strings"

	"fiig-lang/compiler/lexer"
)

// A construct other languages have and fiig leaves out produces a cascade of
// parse errors that never say so: `a && b` was three, `elif` seven, a C-style
// `for` twelve (abs-recommendations GAPS G1, G9, G10, G33). When the first
// error is on a line that holds one of those constructs, Parse reports one
// error that names it and what to write instead, and drops the cascade after
// it. The errors before it are as reliable as they were.
//
// docs/improvement-ideas.md B3.

// hintFor is the one-line error for the earliest of errs, or false when none
// of the known constructs is on its line.
func hintFor(toks []lexer.Token, errs []Error) (Error, bool) {
	// Each error in source order, so a real error early in the file doesn't
	// hide a cascade later in it.
	ordered := append([]Error(nil), errs...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].At.Offset < ordered[j].At.Offset })
	for _, e := range ordered {
		// The tokens on the error's line and the line before it: a construct
		// such as `elif a > 0 {` is found at its head, a few tokens before the
		// error.
		var window []lexer.Token
		for _, t := range toks {
			if t.Start.Line >= e.At.Line-1 && t.Start.Line <= e.At.Line {
				window = append(window, t)
			}
		}
		for j, t := range window {
			if msg, ok := hintAt(window, j, e.Msg); ok {
				return Error{Msg: msg, At: t.Start}, true
			}
		}
	}
	return Error{}, false
}

// adjacent reports whether b follows a with nothing between them.
func adjacent(a, b lexer.Token) bool { return a.End.Offset == b.Start.Offset }

func hintAt(w []lexer.Token, j int, firstMsg string) (string, bool) {
	t := w[j]
	next := lexer.Token{}
	if j+1 < len(w) {
		next = w[j+1]
	}
	prev := lexer.Token{}
	if j > 0 {
		prev = w[j-1]
	}
	lineStart := j == 0 || w[j-1].Start.Line != t.Start.Line
	switch {
	case t.Kind == lexer.Illegal && t.Text == "&":
		return "`&&` and `&` aren't operators; write `and` (conditions are `a and b`, `a or b`, `not a`)", true
	case t.Kind == lexer.Pipe && next.Kind == lexer.Pipe && adjacent(t, next):
		return "`||` isn't an operator; write `or` (conditions are `a and b`, `a or b`, `not a`)", true
	case t.Kind == lexer.Illegal && t.Text == "!":
		return "`!` isn't an operator; write `not` (conditions are `a and b`, `a or b`, `not a`)", true
	case t.Kind == lexer.Illegal && t.Text == ";":
		return "a statement ends at the end of its line; remove the `;`", true
	case (t.Kind == lexer.Plus || t.Kind == lexer.Minus) && next.Kind == t.Kind && adjacent(t, next):
		op := "+"
		if t.Kind == lexer.Minus {
			op = "-"
		}
		return "`" + op + op + "` doesn't exist; write `n = n " + op + " 1` (a local is reassigned with `=`, and only when declared with a type, ADR 0053)", true
	case (t.Kind == lexer.Plus || t.Kind == lexer.Minus || t.Kind == lexer.Star || t.Kind == lexer.Slash) && next.Kind == lexer.Equals && adjacent(t, next):
		return "there is no compound assignment; write `n = n " + t.Text + " x` (a local is reassigned with `=`, and only when declared with a type, ADR 0053)", true
	case t.Kind == lexer.Equals && next.Kind == lexer.RAngle && adjacent(t, next):
		return "there are no lambdas (ADR 0056); a list form writes its body in place, as in `map(x in xs): x + 1` or `filter(x in xs): x > 1` (ADR 0093)", true
	case t.Kind == lexer.Colon && strings.Contains(firstMsg, "got :") && hasBefore(w, j, lexer.Question):
		return "there is no `cond ? a : b`; write an `if` statement, or use `math.max` and `math.min` (`if` is a statement, not a value)", true
	case t.Kind == lexer.LBrace && strings.Contains(firstMsg, "got {") && isExprStart(prev):
		return "a map literal is written with square brackets, `[\"a\": 1, \"b\": 2]`, and `[:]` is empty (ADR 0065); braces hold a block", true
	case t.Kind == lexer.KwFor && next.Kind == lexer.LParen:
		return "`for` takes a name and a list, with no parentheses: `for x in xs { ... }` (ADR 0053)", true
	case t.Kind == lexer.Ident && isIdentHint(t.Text, lineStart):
		return identHint(t.Text), true
	}
	return "", false
}

// hasBefore reports whether a token of kind k precedes w[j] on its line.
func hasBefore(w []lexer.Token, j int, k lexer.Kind) bool {
	for i := j - 1; i >= 0 && w[i].Start.Line == w[j].Start.Line; i-- {
		if w[i].Kind == k {
			return true
		}
	}
	return false
}

// isExprStart reports whether a token before a `{` puts an expression there.
func isExprStart(prev lexer.Token) bool {
	switch prev.Kind {
	case lexer.Equals, lexer.KwReturn, lexer.LParen, lexer.Comma, lexer.LBracket, lexer.Colon:
		return true
	}
	return false
}

// A word from another language that is a plain identifier here. A few are
// only a hint at the start of a line (`let x = 1`), since elsewhere they could
// be a name.
var identHints = map[string]struct {
	atLineStart bool
	msg         string
}{
	"elif":     {false, "write `else if`; there is no `elif`"},
	"elsif":    {false, "write `else if`; there is no `elsif`"},
	"elseif":   {false, "write `else if`; there is no `elseif`"},
	"let":      {true, "a binding is `name = value`, and `name: Type = value` can be reassigned (ADR 0053); there is no `let`"},
	"var":      {true, "a binding is `name = value`, and `name: Type = value` can be reassigned (ADR 0053); there is no `var`"},
	"val":      {true, "a binding is `name = value`, and `name: Type = value` can be reassigned (ADR 0053); there is no `val`"},
	"const":    {true, "there are no constants of their own; a rule with no parameters returns one (`rule maxItems() -> int { return 3 }`), or an enum header holds it (ADR 0046 §4a)"},
	"switch":   {true, "write a `match`; there is no `switch` (ADR 0046)"},
	"import":   {true, "an import is `use acme.package`; there is no `import` (ADR 0043)"},
	"try":      {true, "an expected failure is a `Result`, and `?` passes an `Err` on; there is no `try`, `catch` or `throw`"},
	"catch":    {true, "an expected failure is a `Result`, and `?` passes an `Err` on; there is no `try`, `catch` or `throw`"},
	"throw":    {true, "an expected failure is a `Result`, and `?` passes an `Err` on; there is no `try`, `catch` or `throw`"},
	"function": {true, "a declaration starts with `rule`, `conversion`, `activity`, `gateway` or `workflow`; there is no `function`"},
	"func":     {true, "a declaration starts with `rule`, `conversion`, `activity`, `gateway` or `workflow`; there is no `func`"},
	"fn":       {true, "a declaration starts with `rule`, `conversion`, `activity`, `gateway` or `workflow`; there is no `fn`"},
	"def":      {true, "a declaration starts with `rule`, `conversion`, `activity`, `gateway` or `workflow`; there is no `def`"},
	"class":    {true, "data is a `model`; there are no classes (a model has only fields, and behavior is in rules and activities)"},
	"struct":   {true, "data is a `model`; there is no `struct`"},
}

func isIdentHint(name string, lineStart bool) bool {
	h, ok := identHints[name]
	return ok && (lineStart || !h.atLineStart)
}

// withHint is errs with a cascade replaced by one hint, when the first error
// is on a line that holds a known construct.
func withHint(toks []lexer.Token, errs []Error) []Error {
	h, ok := hintFor(toks, errs)
	if !ok {
		return errs
	}
	var out []Error
	for _, e := range errs {
		if e.At.Offset < h.At.Offset {
			out = append(out, e)
		}
	}
	return append(out, h)
}

func identHint(name string) string { return identHints[name].msg }
