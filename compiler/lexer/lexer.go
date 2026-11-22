package lexer

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ctxKind tracks what the lexer is currently scanning, so that string
// interpolation (`"...${expr}..."`) can nest arbitrarily: an interpolated
// expression can itself contain a string literal, which can itself
// interpolate, and so on.
type ctxKind int

const (
	ctxTop    ctxKind = iota // ordinary top-level/program scanning
	ctxString                // inside a string literal, between StringStart and StringEnd
	ctxInterp                // inside `${ ... }`, scanning ordinary tokens
)

type context struct {
	kind       ctxKind
	braceDepth int // ctxInterp only: nested '{'/'}' seen since the interpolation opened
}

// Lexer scans fiig source text into a flat Token stream per
// docs/specification/grammar.md §1.
type Lexer struct {
	src      string
	pos      int // byte offset of the next unread byte
	line     int
	col      int
	stack    []context
	errors   []Error
	comments []Comment
	// names holds each identifier once, so every use of a name shares one
	// string, and the interpreter's name checks compare pointers (ADR 0115
	// §3).
	names map[string]string
}

// Error is a lexical error with the source span it occurred at.
type Error struct {
	Msg string
	At  Pos
}

// Comment is one `//` line comment. Comments are not part of the grammar
// (grammar.md §1) and never appear in the Token stream Next/AllTokens
// produce — the parser never sees them — but tooling that needs them back,
// like the formatter, can recover them from Comments() in source order.
type Comment struct {
	Text  string // includes the leading "//"
	Start Pos
	End   Pos
}

func New(src string) *Lexer {
	return &Lexer{
		src:   src,
		pos:   0,
		line:  1,
		col:   1,
		stack: []context{{kind: ctxTop}},
	}
}

// Errors returns every lexical error encountered so far.
func (l *Lexer) Errors() []Error {
	return l.errors
}

// Comments returns every line comment encountered so far, in source order.
// Call after draining the lexer (e.g. via AllTokens) for the complete list.
func (l *Lexer) Comments() []Comment {
	return l.comments
}

func (l *Lexer) errorf(at Pos, format string, args ...any) {
	l.errors = append(l.errors, Error{Msg: fmt.Sprintf(format, args...), At: at})
}

func (l *Lexer) here() Pos {
	return Pos{Line: l.line, Column: l.col, Offset: l.pos}
}

func (l *Lexer) atEOF() bool {
	return l.pos >= len(l.src)
}

func (l *Lexer) peekByte() byte {
	if l.atEOF() {
		return 0
	}
	return l.src[l.pos]
}

func (l *Lexer) peekByteAt(offset int) byte {
	if l.pos+offset >= len(l.src) {
		return 0
	}
	return l.src[l.pos+offset]
}

// advance consumes and returns the next rune, updating line/column.
func (l *Lexer) advance() rune {
	if l.atEOF() {
		return 0
	}
	r, size := utf8.DecodeRuneInString(l.src[l.pos:])
	l.pos += size
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

// isLetter matches grammar.md §1's `letter` — deliberately excludes '_',
// since identifier requires a *leading* letter and '_' is its own token
// (Underscore), used by the wildcard pattern (grammar.md §5 `pattern`).
func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

// isIdentContinue matches identifier's repetition part, `(letter | digit | '_')`,
// which does allow '_' after the leading letter.
func isIdentContinue(r rune) bool {
	return isLetter(r) || isDigit(r) || r == '_'
}

// Next returns the next token in the stream. Once it returns a Kind == EOF
// token, every subsequent call keeps returning EOF.
func (l *Lexer) Next() Token {
	top := l.stack[len(l.stack)-1]
	if top.kind == ctxString {
		return l.scanStringPart()
	}
	return l.scanToken()
}

// scanToken handles ctxTop and ctxInterp, which share ordinary-token
// scanning; ctxInterp only differs in how it tracks brace depth to find the
// interpolation's closing '}'.
func (l *Lexer) scanToken() Token {
	l.skipWhitespaceAndComments()
	start := l.here()

	if l.atEOF() {
		return Token{Kind: EOF, Start: start, End: start}
	}

	r, _ := utf8.DecodeRuneInString(l.src[l.pos:])

	switch {
	case r == '"':
		l.advance()
		l.stack = append(l.stack, context{kind: ctxString})
		return Token{Kind: StringStart, Start: start, End: l.here()}

	case isLetter(r):
		return l.scanIdentOrKeyword(start)

	case isDigit(r):
		return l.scanNumber(start)

	case r == '_':
		// Only reached when '_' is not immediately followed by a letter/digit
		// (scanIdentOrKeyword already handles '_' followed by more identifier
		// characters, since '_' satisfies isLetter above).
		l.advance()
		return Token{Kind: Underscore, Text: "_", Start: start, End: l.here()}
	}

	return l.scanPunct(start)
}

func (l *Lexer) skipWhitespaceAndComments() {
	for {
		if l.atEOF() {
			return
		}
		b := l.peekByte()
		if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
			l.advance()
			continue
		}
		if b == '/' && l.peekByteAt(1) == '/' {
			start := l.here()
			for !l.atEOF() && l.peekByte() != '\n' {
				l.advance()
			}
			end := l.here()
			// Slice the original source by byte offset rather than rebuild it
			// rune-by-rune, so multi-byte UTF-8 comment text survives intact.
			// A CRLF file's line break is not part of the comment (ADR 0061).
			text := strings.TrimSuffix(l.src[start.Offset:end.Offset], "\r")
			l.comments = append(l.comments, Comment{Text: text, Start: start, End: end})
			continue
		}
		return
	}
}

func (l *Lexer) scanIdentOrKeyword(start Pos) Token {
	var sb strings.Builder
	for !l.atEOF() {
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if !isIdentContinue(r) {
			break
		}
		sb.WriteRune(r)
		l.pos += size
		if r == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
	}
	text := sb.String()
	if kind, ok := keywords[text]; ok {
		return Token{Kind: kind, Text: text, Start: start, End: l.here()}
	}
	if name, ok := l.names[text]; ok {
		text = name
	} else {
		if l.names == nil {
			l.names = make(map[string]string)
		}
		l.names[text] = text
	}
	return Token{Kind: Ident, Text: text, Start: start, End: l.here()}
}

// scanNumber handles integer-literal (grammar.md §1: digit+, or `0x` and
// hex digits, ADR 0058) as one Int token, and float-literal (ADR 0048: a
// fraction, an exponent, or both) as one Float token, text kept as written.
// Package versions (ADR 0032) live in a manifest, not in dotted number
// tokens here — the dotted `version-literal` form this used to also handle
// for `use pkg@version` was removed along with that grammar; the dot a
// float literal carries is unrelated to it.
func (l *Lexer) scanNumber(start Pos) Token {
	var sb strings.Builder
	if l.peekByte() == '0' && (l.peekByteAt(1) == 'x' || l.peekByteAt(1) == 'X') {
		sb.WriteByte(l.peekByte())
		l.advance()
		sb.WriteByte(l.peekByte())
		l.advance()
		digits := 0
		for !l.atEOF() && isHexDigit(l.peekByte()) {
			sb.WriteByte(l.peekByte())
			l.advance()
			digits++
		}
		if digits == 0 {
			l.errorf(start, "hex literal %q has no digits", sb.String())
		}
		return Token{Kind: Int, Text: sb.String(), Start: start, End: l.here()}
	}
	l.scanDigits(&sb)
	kind := Int
	// ADR 0048 §2.4: a fraction needs a digit on both sides of the dot, so
	// `3.abs` would still be a field access, and an exponent needs a digit
	// after its optional sign.
	if l.peekByte() == '.' && isDigit(rune(l.peekByteAt(1))) {
		kind = Float
		sb.WriteByte('.')
		l.advance()
		l.scanDigits(&sb)
	}
	if b := l.peekByte(); b == 'e' || b == 'E' {
		next := l.peekByteAt(1)
		if next == '+' || next == '-' {
			next = l.peekByteAt(2)
		}
		if isDigit(rune(next)) {
			kind = Float
			sb.WriteByte(l.peekByte())
			l.advance()
			if b := l.peekByte(); b == '+' || b == '-' {
				sb.WriteByte(b)
				l.advance()
			}
			l.scanDigits(&sb)
		}
	}
	return Token{Kind: kind, Text: sb.String(), Start: start, End: l.here()}
}

func (l *Lexer) scanDigits(sb *strings.Builder) {
	for !l.atEOF() && isDigit(rune(l.peekByte())) {
		sb.WriteByte(l.peekByte())
		l.advance()
	}
}

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func (l *Lexer) scanPunct(start Pos) Token {
	top := &l.stack[len(l.stack)-1]

	two := l.src[l.pos:min(l.pos+2, len(l.src))]
	switch two {
	case "->":
		l.advance()
		l.advance()
		return Token{Kind: Arrow, Text: "->", Start: start, End: l.here()}
	case "<<":
		// ADR 0058: opens a binary pattern or expression. Its closing `>>`
		// stays two `>` tokens, so `List<List<int>>` is unaffected.
		l.advance()
		l.advance()
		return Token{Kind: LShift, Text: "<<", Start: start, End: l.here()}
	case "==":
		l.advance()
		l.advance()
		return Token{Kind: EqEq, Text: "==", Start: start, End: l.here()}
	case "!=":
		l.advance()
		l.advance()
		return Token{Kind: NotEq, Text: "!=", Start: start, End: l.here()}
	case "<=":
		l.advance()
		l.advance()
		return Token{Kind: LtEq, Text: "<=", Start: start, End: l.here()}
	case ">=":
		l.advance()
		l.advance()
		return Token{Kind: GtEq, Text: ">=", Start: start, End: l.here()}
	case "&&", "||", "??":
		// ADR 0142 §3. `??` is read before `?`, so `f()??x` is a default;
		// `f()? ?? x` propagates first.
		l.advance()
		l.advance()
		kind := QuestionQuestion
		switch two {
		case "&&":
			kind = AndAnd
		case "||":
			kind = OrOr
		}
		return Token{Kind: kind, Text: two, Start: start, End: l.here()}
	}

	r := l.advance()
	switch r {
	case '{':
		if top.kind == ctxInterp {
			top.braceDepth++
		}
		return Token{Kind: LBrace, Text: "{", Start: start, End: l.here()}
	case '}':
		if top.kind == ctxInterp {
			if top.braceDepth == 0 {
				// This '}' closes the interpolation, not a nested block.
				l.stack = l.stack[:len(l.stack)-1]
				return Token{Kind: InterpEnd, Text: "}", Start: start, End: l.here()}
			}
			top.braceDepth--
		}
		return Token{Kind: RBrace, Text: "}", Start: start, End: l.here()}
	case '(':
		return Token{Kind: LParen, Text: "(", Start: start, End: l.here()}
	case ')':
		return Token{Kind: RParen, Text: ")", Start: start, End: l.here()}
	case '[':
		return Token{Kind: LBracket, Text: "[", Start: start, End: l.here()}
	case ']':
		return Token{Kind: RBracket, Text: "]", Start: start, End: l.here()}
	case '<':
		return Token{Kind: LAngle, Text: "<", Start: start, End: l.here()}
	case '>':
		return Token{Kind: RAngle, Text: ">", Start: start, End: l.here()}
	case ',':
		return Token{Kind: Comma, Text: ",", Start: start, End: l.here()}
	case ':':
		return Token{Kind: Colon, Text: ":", Start: start, End: l.here()}
	case '.':
		return Token{Kind: Dot, Text: ".", Start: start, End: l.here()}
	case '?':
		return Token{Kind: Question, Text: "?", Start: start, End: l.here()}
	case '=':
		return Token{Kind: Equals, Text: "=", Start: start, End: l.here()}
	case '|':
		return Token{Kind: Pipe, Text: "|", Start: start, End: l.here()}
	case '*':
		return Token{Kind: Star, Text: "*", Start: start, End: l.here()}
	case '+':
		return Token{Kind: Plus, Text: "+", Start: start, End: l.here()}
	case '-':
		return Token{Kind: Minus, Text: "-", Start: start, End: l.here()}
	case '/':
		return Token{Kind: Slash, Text: "/", Start: start, End: l.here()}
	case '%':
		return Token{Kind: Percent, Text: "%", Start: start, End: l.here()}
	case ';':
		return Token{Kind: Semicolon, Text: ";", Start: start, End: l.here()}
	case '!':
		// `!=` was read above as one token.
		return Token{Kind: Bang, Text: "!", Start: start, End: l.here()}
	}

	l.errorf(start, "unexpected character %q", r)
	return Token{Kind: Illegal, Text: string(r), Start: start, End: l.here()}
}

// scanStringPart is called while ctxString is on top of the stack. It
// returns exactly one of: StringText (a run of literal text with escapes
// resolved), InterpStart (on seeing "${", after which the stack has
// ctxInterp on top), or StringEnd (on the closing '"', which pops ctxString).
func (l *Lexer) scanStringPart() Token {
	start := l.here()

	if l.atEOF() {
		l.errorf(start, "unterminated string literal")
		l.stack = l.stack[:len(l.stack)-1]
		return Token{Kind: StringEnd, Start: start, End: start}
	}

	if l.peekByte() == '"' {
		l.advance()
		l.stack = l.stack[:len(l.stack)-1]
		return Token{Kind: StringEnd, Text: "\"", Start: start, End: l.here()}
	}

	if l.peekByte() == '$' && l.peekByteAt(1) == '{' {
		l.advance()
		l.advance()
		l.stack = append(l.stack, context{kind: ctxInterp})
		return Token{Kind: InterpStart, Text: "${", Start: start, End: l.here()}
	}

	var sb strings.Builder
	for {
		if l.atEOF() {
			l.errorf(l.here(), "unterminated string literal")
			break
		}
		if l.peekByte() == '"' {
			break
		}
		if l.peekByte() == '$' && l.peekByteAt(1) == '{' {
			break
		}
		if l.peekByte() == '\\' {
			escStart := l.here()
			l.advance()
			switch l.peekByte() {
			case '"':
				sb.WriteByte('"')
				l.advance()
			case '\\':
				sb.WriteByte('\\')
				l.advance()
			case 'n':
				sb.WriteByte('\n')
				l.advance()
			case 't':
				sb.WriteByte('\t')
				l.advance()
			case 'r':
				sb.WriteByte('\r')
				l.advance()
			case '$':
				// ADR 0061: `\${` is a literal "${", not an interpolation.
				sb.WriteByte('$')
				l.advance()
			default:
				l.errorf(escStart, "unknown escape sequence '\\%c'", l.peekByte())
				if !l.atEOF() {
					l.advance()
				}
			}
			continue
		}
		if l.peekByte() == '\r' {
			// ADR 0061 §2: a line break inside a literal is LF whatever the
			// file was saved with, so CR LF reads as LF and a lone CR, whose
			// intent is ambiguous, must be spelled `\r`.
			crStart := l.here()
			l.advance()
			if l.peekByte() == '\n' {
				sb.WriteRune(l.advance())
			} else {
				l.errorf(crStart, "carriage return inside a string literal; use \\r")
			}
			continue
		}
		sb.WriteRune(l.advance())
	}

	return Token{Kind: StringText, Text: sb.String(), Start: start, End: l.here()}
}

// AllTokens drains the lexer, for callers (tests, tooling) that want the
// whole stream at once rather than pulling one token at a time.
func (l *Lexer) AllTokens() []Token {
	var toks []Token
	for {
		t := l.Next()
		toks = append(toks, t)
		if t.Kind == EOF {
			return toks
		}
	}
}
