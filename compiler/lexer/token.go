// Package lexer implements the fiig lexical grammar
// (docs/specification/grammar.md §1).
package lexer

import "fmt"

// Kind identifies the lexical category of a Token.
type Kind int

const (
	EOF Kind = iota
	Illegal

	Ident
	Int
	Float      // a fractional or exponent number literal (ADR 0048 §2.4)
	Underscore // '_', lexed separately because identifier requires a leading letter

	// String literals are emitted as a sequence of tokens rather than one
	// opaque token, so that interpolated expressions (grammar.md §1
	// `interpolation ::= '${' expression '}'`) can be lexed and later
	// parsed like any other expression:
	//
	//   "Hello, ${name}!"  ->  StringStart StringText("Hello, ") InterpStart
	//                          Ident("name") InterpEnd StringText("!") StringEnd
	StringStart
	StringText
	StringEnd
	InterpStart // '${'
	InterpEnd   // the '}' that closes an interpolation (not a block)

	// Keywords
	KwModel
	KwEnum
	KwRule
	KwConversion
	KwActivity
	KwGateway
	KwWorkflow
	KwAdapter
	KwRequires
	KwUse
	KwAs
	KwReturn
	KwIf
	KwElse
	KwNot
	KwAnd
	KwOr
	KwMatch
	KwTransaction
	KwConcurrent
	KwRetry
	KwIn
	KwLimit
	KwAttempts
	KwCommand
	KwIdempotent
	KwExit
	KwHttp
	KwCli
	KwPreserveOrder
	KwFor
	KwWhile // ADR 0059
	KwBreak
	KwContinue
	KwPrivate // ADR 0054: a kind's file-scoped `private { ... }` block
	// ADR 0043: an interface, and a capability group of gateways.
	KwInterface
	KwCapability
	// ADR 0049: a `formulas` group and its `formula` members.
	KwFormulas
	KwFormula
	// ADR 0071: a `config` declaration.
	KwConfig

	// Punctuation and operators
	LBrace   // {
	RBrace   // }
	LParen   // (
	RParen   // )
	LBracket // [ (ADR 0057: list literals)
	LShift   // << (ADR 0058: opens a binary pattern or expression)
	RBracket // ]
	LAngle   // <
	RAngle   // >
	Comma    // ,
	Colon    // :
	Dot      // .
	Question // ?
	Equals   // =
	Pipe     // |
	Star     // *
	Arrow    // ->
	EqEq     // ==
	NotEq    // !=
	LtEq     // <=
	GtEq     // >=
	Plus     // +
	Minus    // -
	Slash    // /
	Percent  // %
)

var keywords = map[string]Kind{
	"model":         KwModel,
	"enum":          KwEnum,
	"rule":          KwRule,
	"conversion":    KwConversion,
	"activity":      KwActivity,
	"gateway":       KwGateway,
	"workflow":      KwWorkflow,
	"adapter":       KwAdapter,
	"requires":      KwRequires,
	"use":           KwUse,
	"as":            KwAs,
	"return":        KwReturn,
	"if":            KwIf,
	"else":          KwElse,
	"not":           KwNot,
	"and":           KwAnd,
	"or":            KwOr,
	"match":         KwMatch,
	"transaction":   KwTransaction,
	"concurrent":    KwConcurrent,
	"retry":         KwRetry,
	"in":            KwIn,
	"limit":         KwLimit,
	"attempts":      KwAttempts,
	"command":       KwCommand,
	"idempotent":    KwIdempotent,
	"exit":          KwExit,
	"http":          KwHttp,
	"cli":           KwCli,
	"preserveOrder": KwPreserveOrder,
	"for":           KwFor,
	"while":         KwWhile,
	"break":         KwBreak,
	"continue":      KwContinue,
	"private":       KwPrivate,
	"interface":     KwInterface,
	"capability":    KwCapability,
	"formulas":      KwFormulas,
	"formula":       KwFormula,
	"config":        KwConfig,
}

// Pos is a source location, 1-indexed for both line and column so it can be
// reported to a human directly (Milestone 1 exit criteria: diagnostics
// identify the exact declaration and source span). JSON tags make it
// machine-readable too — the other half of that same exit criteria.
type Pos struct {
	Line   int `json:"line"`
	Column int `json:"column"`
	Offset int `json:"offset"` // byte offset, for span-preserving tooling that needs it
}

func (p Pos) String() string {
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// Token is one lexical token together with its source span.
type Token struct {
	Kind  Kind
	Text  string // raw source text (identifier name, digits, decoded string text, ...)
	Start Pos
	End   Pos
}

func (t Token) String() string {
	if t.Text == "" {
		return t.Kind.String()
	}
	return fmt.Sprintf("%s(%q)", t.Kind, t.Text)
}

func (k Kind) String() string {
	switch k {
	case EOF:
		return "EOF"
	case Illegal:
		return "Illegal"
	case Ident:
		return "Ident"
	case Int:
		return "Int"
	case Float:
		return "Float"
	case Underscore:
		return "Underscore"
	case StringStart:
		return "StringStart"
	case StringText:
		return "StringText"
	case StringEnd:
		return "StringEnd"
	case InterpStart:
		return "InterpStart"
	case InterpEnd:
		return "InterpEnd"
	case LBrace:
		return "{"
	case RBrace:
		return "}"
	case LParen:
		return "("
	case RParen:
		return ")"
	case LBracket:
		return "["
	case LShift:
		return "<<"
	case RBracket:
		return "]"
	case LAngle:
		return "<"
	case RAngle:
		return ">"
	case Comma:
		return ","
	case Colon:
		return ":"
	case Dot:
		return "."
	case Question:
		return "?"
	case Equals:
		return "="
	case Pipe:
		return "|"
	case Star:
		return "*"
	case Arrow:
		return "->"
	case EqEq:
		return "=="
	case NotEq:
		return "!="
	case LtEq:
		return "<="
	case GtEq:
		return ">="
	case Plus:
		return "+"
	case Minus:
		return "-"
	case Slash:
		return "/"
	case Percent:
		return "%"
	default:
		for text, kind := range keywords {
			if kind == k {
				return "Kw:" + text
			}
		}
		return "Unknown"
	}
}
