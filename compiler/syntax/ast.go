// Package syntax defines the fiig abstract syntax tree, per
// docs/specification/grammar.md. Every node carries a Span so tooling
// (diagnostics, the formatter) can point at exact source locations —
// Milestone 1's exit criteria requires this.
package syntax

import (
	"strings"

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
	// Tests marks a file under its package's tests/ directory (ADR 0091
	// §2), set by whoever reads the file from disk: it may hold a test,
	// and nothing outside tests/ may name what it declares (T1, T2).
	Tests bool
}

// IsTestPath reports whether rel, a path relative to a package's root
// with either separator, is under its tests/ directory (ADR 0091 §2).
func IsTestPath(rel string) bool {
	rel = strings.ReplaceAll(rel, "\\", "/")
	// tests/ in the old layout; test/ in a module (ADR 0102).
	return strings.HasPrefix(rel, "tests/") || strings.HasPrefix(rel, "test/")
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
	// Base64URL marks a field written `from json base64url` (ADR 0078 §6):
	// the `bytes` it holds are written as unpadded base64url instead of
	// standard base64. Reading accepts either alphabet regardless.
	Base64URL bool
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
	// Base64URL is the field's `from json base64url` (ADR 0078 §6).
	Base64URL bool
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
		out[i] = Field{Span: f.Span, Name: f.Name, Type: f.Type, JSON: f.JSON, Base64URL: f.Base64URL}
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
	// JSON is the spelling a `from json "text"` clause gives the variant
	// (ADR 0080 §1); "" when it has none. Use Text.
	JSON string
}

// Text is the variant's text form (ADR 0080 §2): its `from json`
// spelling, or its own name. JSON, route arguments read from text, and
// config all read and write it.
func (v EnumVariant) Text() string {
	if v.JSON != "" {
		return v.JSON
	}
	return v.Name
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

// VariantByText returns the variant whose text form (ADR 0080 §2) is
// text: its `from json` spelling, or its name when it declares none.
func (d *EnumDecl) VariantByText(text string) (EnumVariant, bool) {
	for _, v := range d.Variants {
		if v.Text() == text {
			return v, true
		}
	}
	return EnumVariant{}, false
}

// Texts is every variant's text form, in declared order, for a message
// or a schema listing what may be sent.
func (d *EnumDecl) Texts() []string {
	out := make([]string, len(d.Variants))
	for i, v := range d.Variants {
		out[i] = v.Text()
	}
	return out
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
	// RequiresStart is where the capability's name is written, for Go to
	// Declaration on a `requires` clause.
	RequiresStart lexer.Pos
	Idempotent    bool // ADR 0014
	Body          *Block
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
	// Declares is the interface the source says this group provides,
	// `capability X provides alias.Interface` (ADR 0105 §3), as written;
	// nil when it says none.
	Declares []string
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
//
// When, if set, is ADR 0122's condition: the config is present only when it
// holds, and absent as a whole otherwise. The loader reads it at startup from
// other config values, so a deployment that doesn't use a feature needn't
// set the feature's variables.
type ConfigDecl struct {
	Span
	Name   string
	When   Expr
	Fields []ConfigField
}

// ConfigField is one `name: Type from env "NAME" = default` line. Default
// is nil when the field has none; the checker holds it to a constant.
type ConfigField struct {
	Span
	Name string
	Type Type
	Env  string // the environment variable's name
	// Startup, instead of Env, names a startup route as `Adapter.workflow`:
	// the field is true when that route ended degraded (ADR 0127).
	Startup string
	// Setting names a setting saved in the app (ADR 0140): `from env "X" or
	// setting "x"`. Env may be "" when the field reads only the setting.
	// SettingFirst is the order written, `setting "x" or env "X"`, and
	// OnRestart makes it a hard setting, read once at start (§1a).
	Setting      string
	SettingFirst bool
	OnRestart    bool
	Default      Expr
}

// SoftSetting reports whether the field reads a setting live (ADR 0140
// §1a): one without `on restart`.
func (f ConfigField) SoftSetting() bool { return f.Setting != "" && !f.OnRestart }

// Field returns the field named name, if the config has one.
func (d *ConfigDecl) Field(name string) (ConfigField, bool) {
	for _, f := range d.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return ConfigField{}, false
}

// TestDecl is `test name { fake ... statements }` (ADR 0091 §1): an entry
// point nothing calls, living under tests/ (§2). Fakes are the `fake`
// blocks at the start of its body, kept apart from the statements after
// them.
type TestDecl struct {
	Span
	Name  string
	Fakes []*FakeDecl
	Body  *Block
}

// FakeDecl is `fake Target { ... }` in a test (ADR 0091 §4): the
// operations of a capability or capability group, written as gateways
// (Gateways), or the values of a config, Clock, or Random (Values, one
// `name: expr` each). Target is the name as written, which may be
// qualified (`core.LedgerApi`).
type FakeDecl struct {
	Span
	Target   []string
	Gateways []*GatewayDecl
	Values   []Arg
}

// TargetName is the fake's target as written: `LedgerApi`, `core.LedgerApi`.
func (f *FakeDecl) TargetName() string { return strings.Join(f.Target, ".") }

// Gateway returns the fake's operation named name, or nil.
func (f *FakeDecl) Gateway(name string) *GatewayDecl {
	for _, g := range f.Gateways {
		if g.Name == name {
			return g
		}
	}
	return nil
}

type WorkflowDecl struct {
	Span
	Name       string
	Params     []Param
	ReturnType Type
	Body       *Block
}

// AdapterKind is "http", "cli", "stream", "schedule", or "startup"
// (grammar.md `adapter-kind`, ADRs 0111, 0112, and 0124).
type AdapterKind string

const (
	AdapterHTTP     AdapterKind = "http"
	AdapterCLI      AdapterKind = "cli"
	AdapterStream   AdapterKind = "stream"
	AdapterSchedule AdapterKind = "schedule"
	AdapterStartup  AdapterKind = "startup"
)

type AdapterDecl struct {
	Span
	Kind       AdapterKind
	Name       string
	Principals []*PrincipalDecl // ADR 0137; http adapters only (U6)
	Routes     []*Route
}

// PrincipalDecl is `principal me: Identity from cookie "sid" by
// sessions.signedIn else sessions.signInFirst` (ADR 0137 §1): who is
// calling, settled by the adapter before it reads the request's body. It is
// read from a header or cookie (Source, SourceName), or settled from an
// earlier principal (From, §2).
type PrincipalDecl struct {
	Span
	Name       string
	Type       Type
	Source     string // "header" or "cookie"; "" when From is set
	SourceName string
	From       *Ident // the earlier principal this one is settled from
	By         *QualifiedRef
	Else       *QualifiedRef
}

// Principal is the adapter's principal named name, or nil.
func (d *AdapterDecl) Principal(name string) *PrincipalDecl {
	for _, pr := range d.Principals {
		if pr.Name == name {
			return pr
		}
	}
	return nil
}

// QualifiedRef names a declaration as a call site does:`signedIn` is Path
// [signedIn], `sessions.signedIn` is [sessions signedIn].
type QualifiedRef struct {
	Span
	Path []string
}

// Name is the reference as written, dotted.
func (r *QualifiedRef) Name() string { return strings.Join(r.Path, ".") }

// Call is the call expression the reference stands for, `ref(args...)`
// positionally: how the runtime calls a principal's binder and refusal,
// and how the walks that follow calls (config, the registry's graph) reach
// them, the same way they reach a call in a response arm.
func (r *QualifiedRef) Call(args ...Expr) *CallExpr {
	var callee Expr = &Ident{Span: r.Span, Name: r.Path[0]}
	if len(r.Path) == 2 {
		callee = &FieldAccess{Span: r.Span, Target: callee, Field: r.Path[1], FieldStart: r.Start}
	}
	call := &CallExpr{Span: r.Span, Callee: callee}
	for _, a := range args {
		call.Args = append(call.Args, Arg{Value: a})
	}
	return call
}

// Route covers http-route, cli-route, and stream-route (grammar.md): Verb
// is an HTTP method ("GET") for an http adapter, "command" for a cli
// adapter, "source" for a stream adapter, whose Path is the source's name
// (ADR 0111 §3), or "every" or "cron" for a schedule adapter, whose Path
// is the schedule (ADR 0112 §1), or "run" for a startup adapter, which has
// no Path (ADR 0124 §1) — the productions differ mainly in that
// one keyword,
// so one struct covers them rather than duplicating it.
type Route struct {
	Span
	Verb string
	Path string // the route's string-literal content
	Key  *Ident // a stream route's element key; nil otherwise or when omitted
	// Group and From are a stream route's consumer group and where a new
	// group starts reading, "earliest" or "latest" (ADR 0111 §3a); ""
	// otherwise or when omitted.
	Group string
	From  string
	// Once and Within are a startup route's `once` (one replica runs it,
	// claimed with the ScheduleLock) and `within "10s"` (its own deadline),
	// ADR 0124 §6; false and "" otherwise or when omitted.
	Once   bool
	Within string
	// Public is an http route's `public` (ADR 0137 §3): anyone may call it,
	// in an adapter that declares a principal.
	Public   bool
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
//
// A bare name with no type (`putRating(me, ...)`) passes the adapter's
// principal of that name (ADR 0137 §1): Principal is true and Type is nil.
type RouteArg struct {
	Span
	Name       string
	Type       Type
	Principal  bool
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

// DispositionResponse is what a stream or schedule route does with an
// outcome. Kind is "ack", "halt", "deadLetter", or "retry" on a stream
// (ADR 0111 §3), "done" or "halt" on a schedule (ADR 0112 §2).
type DispositionResponse struct {
	Span
	Kind     string
	Target   string               // deadLetter's stream
	Attempts int                  // retry's limit
	Else     *DispositionResponse // retry's fallback once the limit is reached; nil is halt
}

// ExprResponse is an ordinary call producing an already-built response,
// e.g. a shared `conversion` (ADR 0017's whole point: no dedicated reuse
// syntax, just an ordinary expression here).
type ExprResponse struct {
	Span
	Value Expr
}

// ApplicationDecl is `application Name { serve [role] { adapter... } }` (ADR
// 0132): what a program runs, written once. Each serve block is one process,
// the adapters it runs named as `alias.adapter` (a folder package imported with
// `use`) or bare (an adapter of the same package); a block with no role is the
// only one, and `fiig serve .` runs it.
type ApplicationDecl struct {
	Span
	Name   string
	Serves []*ServeBlock
}

// ServeBlock is one process of an application.
type ServeBlock struct {
	Span
	Role     string // "" for the one unnamed block
	RoleSpan Span
	Adapters []*AdapterRef
}

// AdapterRef is an adapter named in a serve block: `sessions.sessionsApi`
// is Path [sessions sessionsApi], `healthApi` is [healthApi].
type AdapterRef struct {
	Span
	Path []string
}

func (*ApplicationDecl) declNode() {}

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
func (*TestDecl) declNode()       {}

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
	case *ApplicationDecl:
		return d.Name
	case *TestDecl:
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
	case *ApplicationDecl:
		return "application"
	case *TestDecl:
		return "test"
	default:
		return "declaration"
	}
}

func (*DispositionResponse) responseValueNode() {}
func (*StatusResponse) responseValueNode()      {}
func (*ExitResponse) responseValueNode()        {}
func (*ExprResponse) responseValueNode()        {}

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
	// Isolation is "serializable", "repeatableRead", or "" for the
	// database's default (ADR 0088 §6).
	Isolation string
	// Attempts is retry(attempts: N)'s N, or 0 with no retry (§1).
	Attempts int64
	// AttemptsPos is where N is written, for the checker.
	AttemptsPos lexer.Pos
	Body        *Block
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

// ExpectStmt is `expect cond` or `expect value is Pattern` (ADR 0091
// §3), legal in a test and its fakes. Without a Pattern, Cond is a bool
// and the test fails when it's false; with one, Cond is any value and the
// test fails when it doesn't match, and the names the pattern binds are
// locals for the rest of the block.
type ExpectStmt struct {
	Span
	Cond    Expr
	Pattern Pattern // nil for `expect cond`
}

// PatternBinds reports whether p binds a name: `found` in `Ok(found)`, or
// a named segment of a byte pattern.
func PatternBinds(p Pattern) bool {
	switch p := p.(type) {
	case *NamePattern:
		return len(p.Path) == 1 && p.Path[0] != "None"
	case *ConstructorPattern:
		for _, a := range p.Args {
			if PatternBinds(a) {
				return true
			}
		}
	case *BytesPattern:
		for _, s := range p.Segments {
			if s.Name != "" && s.Name != "_" {
				return true
			}
		}
	}
	return false
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
func (*ExpectStmt) stmtNode()      {}

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
	// Excludes is set by the checker on a bare name against a union of
	// enums and something else (`Status | string`, ADR 0081 §3): the enums
	// the name doesn't catch, since their variants have arms of their own.
	// The runtime holds to it whatever the arms' order, as it does for
	// None. nil otherwise.
	Excludes []*EnumDecl
}

// ConstructorPattern is a qualified name with parens:
// `Ok(customer)`, `Err(_)`, `OrderError.InventoryUnavailable(_)`.
type ConstructorPattern struct {
	Span
	Path []string
	Args []Pattern
}

// LiteralPattern is an int, a string or a bool written where a pattern goes
// (ADR 0130): `404`, `-1`, `"ACME"`, `true`. It matches a value equal to it, by
// the rules of ==. A float or decimal has no literal pattern.
type LiteralPattern struct {
	Span
	Kind string // "int", "string" or "bool"
	Int  int64
	Str  string
	Bool bool
	Lit  *StringLit // a string as written, for the formatter
}

func (*LiteralPattern) patternNode() {}

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

// ErrorName is the name of the Ident the parser leaves where it found no
// expression; the parser has reported the error, so nothing else does.
const ErrorName = "<error>"

type StringLit struct {
	Span
	Parts []StringPart
}

type FieldAccess struct {
	Span
	ConstSlot // `math.pi`, a constant member of the prelude math group (ADR 0049 §2.7)
	Target    Expr
	Field     string
	// FieldStart is where the field's name is written. The span ends at the
	// next token, which is on a later line when the field ends its line, so
	// the editor's references can't be worked out from it.
	FieldStart lexer.Pos
}

type Arg struct {
	Name  string // "" for a positional argument
	Value Expr
}

type CallExpr struct {
	Span
	Callee Expr
	Args   []Arg
	// DecodeInto is the type a json.decode call decodes into (ADR 0073
	// §2), or the row type a Database query reads its rows into (ADR 0082
	// §5): a types.Type the checker takes from the call's context and
	// records here for the runtime; nil on every other call, and before
	// checking.
	DecodeInto any
	// HandlesConflicts is set by the checker on a Database call whose
	// context expects Result<..., DatabaseConflict> (ADR 0087 §1): the call
	// returns its conflicts instead of faulting.
	HandlesConflicts bool
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
	// OrDefault is set by the checker on an `or` whose left side may be
	// None (ADR 0099): `x or d` is x, or d when x is None.
	OrDefault bool
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

// SortedExpr is ADR 0065 §3's keyed sort, `sorted(x in xs): key`, or with
// more keys joined by `then`, each followed by `descending` or not (ADR
// 0093 §4): a new list of xs's elements, stably ordered by Keys in turn,
// each evaluated once per element with Var bound to it. HeaderDescending
// marks the old `sorted(x in xs, descending): key` spelling, which the
// checker rejects and fiig fmt rewrites; the parser has already moved its
// direction onto the key.
type SortedExpr struct {
	Span
	Var              string
	Iterable         Expr
	Keys             []SortKey
	HeaderDescending bool
}

// SortKey is one of a sorted form's keys.
type SortKey struct {
	Key        Expr
	Descending bool
}

// ListForm is one of ADR 0093's list forms, `filter(x in xs): body` and
// the rest: Body is evaluated once per element with Var bound to it.
type ListForm struct {
	Span
	Form     string // a ListForms member
	Var      string
	Iterable Expr
	Body     Expr
	// SumType is a sum's body type, "int", "float", or "decimal", set by
	// the checker: the type of the 0 an empty list sums to.
	SumType string
}

// ListForms are ADR 0093 §1's form names. Each is contextual: it starts
// a form only at `name ( ident in`.
var ListForms = map[string]bool{
	"filter": true, "map": true, "groupBy": true, "count": true, "any": true, "all": true,
	"sum": true, "max": true, "min": true, "maxOrNone": true, "minOrNone": true,
	"mapErr": true, // ADR 0095: over a Result, not a List
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
func (*ListForm) exprNode()       {}
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

// ColumnKey is what a SQL column name and a model field name are compared
// by when a row fills a model (ADR 0094 §1): ASCII letters lowercased and
// underscores removed, so user_id, USER_ID, userid, and userId agree.
func ColumnKey(name string) string {
	b := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '_':
			continue
		case c >= 'A' && c <= 'Z':
			c += 'a' - 'A'
		}
		b = append(b, c)
	}
	return string(b)
}
