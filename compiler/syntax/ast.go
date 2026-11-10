// Package syntax defines the fiig abstract syntax tree, per
// docs/specification/grammar.md. Every node carries a Span so tooling
// (diagnostics, the formatter) can point at exact source locations —
// Milestone 1's exit criteria requires this.
package syntax

import (
	"fiig-lang/compiler/decimal"
	"fiig-lang/compiler/lexer"
)

type Span struct {
	Start lexer.Pos
	End   lexer.Pos
}

// Bounds is promoted onto every node that embeds Span (every node in this
// file does), so tooling that needs a node's source position — the
// formatter, in particular, for deciding where a comment attaches — can ask
// for it through the Spanned interface without a type switch over every
// concrete node type.
func (s Span) Bounds() (lexer.Pos, lexer.Pos) { return s.Start, s.End }

// Spanned is satisfied by every node in this file via Span's promoted
// Bounds method.
type Spanned interface {
	Bounds() (lexer.Pos, lexer.Pos)
}

// File is one source file: its imports, its one public declaration, and
// that declaration's optional `private { ... }` block (ADR 0054). A second
// public declaration is a parse error, but the parser still keeps it in
// Decls so the rest of the file is checked (the invalid-programs fixtures
// pack many declarations into one file on purpose); Decls[0] is the public
// entry.
type File struct {
	Span
	Imports []*Import
	Decls   []Decl
	Private *PrivateBlock // nil when the file has no private block
}

// PrivateBlock is ADR 0054's `private { ... }`: members visible only inside
// their own file. Members holds *HelperDecl and *ModelDecl in source order.
type PrivateBlock struct {
	Span
	Members []Decl
}

// HelperDecl is a private helper function (ADR 0054 §1). It has no kind
// keyword of its own: it takes the composition rules and effect ceiling of
// its file's public declaration.
type HelperDecl struct {
	Span
	Name       string
	Params     []Param
	ReturnType Type
	Body       *Block
}

type Import struct {
	Span
	Path  []string // dotted package path segments
	Alias string   // "" if no `as alias`
}

// ---- Declarations ----------------------------------------------------

type Decl interface{ declNode() }

type Field struct {
	Span
	Name string
	Type Type
	// Embedded marks a model field written as a bare type name with no
	// field name (ADR 0045). Name then holds the embedded type's name, which
	// is also the key construction uses (`Customer(ContactInfo: c, ...)`).
	Embedded bool
	// JSON is the member name a `from json "name"` clause gives a model or
	// payload field (ADR 0073 §7b); "" when it has none. Use JSONName.
	JSON string
}

// JSONName is the member the field reads and writes in JSON: its
// `from json` name, or its own name.
func (f Field) JSONName() string {
	if f.JSON != "" {
		return f.JSON
	}
	return f.Name
}

// FlatField is one field of a model's flattened field set (ADR 0045): an
// own field, or one promoted from an embedded model. Via is the chain of
// embedded model names it was promoted through, outermost first; empty for
// an own field.
type FlatField struct {
	Span
	Name string
	Type Type
	Via  []string
	JSON string
}

type Param struct {
	Span
	Name string
	Type Type
}

type ModelDecl struct {
	Span
	Name   string
	Fields []Field
	// Flat is the flattened field set (own plus promoted, ADR 0045),
	// populated by types.BuildUniverse; nil until then. Use AllFields.
	Flat []FlatField
}

// AllFields is every field a value of this model has, flattened through
// any embedded models: this is what field access, copy, JSON, and the
// registry see. Fields, by contrast, is the declaration as written, with
// embedded models still unexpanded — what construction takes.
func (m *ModelDecl) AllFields() []Field {
	if m.Flat == nil {
		var own []Field
		for _, f := range m.Fields {
			if !f.Embedded {
				own = append(own, f)
			}
		}
		return own
	}
	out := make([]Field, len(m.Flat))
	for i, f := range m.Flat {
		out[i] = Field{Span: f.Span, Name: f.Name, Type: f.Type, JSON: f.JSON}
	}
	return out
}

type EnumVariant struct {
	Span
	Name   string
	Fields []Field
	// Values is the row a variant of a headed enum supplies, one named
	// constant per header field (ADR 0046 §4). Fields and Values are never
	// both set: a headed enum's variants carry no payload (§6).
	Values []Arg
}

// Value returns the constant this variant supplies for header field name.
func (v EnumVariant) Value(name string) (Expr, bool) {
	for _, a := range v.Values {
		if a.Name == name {
			return a.Value, true
		}
	}
	return nil, false
}

// EnumField is one column of an enum's header (ADR 0046 §4): a constant
// every variant supplies. Key marks a field with a derived lookup (§5).
type EnumField struct {
	Span
	Name string
	Key  bool
	Type Type
}

type EnumDecl struct {
	Span
	Name     string
	Header   []EnumField
	Variants []EnumVariant
}

// Variant returns the variant named name, if the enum has one.
func (d *EnumDecl) Variant(name string) (EnumVariant, bool) {
	for _, v := range d.Variants {
		if v.Name == name {
			return v, true
		}
	}
	return EnumVariant{}, false
}

// HasPayload reports whether any variant carries a payload, which decides
// the enum's JSON shape (ADR 0072): an object naming its variant if so, a
// string if not.
func (d *EnumDecl) HasPayload() bool {
	for _, v := range d.Variants {
		if len(v.Fields) > 0 {
			return true
		}
	}
	return false
}

// HeaderField returns the header field named name, if the enum has one.
func (d *EnumDecl) HeaderField(name string) (EnumField, bool) {
	for _, f := range d.Header {
		if f.Name == name {
			return f, true
		}
	}
	return EnumField{}, false
}

type RuleDecl struct {
	Span
	Name       string
	Params     []Param
	ReturnType Type
	Body       *Block
}

type ConversionDecl struct {
	Span
	Name       string
	Params     []Param
	ReturnType Type
	Body       *Block
}

// GatewayDecl is `gateway` (ADR 0031, renamed from `step`) — the one
// declaration kind that always touches exactly one capability (ADR 0010).
type GatewayDecl struct {
	Span
	Name       string
	Params     []Param
	ReturnType Type
	Requires   string
	Idempotent bool // ADR 0014
	Body       *Block
}

// ActivityDecl is `activity` (ADR 0030, widened by ADR 0044) — the reusable
// coordinator: it may call rules, conversions, gateways, and other
// activities, and may hold `transaction` blocks, but is never
// adapter-exposed. It has no `requires` clause: only a gateway names the
// capability it consumes (ADR 0010).
type ActivityDecl struct {
	Span
	Name       string
	Params     []Param
	ReturnType Type
	Body       *Block
}

// InterfaceDecl is `interface` (ADR 0043 §1): a list of gateway signatures
// a capability group fulfills. Its name is usable as a type, and a value of
// it is a capability group chosen at runtime (a provider value, §3).
type InterfaceDecl struct {
	Span
	Name    string
	Members []InterfaceMember
}

// InterfaceMember is one signature in an interface: a gateway's shape with
// no `requires` and no body.
type InterfaceMember struct {
	Span
	Name       string
	Params     []Param
	ReturnType Type
}

// CapabilityDecl is `capability` (ADR 0043 §5): a named group of gateways
// that other gateways reach through `requires Name` and that callers address
// as `Name.member(...)`. A group is the only way a gateway is reachable from
// outside its own package.
type CapabilityDecl struct {
	Span
	Name     string
	Gateways []*GatewayDecl
	// Provides is every interface this group fulfills, from its package
	// manifest's `provides` map (ADR 0043 §2), populated by
	// types.BindProvides once verified; nil until then. It is what makes the
	// group assignable to those interfaces.
	Provides []*InterfaceDecl
}

// Gateway returns the group's member named name, or nil.
func (c *CapabilityDecl) Gateway(name string) *GatewayDecl {
	for _, g := range c.Gateways {
		if g.Name == name {
			return g
		}
	}
	return nil
}

// FormulasDecl is `formulas name { formula ... }` (ADR 0049 §2.1): a group
// of formulas, which §3.9 counts as its file's one declaration. Callers
// outside the group address a member as `name.member(...)`, and only after
// importing the group by path tail, even from the same package (§2.5).
type FormulasDecl struct {
	Span
	Name    string
	Members []*FormulaDecl
}

// Member returns the group's formula named name, or nil.
func (f *FormulasDecl) Member(name string) *FormulaDecl {
	for _, m := range f.Members {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// FormulaDecl is one `formula` in a group (ADR 0049 §2.1): pure
// computation, callable from every kind, calling only formulas. Body is nil
// for a native leaf, whose body the compiler supplies (§2.4). Requires is
// never legal (K4); the parser keeps it so the checker can say why.
type FormulaDecl struct {
	Span
	Name       string
	Params     []Param
	ReturnType Type
	Requires   string
	Body       *Block
}

// ConfigDecl is `config` (ADR 0071 §1): values the deployment supplies
// through the environment, each read and checked once, before the program
// starts. Callers read a field as `Name.field`, from an activity or a
// gateway only (§2).
type ConfigDecl struct {
	Span
	Name   string
	Fields []ConfigField
}

// ConfigField is one `name: Type from env "NAME" = default` line. Default
// is nil when the field has none; the checker holds it to a constant.
type ConfigField struct {
	Span
	Name    string
	Type    Type
	Env     string // the environment variable's name
	Default Expr
}

// Field returns the field named name, if the config has one.
func (d *ConfigDecl) Field(name string) (ConfigField, bool) {
	for _, f := range d.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return ConfigField{}, false
}

type WorkflowDecl struct {
	Span
	Name       string
	Params     []Param
	ReturnType Type
	Body       *Block
}

// AdapterKind is "http" or "cli" (grammar.md `adapter-kind`).
type AdapterKind string

const (
	AdapterHTTP AdapterKind = "http"
	AdapterCLI  AdapterKind = "cli"
)

type AdapterDecl struct {
	Span
	Kind   AdapterKind
	Name   string
	Routes []*Route
}

// Route covers both http-route and cli-route (grammar.md): Verb is an HTTP
// method ("GET") for an http adapter, or "command" for a cli adapter — the
// two grammar productions differ only in that one keyword, so one struct
// covers both rather than duplicating it.
type Route struct {
	Span
	Verb     string
	Path     string // the route's string-literal content
	Workflow *Ident // the workflow the route calls
	Args     []RouteArg
	Consumes *MediaType    // nil if the route has no `consumes` clause (ADR 0069 §5)
	Produces *MediaType    // nil if the route has no `produces` clause
	Mapping  []ResponseArm // nil if the route has no response-mapping block (ADR 0017)
}

// RouteArg is one argument of a route's call: the workflow parameter it
// binds, that parameter's type, and, for an argument read from a header or
// cookie (ADR 0069 §1), where it comes from. A route argument holds a type,
// not a value, so it is not an ordinary call argument.
type RouteArg struct {
	Span
	Name       string
	Type       Type
	Source     string // "", "header", or "cookie"
	SourceName string // the header or cookie name as it appears on the wire
}

// MediaType is a route's `consumes` or `produces` type, such as
// "application/json" (ADR 0069 §5).
type MediaType struct {
	Span
	Text string
}

// ResponseArm is one arm of a route's response-mapping block.
type ResponseArm struct {
	Span
	Pattern Pattern
	Value   ResponseValue
}

type ResponseValue interface{ responseValueNode() }

// StatusResponse is HTTP-only: `200 customer` or bare `404`.
type StatusResponse struct {
	Span
	Code Expr
	Body Expr // nil if no body expression was given
}

// ExitResponse is CLI-only: `exit 0 customer`.
type ExitResponse struct {
	Span
	Code Expr
	Body Expr // nil if no body expression was given
}

// ExprResponse is an ordinary call producing an already-built response,
// e.g. a shared `conversion` (ADR 0017's whole point: no dedicated reuse
// syntax, just an ordinary expression here).
type ExprResponse struct {
	Span
	Value Expr
}

func (*ModelDecl) declNode()      {}
func (*EnumDecl) declNode()       {}
func (*RuleDecl) declNode()       {}
func (*ConversionDecl) declNode() {}
func (*GatewayDecl) declNode()    {}
func (*ActivityDecl) declNode()   {}
func (*WorkflowDecl) declNode()   {}
func (*AdapterDecl) declNode()    {}
func (*HelperDecl) declNode()     {}
func (*InterfaceDecl) declNode()  {}
func (*CapabilityDecl) declNode() {}
func (*FormulasDecl) declNode()   {}
func (*FormulaDecl) declNode()    {}
func (*ConfigDecl) declNode()     {}

// DeclName returns a declaration's name, generically over every kind.
func DeclName(d Decl) string {
	switch d := d.(type) {
	case *ModelDecl:
		return d.Name
	case *EnumDecl:
		return d.Name
	case *RuleDecl:
		return d.Name
	case *ConversionDecl:
		return d.Name
	case *ActivityDecl:
		return d.Name
	case *GatewayDecl:
		return d.Name
	case *WorkflowDecl:
		return d.Name
	case *AdapterDecl:
		return d.Name
	case *HelperDecl:
		return d.Name
	case *InterfaceDecl:
		return d.Name
	case *CapabilityDecl:
		return d.Name
	case *FormulasDecl:
		return d.Name
	case *FormulaDecl:
		return d.Name
	case *ConfigDecl:
		return d.Name
	default:
		return ""
	}
}

// DeclKindName returns a declaration's kind as the keyword that declares
// it, for diagnostics that have to say which of two same-named
// declarations they mean. Covers every kind, unlike
// compiler/checker's declKind, which only classifies the five that have
// bodies for the §3.2 composition matrix.
func DeclKindName(d Decl) string {
	switch d.(type) {
	case *ModelDecl:
		return "model"
	case *EnumDecl:
		return "enum"
	case *RuleDecl:
		return "rule"
	case *ConversionDecl:
		return "conversion"
	case *ActivityDecl:
		return "activity"
	case *GatewayDecl:
		return "gateway"
	case *WorkflowDecl:
		return "workflow"
	case *AdapterDecl:
		return "adapter"
	case *HelperDecl:
		return "private helper"
	case *InterfaceDecl:
		return "interface"
	case *CapabilityDecl:
		return "capability"
	case *FormulasDecl:
		return "formulas group"
	case *FormulaDecl:
		return "formula"
	case *ConfigDecl:
		return "config"
	default:
		return "declaration"
	}
}

func (*StatusResponse) responseValueNode() {}
func (*ExitResponse) responseValueNode()   {}
func (*ExprResponse) responseValueNode()   {}

// ---- Types -------------------------------------------------------------

type Type interface{ typeNode() }

// QualifiedType is a possibly-dotted name with optional generic args:
// `CustomerId`, `systemTime.Instant`, `List<CustomerId>`,
// `Result<Customer, CustomerError>`.
type QualifiedType struct {
	Span
	Path []string
	Args []Type
}

// RecordType is an anonymous structural type: `{ id: OrderId, total: Money }`.
// Legal to parse anywhere a type appears; only legal by the checker as a
// local variable's type, never in a public signature (bug class A8).
type RecordType struct {
	Span
	Fields []Field
}

// UnionType is `A | B | ...`; most commonly `T | None` (§3.4/§3.5).
type UnionType struct {
	Span
	Members []Type
}

func (*QualifiedType) typeNode() {}
func (*RecordType) typeNode()    {}
func (*UnionType) typeNode()     {}

// ---- Statements ----------------------------------------------------------

type Stmt interface{ stmtNode() }

type Block struct {
	Span
	Stmts []Stmt
}

// Binding is `name = expr`, or `name: Type = expr` when Type is set (ADR 0053).
// A typed binding declares a reassignable local; an untyped one is immutable
// unless the name already refers to a reassignable local, in which case it
// is a reassignment.
type Binding struct {
	Span
	Name  string
	Type  Type // nil for an untyped binding
	Value Expr
}

type ElseIf struct {
	Cond Expr
	Then *Block
}

type IfStmt struct {
	Span
	Cond    Expr
	Then    *Block
	ElseIfs []ElseIf // ADR 0013
	Else    *Block   // nil if no trailing else
}

type ReturnStmt struct {
	Span
	Value Expr // nil for a bare `return`
}

type MatchArm struct {
	Span
	Pattern Pattern
	Body    Body // Expr or *Block
}

// MatchStmt is a statement, not an expression (grammar.md §5's
// match-statement is not part of the expression grammar) — but when it's
// the last statement in its block, each arm's own body still produces the
// enclosing declaration's implicit return value, recursively, the same
// rule ExprStmt gets when *it's* the last statement (grammar.md §5). This
// is how most gateways in the corpus actually return their result.
type MatchStmt struct {
	Span
	Subject Expr
	Arms    []MatchArm
}

// ForStmt is `[label:] for x in xs { ... }` (ADR 0053). Label is empty when
// the loop is unlabeled.
type ForStmt struct {
	Span
	Label string
	Var   string
	// Value is the second name of `for key, value in m` (ADR 0065 §7), ""
	// for a one-name loop.
	Value    string
	Iterable Expr
	Body     *Block
}

// WhileStmt is `[label:] while cond { ... }` (ADR 0059). Cond is evaluated
// before every iteration, including the first.
type WhileStmt struct {
	Span
	Label string
	Cond  Expr
	Body  *Block
}

// BreakStmt is `break [label]` (ADR 0053). Label is empty for the innermost loop.
type BreakStmt struct {
	Span
	Label string
}

// ContinueStmt is `continue [label]` (ADR 0053).
type ContinueStmt struct {
	Span
	Label string
}

// TransactionStmt is `transaction Capability { ... }` (§3.8).
type TransactionStmt struct {
	Span
	Capability string
	Body       *Block
}

// ExprStmt is an expression used for its side effect (a bare call, or a
// `concurrent`/`retry` expression whose result isn't bound) — see F1's
// point: an ExprStmt result that's a Result and goes unhandled is a checker
// error (error_handling_error.fiig), not a parse error. Exception: when an
// ExprStmt is the last statement in its block, its value is the enclosing
// declaration's implicit return value (grammar.md §5), so the checker must
// not flag that one under F1.
type ExprStmt struct {
	Span
	Value Expr
}

func (*Binding) stmtNode()         {}
func (*IfStmt) stmtNode()          {}
func (*ReturnStmt) stmtNode()      {}
func (*MatchStmt) stmtNode()       {}
func (*TransactionStmt) stmtNode() {}
func (*ForStmt) stmtNode()         {}
func (*WhileStmt) stmtNode()       {}
func (*BreakStmt) stmtNode()       {}
func (*ContinueStmt) stmtNode()    {}
func (*ExprStmt) stmtNode()        {}

// Body is the `( expression | block )` alternative used by if/match-arm/
// concurrent/retry bodies.
type Body interface{ bodyNode() }

type ExprBody struct{ Expr Expr }

func (ExprBody) bodyNode() {}
func (*Block) bodyNode()   {}

// ---- Patterns --------------------------------------------------------

type Pattern interface{ patternNode() }

type WildcardPattern struct{ Span }

// NamePattern is a bare qualified name with no call parens — `found`,
// `None`. Whether it's a fresh binding or a reference to a known zero-arg
// constructor (like `None`) is a semantic question the checker resolves,
// not the parser (grammar.md §5 "Match").
type NamePattern struct {
	Span
	Path []string
}

// ConstructorPattern is a qualified name with parens:
// `Ok(customer)`, `Err(_)`, `OrderError.InventoryUnavailable(_)`.
type ConstructorPattern struct {
	Span
	Path []string
	Args []Pattern
}

func (*WildcardPattern) patternNode()    {}
func (*NamePattern) patternNode()        {}
func (*ConstructorPattern) patternNode() {}
func (*BytesPattern) patternNode()       {}

// ---- Expressions -------------------------------------------------------

type Expr interface{ exprNode() }

type Ident struct {
	Span
	Name string
}

type IntLit struct {
	Span
	ConstSlot
	Text string // raw digits
}

// FloatLit is a number literal with a fraction or an exponent (ADR 0048
// §2.4). Like IntLit it is an untyped constant, and context gives it a type.
type FloatLit struct {
	Span
	ConstSlot
	Text string // as written: `3.14`, `1e-9`, `6.02E23`
}

// Const is the value of a constant expression once the checker has given it
// a type (ADR 0048 §2.4). A constant is evaluated exactly and rounded once,
// where it lands, so the runtime reads this instead of evaluating the
// expression's parts, which would round at every step and type `7 / 2` in a
// float context as 3.5 rather than Go's 3.
type Const struct {
	Float bool // the type is float
	Int   int64
	F     float64
	// Dec is the value when the type is decimal (ADR 0064), kept with the
	// scale the constant was written with; nil otherwise.
	Dec *decimal.Decimal
}

// ConstSlot holds the Const the checker gives the root of a constant
// expression. It is embedded in every node a constant expression can be
// rooted at, and stays nil on a node the checker never typed.
type ConstSlot struct{ c *Const }

// Constant returns the checker's value for this node, or nil.
func (s *ConstSlot) Constant() *Const { return s.c }

// SetConstant records the checker's value for this node.
func (s *ConstSlot) SetConstant(c *Const) { s.c = c }

// ConstRoot is a node that can root a constant expression.
type ConstRoot interface {
	Constant() *Const
	SetConstant(*Const)
}

// StringPart is one piece of an interpolated string literal: either literal
// text, or an interpolated expression.
type StringPart struct {
	Text   string // valid only when Interp == nil
	Interp Expr   // non-nil for an `${...}` part
}

type StringLit struct {
	Span
	Parts []StringPart
}

type FieldAccess struct {
	Span
	ConstSlot // `math.pi`, a constant member of the prelude math group (ADR 0049 §2.7)
	Target    Expr
	Field     string
}

type Arg struct {
	Name  string // "" for a positional argument
	Value Expr
}

type CallExpr struct {
	Span
	Callee Expr
	Args   []Arg
	// DecodeInto is the type a json.decode call decodes into, a
	// types.Type the checker takes from the call's context and records here
	// for the runtime (ADR 0073 §2); nil on every other call, and before
	// checking.
	DecodeInto any
}

// NotExpr is the prefix `not` (grammar.md §5 `primary`).
type NotExpr struct {
	Span
	Operand Expr
}

// NegExpr is the prefix unary `-` (ADR 0019) — kept as its own node rather
// than a BinaryExpr with an implicit left operand, mirroring NotExpr.
type NegExpr struct {
	Span
	ConstSlot
	Operand Expr
}

type BinaryOp string

const (
	OpEq  BinaryOp = "=="
	OpNe  BinaryOp = "!="
	OpLt  BinaryOp = "<"
	OpLe  BinaryOp = "<="
	OpGt  BinaryOp = ">"
	OpGe  BinaryOp = ">="
	OpAnd BinaryOp = "and"
	OpOr  BinaryOp = "or"
	// Arithmetic (ADR 0019) — int-only, fixed meaning, never overloadable.
	OpAdd BinaryOp = "+"
	OpSub BinaryOp = "-"
	OpMul BinaryOp = "*"
	OpDiv BinaryOp = "/"
	OpMod BinaryOp = "%"
)

type BinaryExpr struct {
	Span
	ConstSlot
	Op    BinaryOp
	Left  Expr
	Right Expr
}

// TryExpr is the postfix `?` (error propagation).
type TryExpr struct {
	Span
	Operand Expr
}

type ParenExpr struct {
	Span
	ConstSlot
	Inner Expr
}

// SegmentKind is what a binary segment's encoding decodes to (ADR 0058).
type SegmentKind int

const (
	SegInt   SegmentKind = iota // u8, i32be, u31, ...: an int
	SegBytes                    // bytes or bytes(n)
	SegUTF8                     // utf8 or utf8(n): a string
)

// Encoding is a segment's `: encoding` part. For SegInt, Bits is the width,
// Signed and Little the format; Name is the encoding as written, for
// printing. For SegBytes and SegUTF8, Size is the `(n)` expression, nil when
// absent (the rest of the input in a pattern, the value's own length in an
// expression).
type Encoding struct {
	Span
	Name   string
	Kind   SegmentKind
	Bits   int
	Signed bool
	Little bool
	Size   Expr
}

// Segment is one `x: encoding` element of a binary pattern or expression.
// In a pattern, Name is the bound name ("_" to skip) or Literal is the
// value to match; in an expression, Value is the value to encode.
type Segment struct {
	Span
	Name     string
	Literal  Expr
	Value    Expr
	Encoding Encoding
}

// BytesLit is a binary expression, `<<value: encoding, ...>>`, building a
// new bytes value (ADR 0058 §4).
type BytesLit struct {
	Span
	Segments []Segment
}

// BytesPattern is a binary pattern, `<<name: encoding, ...>>` (ADR 0058 §3).
type BytesPattern struct {
	Span
	Segments []Segment
}

// ListLit is `[a, b, c]` (ADR 0057). Its element type is inferred from the
// elements, or taken from the expected type; `[]` needs one.
type ListLit struct {
	Span
	Elems []Expr
}

// LimitValue is a `concurrent` block's optional `limit:` clause.
type LimitValue struct {
	Span
	Unbounded bool // `limit: unbounded` — legal to parse, checker-rejected (D1)
	Value     Expr // nil when Unbounded
}

type ConcurrentExpr struct {
	Span
	Var           string
	Iterable      Expr
	Limit         *LimitValue // nil for the bare form
	PreserveOrder bool        // `preserveOrder` modifier (fiig-lang.md §3.7, terminology.md) — results in input order instead of completion order
	Body          Body
}

// MapLit is `["a": 1, "b": 2]`, or `[:]` with no entries (ADR 0065 §5).
type MapLit struct {
	Span
	Entries []MapEntry
}

// MapEntry is one `key: value` of a map literal.
type MapEntry struct {
	Key, Value Expr
}

// SortedExpr is ADR 0065 §3's keyed sort, `sorted(x in xs): key` or
// `sorted(x in xs, descending): key`: a new list of xs's elements, stably
// ordered by Key, which is evaluated once per element with Var bound to it.
type SortedExpr struct {
	Span
	Var        string
	Iterable   Expr
	Descending bool
	Key        Expr
}

type RetryExpr struct {
	Span
	Attempts Expr
	Body     Body
}

func (*Ident) exprNode()          {}
func (*IntLit) exprNode()         {}
func (*FloatLit) exprNode()       {}
func (*StringLit) exprNode()      {}
func (*FieldAccess) exprNode()    {}
func (*CallExpr) exprNode()       {}
func (*NotExpr) exprNode()        {}
func (*NegExpr) exprNode()        {}
func (*BinaryExpr) exprNode()     {}
func (*TryExpr) exprNode()        {}
func (*ParenExpr) exprNode()      {}
func (*ListLit) exprNode()        {}
func (*BytesLit) exprNode()       {}
func (*ConcurrentExpr) exprNode() {}
func (*RetryExpr) exprNode()      {}
func (*SortedExpr) exprNode()     {}
func (*MapLit) exprNode()         {}

// AdmitsNone reports whether a written type is None or a union with None
// among its members, so a value of it may be left out of a construction.
func AdmitsNone(t Type) bool {
	switch t := t.(type) {
	case *QualifiedType:
		return len(t.Path) == 1 && t.Path[0] == "None" && len(t.Args) == 0
	case *UnionType:
		for _, m := range t.Members {
			if AdmitsNone(m) {
				return true
			}
		}
	}
	return false
}
