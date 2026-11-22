// Package parser turns a lexer.Token stream into a syntax.File, per
// docs/specification/grammar.md. It parses everything the grammar sketch
// allows syntactically — including every invalid-programs/*.fiig example —
// since the type/effect checker (Milestone 2, not built yet) is what
// rejects those, not the parser. This split is deliberate throughout the
// grammar sketch and the bug-class catalog.
package parser

import (
	"fmt"
	"strconv"
	"strings"

	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
)

// Error is a parse diagnostic. JSON tags make it machine-readable
// (Milestone 1 deliverable: "parser diagnostics with structured
// machine-readable output") — the CLIs that surface this type print
// "At: Msg" by default and switch to JSON-encoding it under --json.
type Error struct {
	Msg string    `json:"message"`
	At  lexer.Pos `json:"at"`
	// Rule is the bug-classes.md ID when the grammar enforces a numbered
	// bug class itself: G1, a second public declaration (ADR 0054). "" for
	// an ordinary syntax error.
	Rule string `json:"rule,omitempty"`
}

// String is "line:col: message", with the bug class when there is one,
// as the checker's diagnostics print.
func (e Error) String() string {
	if e.Rule != "" {
		return fmt.Sprintf("%s: [%s] %s", e.At, e.Rule, e.Msg)
	}
	return fmt.Sprintf("%s: %s", e.At, e.Msg)
}

type Parser struct {
	toks   []lexer.Token
	pos    int
	errors []Error
	// keywordNames is each keyword this file named something with, which
	// expectName already reported; a later use reads as that name.
	keywordNames map[string]bool
}

func New(toks []lexer.Token) *Parser {
	return &Parser{toks: toks}
}

// Parse lexes src and parses it as a complete File.
func Parse(src string) (*syntax.File, []Error) {
	l := lexer.New(src)
	toks := l.AllTokens()
	p := New(toks)
	f := p.parseFile()
	var errs []Error
	for _, e := range l.Errors() {
		errs = append(errs, Error{Msg: e.Msg, At: e.At})
	}
	errs = append(errs, p.errors...)
	return f, withHint(toks, errs)
}

func (p *Parser) Errors() []Error { return p.errors }

// ParseWithComments is Parse, plus the source's line comments in source
// order — for tooling (the formatter) that needs to re-attach them.
// Ordinary parsing doesn't need them, since comments aren't part of the
// grammar; that's why Parse doesn't return them.
func ParseWithComments(src string) (*syntax.File, []lexer.Comment, []Error) {
	l := lexer.New(src)
	toks := l.AllTokens()
	comments := l.Comments()
	p := New(toks)
	f := p.parseFile()
	var errs []Error
	for _, e := range l.Errors() {
		errs = append(errs, Error{Msg: e.Msg, At: e.At})
	}
	errs = append(errs, p.errors...)
	return f, comments, withHint(toks, errs)
}

// ---- token stream helpers -----------------------------------------------

func (p *Parser) cur() lexer.Token {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return lexer.Token{Kind: lexer.EOF}
}

func (p *Parser) peek(offset int) lexer.Token {
	i := p.pos + offset
	if i < len(p.toks) {
		return p.toks[i]
	}
	return lexer.Token{Kind: lexer.EOF}
}

func (p *Parser) at(k lexer.Kind) bool {
	return p.cur().Kind == k
}

func (p *Parser) advance() lexer.Token {
	t := p.cur()
	if p.pos < len(p.toks) {
		p.pos++
	}
	return t
}

// expect consumes the current token if it matches k, else records an error
// and returns the unconsumed token so callers can keep building a partial
// AST without desynchronizing the stream on every single error.
func (p *Parser) expect(k lexer.Kind) lexer.Token {
	if p.at(k) {
		return p.advance()
	}
	p.errorf(p.cur().Start, "expected %s, got %s", k, p.cur().Kind)
	return p.cur()
}

// structuralKeywords are reserved only at declaration-start position
// (docs/decisions/0018-contextual-structural-keywords.md); everywhere else a
// name is expected — fields, params, bindings, patterns, bare identifier
// references — they parse as ordinary identifiers, the same precedent
// grammar.md §1 already set for Result/Ok/Err/None.
var structuralKeywords = map[lexer.Kind]bool{
	lexer.KwModel:      true,
	lexer.KwEnum:       true,
	lexer.KwRule:       true,
	lexer.KwConversion: true,
	lexer.KwActivity:   true,
	lexer.KwGateway:    true,
	lexer.KwWorkflow:   true,
	lexer.KwAdapter:    true,
	lexer.KwInterface:  true,
	lexer.KwCapability: true,
	lexer.KwFormulas:   true,
	lexer.KwFormula:    true,
	lexer.KwConfig:     true,
	// An adapter kind is reserved only right after `adapter`. Everywhere
	// else it is a name, which a package alias needs: `use acme.http` binds
	// `http` (ADR 0043).
	lexer.KwHttp: true,
	lexer.KwCli:  true,
}

func isNameToken(k lexer.Kind) bool {
	return k == lexer.Ident || structuralKeywords[k]
}

// atName reports whether the current token can serve as a name: an
// identifier, or a structural keyword used outside declaration-start
// position.
func (p *Parser) atName() bool {
	return isNameToken(p.cur().Kind)
}

// expectName is expect(Ident), extended to accept a structural keyword as a
// name per ADR 0018.
func (p *Parser) expectName() lexer.Token {
	if p.atName() {
		return p.advance()
	}
	if lexer.IsKeyword(p.cur().Kind) {
		// abs-recommendations GAPS G33: say it's reserved, and take it as
		// the name, so the rest of the line parses.
		tok := p.cur()
		p.errorf(tok.Start, "`%s` is a keyword, so it can't name a parameter or a local; choose another name (a field may use it, ADR 0073 §7a)", tok.Text)
		if p.keywordNames == nil {
			p.keywordNames = map[string]bool{}
		}
		p.keywordNames[tok.Text] = true
		return p.advance()
	}
	p.errorf(p.cur().Start, "expected a name, got %s", p.cur().Kind)
	return p.cur()
}

// atFieldName reports whether the current token can name a model or enum
// payload field: an identifier or any keyword (ADR 0073 §7a). A field's
// name is only ever read in its declaration, after a `.`, and as a named
// argument, where no keyword means anything else.
func (p *Parser) atFieldName() bool {
	return p.atName() || lexer.IsKeyword(p.cur().Kind)
}

// expectFieldName is expectName for a field name.
func (p *Parser) expectFieldName() lexer.Token {
	if p.atFieldName() {
		return p.advance()
	}
	return p.expectName()
}

// guardProgress runs parse and guarantees the token stream advances by at
// least one token, even if parse itself fails to consume anything. Without
// this, a caller looping on "not at the closing brace yet" can spin forever
// on a single stuck token (docs/decisions/0018-contextual-structural-keywords.md).
func (p *Parser) guardProgress(parse func()) {
	before := p.pos
	parse()
	if p.pos == before {
		p.errorf(p.cur().Start, "unexpected token %s", p.cur().Kind)
		p.advance()
	}
}

func (p *Parser) errorf(at lexer.Pos, format string, args ...any) {
	p.errorRule(at, "", format, args...)
}

func (p *Parser) errorRule(at lexer.Pos, rule, format string, args ...any) {
	p.errors = append(p.errors, Error{Msg: fmt.Sprintf(format, args...), At: at, Rule: rule})
}

// ---- file, imports, declarations -----------------------------------------

func (p *Parser) parseFile() *syntax.File {
	start := p.cur().Start
	f := &syntax.File{}
	for p.at(lexer.KwUse) {
		f.Imports = append(f.Imports, p.parseImport())
	}
	// ADR 0054: one public declaration, then at most one private block.
	// A second public declaration is a parse error, but it is still parsed
	// and kept in Decls so the rest of the file gets checked: the
	// invalid-programs fixtures pack many declarations into one file.
	for !p.at(lexer.EOF) {
		before := p.pos
		if p.at(lexer.KwPrivate) {
			p.parseFilePrivateBlock(f)
		} else if d := p.parseDecl(); d != nil {
			_, isTest := d.(*syntax.TestDecl)
			if isTest && len(f.Decls) > 0 && f.Private == nil && allTests(f.Decls) {
				// ADR 0102 §2: a test file holds many tests; tests aren't
				// in the program, so the one-declaration rule doesn't apply.
			} else if len(f.Decls) > 0 && allTests(f.Decls) && !isTest {
				start, _ := d.(syntax.Spanned).Bounds()
				p.errorRule(start, "G1", "%s %q is in a test file; a test file holds only tests and their private block (ADR 0102 §2)", syntax.DeclKindName(d), syntax.DeclName(d))
			} else if len(f.Decls) > 0 || f.Private != nil {
				start, _ := d.(syntax.Spanned).Bounds()
				p.errorRule(start, "G1", "%s %q is a second public declaration; a file holds exactly one (ADR 0054). Move it into this file's private block, or into its own file", syntax.DeclKindName(d), syntax.DeclName(d))
			}
			f.Decls = append(f.Decls, d)
		}
		if p.pos == before {
			// Safety net: guarantee forward progress on unrecognized input.
			p.errorf(p.cur().Start, "unexpected token %s at top level", p.cur().Kind)
			p.advance()
		}
	}
	f.Span = syntax.Span{Start: start, End: p.cur().End}
	return f
}

// parseFilePrivateBlock parses a file's `private { ... }` block (ADR 0054)
// and attaches it to f, reporting it when it is misplaced: a second block,
// one with no public declaration before it, or one after a declaration that
// holds no code for a helper to serve.
func (p *Parser) parseFilePrivateBlock(f *syntax.File) {
	start := p.cur().Start
	block := p.parsePrivateBlock()
	switch {
	case f.Private != nil:
		p.errorf(start, "a file may have only one private block (ADR 0054)")
		return
	case len(f.Decls) == 0:
		p.errorf(start, "a private block must follow the file's public declaration (ADR 0054)")
	default:
		switch d := f.Decls[0].(type) {
		case *syntax.RuleDecl, *syntax.ConversionDecl, *syntax.ActivityDecl, *syntax.GatewayDecl, *syntax.WorkflowDecl, *syntax.TestDecl:
		case *syntax.CapabilityDecl:
			p.errorf(start, "%s", groupHelperHint)
		default:
			p.errorf(start, "%s %q may not have a private block; only a rule, conversion, activity, gateway, workflow, or test holds code a helper could serve (ADR 0054)", syntax.DeclKindName(d), syntax.DeclName(d))
		}
	}
	f.Private = block
}

// parsePrivateBlock parses `private { member* }`. A member is a model, or a
// helper function written `name(params) -> Type { ... }` with no kind
// keyword. A kind keyword in front of a helper is reported and skipped, and
// an enum is reported and dropped (ADR 0054 §2 leaves enums to a follow-up).
func (p *Parser) parsePrivateBlock() *syntax.PrivateBlock {
	start := p.advance().Start // 'private'
	block := &syntax.PrivateBlock{}
	p.expect(lexer.LBrace)
	for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
		p.guardProgress(func() {
			switch p.cur().Kind {
			case lexer.KwModel:
				block.Members = append(block.Members, p.parseModelDecl())
			case lexer.KwEnum:
				p.errorf(p.cur().Start, "enums are not allowed in a private block yet (ADR 0054 §2)")
				p.parseEnumDecl()
			case lexer.KwRule, lexer.KwConversion, lexer.KwActivity, lexer.KwGateway, lexer.KwWorkflow:
				if p.peek(1).Kind == lexer.LParen {
					// `rule(...)` is a helper named after a keyword, not a
					// keyword in front of a helper.
					block.Members = append(block.Members, p.parseHelperDecl())
					return
				}
				kw := p.advance()
				p.errorf(kw.Start, "a private helper has no kind keyword; remove %q (it takes its file's kind, ADR 0054)", kw.Text)
				block.Members = append(block.Members, p.parseHelperDecl())
			default:
				if p.atName() && p.peek(1).Kind == lexer.LParen {
					block.Members = append(block.Members, p.parseHelperDecl())
					return
				}
				p.errorf(p.cur().Start, "expected a helper function or a model in a private block, got %s", p.cur().Kind)
			}
		})
	}
	end := p.expect(lexer.RBrace).End
	block.Span = syntax.Span{Start: start, End: end}
	return block
}

func (p *Parser) parseHelperDecl() *syntax.HelperDecl {
	start := p.cur().Start
	name := p.expectName()
	params := p.parseParams()
	p.expect(lexer.Arrow)
	ret := p.parseType()
	body := p.parseBlock()
	return &syntax.HelperDecl{Span: syntax.Span{Start: start, End: body.End}, Name: name.Text, Params: params, ReturnType: ret, Body: body}
}

func (p *Parser) parseImport() *syntax.Import {
	start := p.advance().Start // 'use'
	imp := &syntax.Import{}
	first := p.expectPathSegment()
	imp.Path = []string{first.Text}
	for p.at(lexer.Dot) {
		p.advance()
		if p.at(lexer.Star) {
			// There is no wildcard import (ADR 0050 §2.4): every
			// cross-package reference is qualified, so `.*` has nothing to
			// be the degenerate case of. Reported here, tagged G2 like G1's
			// second public declaration, and skipped so the rest of the
			// file still parses as the plain import of the path before it.
			star := p.advance()
			p.errorRule(star.Start, "G2", "there is no wildcard import; write `use %s` and qualify each name, as in `%s.Name` (ADR 0050 §2.4)", strings.Join(imp.Path, "."), imp.Path[len(imp.Path)-1])
			break
		}
		seg := p.expectPathSegment()
		imp.Path = append(imp.Path, seg.Text)
	}
	if p.at(lexer.KwAs) {
		p.advance()
		imp.Alias = p.expectName().Text
	}
	imp.Span = syntax.Span{Start: start, End: p.cur().Start}
	return imp
}

// expectPathSegment is expectName for one segment of a `use` path, which
// also accepts any keyword: a path is delimited by `use`, `.`, `as`, and end
// of line, so a segment is never ambiguous, and a package is entitled to a
// name like `acme.http` even though `http` is a keyword (an adapter kind).
func (p *Parser) expectPathSegment() lexer.Token {
	if t := p.cur(); t.Kind != lexer.Ident && t.Kind != lexer.KwAs && isWord(t.Text) {
		return p.advance()
	}
	return p.expectName()
}

func isWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func (p *Parser) parseDecl() syntax.Decl {
	switch p.cur().Kind {
	case lexer.KwModel:
		return p.parseModelDecl()
	case lexer.KwEnum:
		return p.parseEnumDecl()
	case lexer.KwRule:
		return p.parseRuleDecl()
	case lexer.KwConversion:
		return p.parseConversionDecl()
	case lexer.KwActivity:
		return p.parseActivityDecl()
	case lexer.KwGateway:
		return p.parseGatewayDecl()
	case lexer.KwWorkflow:
		return p.parseWorkflowDecl()
	case lexer.KwAdapter:
		return p.parseAdapterDecl()
	case lexer.KwInterface:
		return p.parseInterfaceDecl()
	case lexer.KwCapability:
		return p.parseCapabilityDecl()
	case lexer.KwFormulas:
		return p.parseFormulasDecl()
	case lexer.KwConfig:
		return p.parseConfigDecl()
	default:
		if p.atContextual("test") && isNameToken(p.peek(1).Kind) {
			return p.parseTestDecl()
		}
		// ADR 0132: `application` starts a declaration only here, before a
		// name and a brace, so it stays an ordinary identifier everywhere else.
		if p.atContextual("application") && isNameToken(p.peek(1).Kind) && p.peek(2).Kind == lexer.LBrace {
			return p.parseApplicationDecl()
		}
		// ADR 0143: `wire` is contextual, a keyword only here, before a name
		// and `:` (one value) or `{`/`when` (a group), so it stays a name
		// everywhere else. `principal` is its old spelling (ADR 0141).
		if (p.atContextual("wire") || p.atContextual("principal")) && isNameToken(p.peek(1).Kind) && p.peek(2).Kind == lexer.Colon {
			return p.parsePrincipal()
		}
		if p.atContextual("wire") && isNameToken(p.peek(1).Kind) && (p.peek(2).Kind == lexer.LBrace || p.peek(2).Kind == lexer.Ident && p.peek(2).Text == "when") {
			return p.parseConfigDecl()
		}
		if p.cur().Kind == lexer.Ident && p.cur().Text == "value" {
			// ADR 0055: `value Name: T` was removed, not renamed — say so
			// rather than leaving a generic "expected a declaration".
			p.errorf(p.cur().Start, "`value` declarations were removed (ADR 0055); use the underlying type directly, or a `model` if it needs structure")
			// Skip the rest of the one-line declaration so it reports once,
			// not once per leftover token.
			line := p.cur().Start.Line
			for !p.at(lexer.EOF) && p.cur().Start.Line == line {
				p.advance()
			}
			return nil
		}
		p.errorf(p.cur().Start, "expected a declaration (model/enum/rule/conversion/activity/gateway/workflow/adapter/interface/capability/formulas/config/principal/test), got %s", p.cur().Kind)
		return nil
	}
}

// parseApplicationDecl parses `application Name { serve [role] { adapter... } }`
// (ADR 0132). `application` and `serve` are contextual.
func (p *Parser) parseApplicationDecl() *syntax.ApplicationDecl {
	start := p.advance().Start // 'application'
	name := p.expectName()
	p.expect(lexer.LBrace)
	d := &syntax.ApplicationDecl{Name: name.Text}
	for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
		p.guardProgress(func() {
			if !p.atContextual("serve") {
				p.errorf(p.cur().Start, "expected `serve` in an application, got %s; an application says what each process serves: `serve { adapterName ... }` (ADR 0132)", p.cur().Kind)
				p.advance()
				return
			}
			d.Serves = append(d.Serves, p.parseServeBlock())
		})
	}
	end := p.expect(lexer.RBrace).End
	d.Span = syntax.Span{Start: start, End: end}
	return d
}

// parseServeBlock parses `serve [role] { alias.adapter ... }`: a role is a name
// before the brace, and the adapters follow one after another, separated by
// whitespace or commas.
func (p *Parser) parseServeBlock() *syntax.ServeBlock {
	start := p.advance().Start // 'serve'
	b := &syntax.ServeBlock{}
	if p.atName() && p.peek(1).Kind == lexer.LBrace {
		role := p.advance()
		b.Role = role.Text
		b.RoleSpan = syntax.Span{Start: role.Start, End: role.End}
	}
	p.expect(lexer.LBrace)
	for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
		p.guardProgress(func() {
			refStart := p.cur().Start
			path := []string{p.expectPathSegment().Text}
			for p.at(lexer.Dot) {
				p.advance()
				path = append(path, p.expectPathSegment().Text)
			}
			b.Adapters = append(b.Adapters, &syntax.AdapterRef{Span: syntax.Span{Start: refStart, End: p.cur().Start}, Path: path})
			if p.at(lexer.Comma) {
				p.advance()
			}
		})
	}
	end := p.expect(lexer.RBrace).End
	b.Span = syntax.Span{Start: start, End: end}
	return b
}

// parseField parses a model, payload, or record field. jsonName allows a
// `from json` clause after the type (ADR 0073 §7b, ADR 0078 §6), which
// model and payload fields may have.
func (p *Parser) parseField(jsonName bool) syntax.Field {
	start := p.cur().Start
	name := p.expectFieldName()
	p.expect(lexer.Colon)
	typ := p.parseType()
	f := syntax.Field{Name: name.Text, Type: typ}
	if jsonName {
		p.parseFieldJSON(&f)
	}
	f.Span = syntax.Span{Start: start, End: p.cur().Start}
	return f
}

// parseFieldJSON parses a field's optional `from json` clause: a member
// name (ADR 0073 §7b), the contextual word `base64url` (ADR 0078 §6), or
// both in that order, as `raw: bytes from json "raw_data" base64url`.
func (p *Parser) parseFieldJSON(f *syntax.Field) {
	if !p.atContextual("from") || p.peek(1).Kind != lexer.Ident || p.peek(1).Text != "json" {
		return
	}
	p.advance()
	p.advance()
	named := p.at(lexer.StringStart)
	if named {
		f.JSON = p.jsonText("a JSON member name")
	}
	if p.atContextual("base64url") {
		p.advance()
		f.Base64URL = true
	} else if !named {
		p.errorf(p.cur().Start, "expected a JSON member name or `base64url` after `from json`, got %s", p.cur().Kind)
	}
}

// parseModelField is parseField, plus ADR 0045's embedded form: a bare type
// name with no `name:` in front of it. A name token followed by a colon is
// an ordinary field; anything else is parsed as a type and kept as an
// embedded field named for it (what the checker validates is that it names
// a model).
func (p *Parser) parseModelField() syntax.Field {
	if p.atFieldName() && p.peek(1).Kind == lexer.Colon {
		return p.parseField(true)
	}
	start := p.cur().Start
	typ := p.parseType()
	name := ""
	if qt, ok := typ.(*syntax.QualifiedType); ok {
		name = strings.Join(qt.Path, ".")
	}
	return syntax.Field{Span: syntax.Span{Start: start, End: p.cur().Start}, Name: name, Type: typ, Embedded: true}
}

func (p *Parser) parseParams() []syntax.Param {
	p.expect(lexer.LParen)
	var params []syntax.Param
	if !p.at(lexer.RParen) {
		params = append(params, p.parseParam())
		for p.at(lexer.Comma) {
			p.advance()
			// A trailing comma, as a call's arguments allow: how `fiig fmt`
			// writes parameters one per line.
			if p.at(lexer.RParen) {
				break
			}
			params = append(params, p.parseParam())
		}
	}
	p.expect(lexer.RParen)
	return params
}

func (p *Parser) parseParam() syntax.Param {
	start := p.cur().Start
	name := p.expectName()
	p.expect(lexer.Colon)
	typ := p.parseType()
	return syntax.Param{Span: syntax.Span{Start: start, End: p.cur().Start}, Name: name.Text, Type: typ}
}

func (p *Parser) parseModelDecl() *syntax.ModelDecl {
	start := p.advance().Start // 'model'
	name := p.expectName()
	p.expect(lexer.LBrace)
	var fields []syntax.Field
	for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
		p.guardProgress(func() { fields = append(fields, p.parseModelField()) })
	}
	end := p.expect(lexer.RBrace).End
	return &syntax.ModelDecl{Span: syntax.Span{Start: start, End: end}, Name: name.Text, Fields: fields}
}

// parseConfigDecl parses `config Name [when condition] { name: Type from env
// "NAME" = default }` (ADR 0071 §1, ADR 0122). `from`, `env` and `when` are
// contextual here only, as `from` and `env` are in a route argument.
func (p *Parser) parseConfigDecl() *syntax.ConfigDecl {
	kw := p.advance() // 'wire', or 'config' before ADR 0143
	start := kw.Start
	name := p.expectName()
	var when syntax.Expr
	if p.atContextual("when") {
		p.advance()
		when = p.parseExpression()
	}
	p.expect(lexer.LBrace)
	var fields []syntax.ConfigField
	for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
		p.guardProgress(func() { fields = append(fields, p.parseConfigField()) })
	}
	end := p.expect(lexer.RBrace).End
	return &syntax.ConfigDecl{Span: syntax.Span{Start: start, End: end}, Keyword: kw.Text, Name: name.Text, When: when, Fields: fields}
}

func (p *Parser) parseConfigField() syntax.ConfigField {
	start := p.cur().Start
	name := p.expectName()
	p.expect(lexer.Colon)
	f := syntax.ConfigField{Name: name.Text, Type: p.parseType()}
	isSource := func(at int) bool {
		t := p.peek(at)
		return t.Kind == lexer.Ident && (t.Text == "env" || t.Text == "setting")
	}
	if p.atContextual("from") && isSource(1) {
		p.advance()
		p.parseConfigSources(&f)
	} else if p.atContextual("from") && p.peek(1).Kind == lexer.Ident && p.peek(1).Text == "startup" {
		p.advance()
		p.advance()
		lit := p.parseStringLit()
		f.Startup = plainText(lit)
		if hasInterp(lit) {
			p.errorf(lit.Start, "interpolation is not allowed in a startup route's name")
		}
	} else {
		if p.atContextual("from") && p.peek(1).Kind == lexer.Ident && (p.peek(1).Text == "cookie" || p.peek(1).Text == "header") {
			p.errorf(p.cur().Start, "[U10] field %s reads a %s, which belongs to one request, so it is a single wire with its refusal: `wire %s: Type from %s \"...\" by binder else refusal` (ADR 0143 §3)", name.Text, p.peek(1).Text, name.Text, p.peek(1).Text)
			for !p.at(lexer.EOF) && p.cur().Start.Line == start.Line {
				p.advance()
			}
			f.Span = syntax.Span{Start: start, End: p.cur().Start}
			return f
		}
		p.errorf(p.cur().Start, "expected `from env \"NAME\"`, `from setting \"name\"` or `from startup \"Adapter.workflow\"` after field %q's type (ADR 0071 §1, ADR 0140, ADR 0127), got %s", name.Text, p.cur().Kind)
	}
	if p.at(lexer.Equals) {
		p.advance()
		f.Default = p.parseExpression()
	}
	f.Span = syntax.Span{Start: start, End: p.cur().Start}
	return f
}

// parseConfigSources parses `env "X" [or setting "x" [on restart]]`, in
// either order (ADR 0140 §1). `setting`, `on` and `restart` are contextual
// here only.
func (p *Parser) parseConfigSources(f *syntax.ConfigField) {
	for i := 0; ; i++ {
		kind := p.advance() // env | setting
		lit := p.parseStringLit()
		switch kind.Text {
		case "env":
			if f.Env != "" {
				p.errorf(kind.Start, "config field %q reads env twice; a field has at most one env and one setting source", f.Name)
			}
			f.Env = plainText(lit)
			if hasInterp(lit) {
				p.errorf(lit.Start, "interpolation is not allowed in an environment variable's name")
			}
		case "setting":
			if f.Setting != "" {
				p.errorf(kind.Start, "config field %q reads setting twice; a field has at most one env and one setting source", f.Name)
			}
			f.Setting = plainText(lit)
			f.SettingFirst = i == 0
			if hasInterp(lit) {
				p.errorf(lit.Start, "interpolation is not allowed in a setting's name")
			}
			if p.atContextual("on") && p.peek(1).Kind == lexer.Ident && p.peek(1).Text == "restart" {
				p.advance()
				p.advance()
				f.OnRestart = true
			}
		}
		// `??` joins sources (ADR 0143 §7); `or`, ADR 0140's spelling,
		// parses until ADR 0142 §5's second step.
		next := p.peek(1)
		if !(p.at(lexer.QuestionQuestion) || p.at(lexer.KwOr)) || next.Kind != lexer.Ident || (next.Text != "env" && next.Text != "setting") {
			return
		}
		p.advance() // ?? or or
	}
}

func (p *Parser) parseEnumDecl() *syntax.EnumDecl {
	start := p.advance().Start // 'enum'
	name := p.expectName()
	var header []syntax.EnumField
	if p.at(lexer.LParen) {
		p.advance()
		header = append(header, p.parseEnumField())
		for p.at(lexer.Comma) {
			p.advance()
			header = append(header, p.parseEnumField())
		}
		p.expect(lexer.RParen)
	}
	p.expect(lexer.LBrace)
	var variants []syntax.EnumVariant
	for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
		p.guardProgress(func() { variants = append(variants, p.parseEnumVariant(header != nil)) })
	}
	end := p.expect(lexer.RBrace).End
	return &syntax.EnumDecl{Span: syntax.Span{Start: start, End: end}, Name: name.Text, Header: header, Variants: variants}
}

// parseEnumField is one header column (ADR 0046 §4). `key` is contextual:
// it is the modifier only when another name follows it, so a field may
// still be named key (`key: string`).
func (p *Parser) parseEnumField() syntax.EnumField {
	start := p.cur().Start
	isKey := false
	if p.at(lexer.Ident) && p.cur().Text == "key" && isNameToken(p.peek(1).Kind) {
		p.advance()
		isKey = true
	}
	name := p.expectName()
	p.expect(lexer.Colon)
	typ := p.parseType()
	return syntax.EnumField{Span: syntax.Span{Start: start, End: p.cur().Start}, Name: name.Text, Key: isKey, Type: typ}
}

// parseEnumVariant parses a payload (`NotFound(id: CustomerId)`) on an
// enum with no header, and a row of constants (`Acme(code: "ACME")`) on
// one with a header: the same `name: x` shape, where x is a type in the
// first and a value in the second (ADR 0046 §4).
func (p *Parser) parseEnumVariant(headed bool) syntax.EnumVariant {
	start := p.cur().Start
	name := p.expectName()
	if headed {
		var values []syntax.Arg
		if p.at(lexer.LParen) {
			values = p.parseArguments()
		}
		json := p.parseJSONName("an enum variant's spelling")
		return syntax.EnumVariant{Span: syntax.Span{Start: start, End: p.cur().Start}, Name: name.Text, Values: values, JSON: json}
	}
	var fields []syntax.Field
	if p.at(lexer.LParen) {
		p.advance()
		if !p.at(lexer.RParen) {
			fields = append(fields, p.parseField(true))
			for p.at(lexer.Comma) {
				p.advance()
				fields = append(fields, p.parseField(true))
			}
		}
		p.expect(lexer.RParen)
	}
	json := p.parseJSONName("an enum variant's spelling")
	return syntax.EnumVariant{Span: syntax.Span{Start: start, End: p.cur().Start}, Name: name.Text, Fields: fields, JSON: json}
}

// parseJSONName parses an optional `from json "text"` clause: a field's
// member name (ADR 0073 §7b) or a variant's spelling (ADR 0080 §1). what
// names it in errors. "" when there is none.
func (p *Parser) parseJSONName(what string) string {
	if !p.atContextual("from") || p.peek(1).Kind != lexer.Ident || p.peek(1).Text != "json" {
		return ""
	}
	p.advance()
	p.advance()
	return p.jsonText(what)
}

// jsonText parses the string literal of a `from json "text"` clause, which
// can be neither interpolated nor empty. what names it in errors.
func (p *Parser) jsonText(what string) string {
	lit := p.parseStringLit()
	if hasInterp(lit) {
		p.errorf(lit.Start, "interpolation is not allowed in %s", what)
	}
	text := plainText(lit)
	if text == "" {
		// "" would read as no `from json` at all; no producer sends it.
		p.errorf(lit.Start, "%s can't be empty", what)
	}
	return text
}

func (p *Parser) parseRuleDecl() *syntax.RuleDecl {
	start := p.advance().Start // 'rule'
	name := p.expectName()
	params := p.parseParams()
	p.expect(lexer.Arrow)
	ret := p.parseType()
	body := p.parseBlock()
	return &syntax.RuleDecl{Span: syntax.Span{Start: start, End: body.End}, Name: name.Text, Params: params, ReturnType: ret, Body: body}
}

func (p *Parser) parseConversionDecl() *syntax.ConversionDecl {
	start := p.advance().Start // 'conversion'
	name := p.expectName()
	params := p.parseParams()
	p.expect(lexer.Arrow)
	ret := p.parseType()
	body := p.parseBlock()
	return &syntax.ConversionDecl{Span: syntax.Span{Start: start, End: body.End}, Name: name.Text, Params: params, ReturnType: ret, Body: body}
}

// parseActivityDecl (ADR 0030) is identical in shape to parseRuleDecl/
// parseConversionDecl — no `requires` clause exists in this grammar at
// all, since an Activity never touches a capability.
func (p *Parser) parseActivityDecl() *syntax.ActivityDecl {
	start := p.advance().Start // 'activity'
	name := p.expectName()
	params := p.parseParams()
	p.expect(lexer.Arrow)
	ret := p.parseType()
	body := p.parseBlock()
	return &syntax.ActivityDecl{Span: syntax.Span{Start: start, End: body.End}, Name: name.Text, Params: params, ReturnType: ret, Body: body}
}

func (p *Parser) parseGatewayDecl() *syntax.GatewayDecl {
	return p.parseGateway(false)
}

// parseGateway parses a gateway; in a fake (ADR 0091 §4) its `requires`
// clause is optional, since a fake operation touches nothing unless it
// says so.
func (p *Parser) parseGateway(inFake bool) *syntax.GatewayDecl {
	start := p.advance().Start // 'gateway'
	name := p.expectName()
	params := p.parseParams()
	p.expect(lexer.Arrow)
	ret := p.parseType()
	var capability lexer.Token
	if !inFake || p.at(lexer.KwRequires) {
		p.expect(lexer.KwRequires)
		capability = p.expectName()
	}
	idempotent := false
	if p.at(lexer.KwIdempotent) {
		p.advance()
		idempotent = true
	}
	body := p.parseBlock()
	return &syntax.GatewayDecl{
		Span: syntax.Span{Start: start, End: body.End}, Name: name.Text, Params: params,
		ReturnType: ret, Requires: capability.Text, RequiresStart: capability.Start, Idempotent: idempotent, Body: body,
	}
}

// parseInterfaceDecl parses `interface Name { member(params) -> Type ... }`
// (ADR 0043 §1). A member is a gateway signature with no keyword, no
// `requires`, and no body.
func (p *Parser) parseInterfaceDecl() *syntax.InterfaceDecl {
	start := p.advance().Start // 'interface'
	name := p.expectName()
	p.expect(lexer.LBrace)
	var members []syntax.InterfaceMember
	for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
		p.guardProgress(func() {
			mStart := p.cur().Start
			mName := p.expectName()
			params := p.parseParams()
			p.expect(lexer.Arrow)
			ret := p.parseType()
			members = append(members, syntax.InterfaceMember{Span: syntax.Span{Start: mStart, End: p.cur().Start}, Name: mName.Text, Params: params, ReturnType: ret})
		})
	}
	end := p.expect(lexer.RBrace).End
	return &syntax.InterfaceDecl{Span: syntax.Span{Start: start, End: end}, Name: name.Text, Members: members}
}

// parseCapabilityDecl parses `capability Name { gateway ... }` (ADR 0043
// §5a). Its members are gateways and nothing else.
func (p *Parser) parseCapabilityDecl() *syntax.CapabilityDecl {
	start := p.advance().Start // 'capability'
	name := p.expectName()
	// ADR 0105 §3: `capability X provides alias.Interface`.
	var provides []string
	if p.atContextual("provides") {
		p.advance()
		provides = p.parseQualifiedName()
	}
	p.expect(lexer.LBrace)
	var gateways []*syntax.GatewayDecl
	for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
		if !p.at(lexer.KwGateway) {
			if p.at(lexer.KwPrivate) {
				p.errorf(p.cur().Start, "%s", groupHelperHint)
			} else {
				p.errorf(p.cur().Start, "a capability group holds only gateways, got %s", p.cur().Kind)
			}
			p.skipToGroupMember()
			continue
		}
		gateways = append(gateways, p.parseGatewayDecl())
	}
	end := p.expect(lexer.RBrace).End
	return &syntax.CapabilityDecl{Span: syntax.Span{Start: start, End: end}, Name: name.Text, Gateways: gateways, Declares: provides}
}

// groupHelperHint is the error for a private block in or after a
// capability group: what a group's gateways share instead (abs-recommendations
// GAPS G8).
const groupHelperHint = "a capability group may not have a private block; to share steps between its gateways, put them in a conversion that turns a result into the group's error, and call it with `?`, as fiig.postgres does: `connection = connectionOr(Socket.connect(host, port))?` (docs/writing-a-client.md)"

// skipToGroupMember skips what isn't a gateway in a capability group, up
// to the next `gateway` or the group's closing brace, so one wrong member
// is one error.
func (p *Parser) skipToGroupMember() {
	depth := 0
	for !p.at(lexer.EOF) {
		switch {
		case p.at(lexer.LBrace):
			depth++
		case p.at(lexer.RBrace):
			if depth == 0 {
				return
			}
			depth--
		case p.at(lexer.KwGateway) && depth == 0:
			return
		}
		p.advance()
	}
}

// parseFormulasDecl parses `formulas name { formula ... }` (ADR 0049 §2.1).
// Its members are formulas and nothing else.
func (p *Parser) parseFormulasDecl() *syntax.FormulasDecl {
	start := p.advance().Start // 'formulas'
	name := p.expectName()
	p.expect(lexer.LBrace)
	var members []*syntax.FormulaDecl
	for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
		p.guardProgress(func() {
			if !p.at(lexer.KwFormula) {
				p.errorf(p.cur().Start, "a formulas group holds only formulas, got %s", p.cur().Kind)
				return
			}
			members = append(members, p.parseFormulaDecl())
		})
	}
	end := p.expect(lexer.RBrace).End
	return &syntax.FormulasDecl{Span: syntax.Span{Start: start, End: end}, Name: name.Text, Members: members}
}

// parseFormulaDecl parses one `formula name(params) -> Type [block]`. With
// no block it is a native leaf (§2.4). A `requires` clause is not in the
// grammar, but it is parsed and kept so the checker can reject it as K4,
// with §2.2's reason, rather than as an unexpected token.
func (p *Parser) parseFormulaDecl() *syntax.FormulaDecl {
	start := p.advance().Start // 'formula'
	name := p.expectName()
	params := p.parseParams()
	p.expect(lexer.Arrow)
	ret := p.parseType()
	requires := ""
	if p.at(lexer.KwRequires) {
		p.advance()
		requires = p.expectName().Text
	}
	f := &syntax.FormulaDecl{Name: name.Text, Params: params, ReturnType: ret, Requires: requires}
	end := p.cur().Start
	if p.at(lexer.LBrace) {
		f.Body = p.parseBlock()
		end = f.Body.End
	}
	f.Span = syntax.Span{Start: start, End: end}
	return f
}

// parseTestDecl parses `test name { fake ... statements }` (ADR 0091 §1).
// `test` and `fake` are contextual: a test begins with the word `test`
// and a name, and its fakes are the `fake Name {` blocks before its first
// statement.
func (p *Parser) parseTestDecl() *syntax.TestDecl {
	start := p.advance().Start // 'test'
	name := p.expectName()
	d := &syntax.TestDecl{Name: name.Text}
	bodyStart := p.expect(lexer.LBrace).Start
	for p.atFake() {
		d.Fakes = append(d.Fakes, p.parseFakeDecl())
	}
	var stmts []syntax.Stmt
	for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
		before := p.pos
		if p.atFake() {
			p.errorf(p.cur().Start, "a fake comes before the test's first statement (ADR 0091 §4)")
			p.parseFakeDecl()
			continue
		}
		stmts = append(stmts, p.parseStatement())
		if p.pos == before {
			p.errorf(p.cur().Start, "unexpected token %s in statement", p.cur().Kind)
			p.advance()
		}
		if p.at(lexer.Semicolon) { // ADR 0142 §1, as parseBlock
			p.advance()
		}
	}
	end := p.expect(lexer.RBrace).End
	d.Body = &syntax.Block{Span: syntax.Span{Start: bodyStart, End: end}, Stmts: stmts}
	d.Span = syntax.Span{Start: start, End: end}
	return d
}

// atFake reports whether a `fake Name {` or `fake pkg.Name {` block starts
// here.
func (p *Parser) atFake() bool {
	if !p.atContextual("fake") || !isNameToken(p.peek(1).Kind) {
		return false
	}
	i := 2
	for p.peek(i).Kind == lexer.Dot && isNameToken(p.peek(i+1).Kind) {
		i += 2
	}
	return p.peek(i).Kind == lexer.LBrace
}

// parseFakeDecl parses `fake Target { gateway ... | name: expr ... }`
// (ADR 0091 §4).
func (p *Parser) parseFakeDecl() *syntax.FakeDecl {
	start := p.advance().Start // 'fake'
	f := &syntax.FakeDecl{Target: []string{p.expectName().Text}}
	for p.at(lexer.Dot) {
		p.advance()
		f.Target = append(f.Target, p.expectName().Text)
	}
	p.expect(lexer.LBrace)
	for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
		p.guardProgress(func() {
			switch {
			case p.at(lexer.KwGateway):
				f.Gateways = append(f.Gateways, p.parseGateway(true))
			case p.atFieldName() && p.peek(1).Kind == lexer.Colon:
				field := p.advance().Text
				p.advance() // ':'
				f.Values = append(f.Values, syntax.Arg{Name: field, Value: p.parseExpression()})
			default:
				p.errorf(p.cur().Start, "a fake holds gateways or `name: value` lines (ADR 0091 §4), got %s", p.cur().Kind)
			}
		})
	}
	end := p.expect(lexer.RBrace).End
	f.Span = syntax.Span{Start: start, End: end}
	return f
}

func (p *Parser) parseWorkflowDecl() *syntax.WorkflowDecl {
	start := p.advance().Start // 'workflow'
	name := p.expectName()
	params := p.parseParams()
	p.expect(lexer.Arrow)
	ret := p.parseType()
	body := p.parseBlock()
	return &syntax.WorkflowDecl{Span: syntax.Span{Start: start, End: body.End}, Name: name.Text, Params: params, ReturnType: ret, Body: body}
}

func (p *Parser) parseAdapterDecl() *syntax.AdapterDecl {
	start := p.advance().Start // 'adapter'
	var kind syntax.AdapterKind
	switch p.cur().Kind {
	case lexer.KwHttp:
		p.advance()
		kind = syntax.AdapterHTTP
	case lexer.KwCli:
		p.advance()
		kind = syntax.AdapterCLI
	default:
		// `stream`, `schedule`, and `startup` are contextual (ADR 0111 §3,
		// ADR 0112 §1, ADR 0124 §1).
		switch {
		case p.atContextual("stream"):
			p.advance()
			kind = syntax.AdapterStream
		case p.atContextual("schedule"):
			p.advance()
			kind = syntax.AdapterSchedule
		case p.atContextual("startup"):
			p.advance()
			kind = syntax.AdapterStartup
		default:
			p.errorf(p.cur().Start, "expected 'http', 'cli', 'stream', 'schedule', or 'startup', got %s", p.cur().Kind)
		}
	}
	name := p.expectName()
	p.expect(lexer.LBrace)
	var principals []*syntax.PrincipalDecl
	var refs []*syntax.PrincipalRef
	var routes []*syntax.Route
	for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
		p.guardProgress(func() {
			// `principal` is contextual: a keyword only at the start of an
			// adapter body line, before a name (ADR 0137 §1). A name and a
			// colon declare one inline; a name alone names a top-level one
			// (ADR 0141 §2).
			if (p.atContextual("wire") || p.atContextual("principal")) && isNameToken(p.peek(1).Kind) {
				if p.peek(2).Kind != lexer.Colon {
					ref := p.parsePrincipalRef()
					if len(routes) > 0 {
						p.errorf(ref.Start, "principal %s comes after a route; an adapter names its principals first (ADR 0141 §2)", ref.Name())
					}
					refs = append(refs, ref)
					return
				}
				pr := p.parsePrincipal()
				if len(routes) > 0 {
					p.errorf(pr.Start, "principal %s comes after a route; an adapter declares its principals first (ADR 0137 §1)", pr.Name)
				}
				principals = append(principals, pr)
				return
			}
			routes = append(routes, p.parseRoute(kind))
		})
	}
	end := p.expect(lexer.RBrace).End
	return &syntax.AdapterDecl{Span: syntax.Span{Start: start, End: end}, Kind: kind, Name: name.Text, Principals: principals, PrincipalRefs: refs, Routes: routes}
}

// parsePrincipalRef parses `principal name`, `principal alias.name`, and
// either with `as other` (ADR 0141 §2).
func (p *Parser) parsePrincipalRef() *syntax.PrincipalRef {
	kw := p.advance() // wire, or principal before ADR 0143
	start := kw.Start
	ref := &syntax.PrincipalRef{Keyword: kw.Text, Path: []string{p.expectName().Text}}
	for p.at(lexer.Dot) {
		p.advance()
		ref.Path = append(ref.Path, p.expectName().Text)
	}
	if p.at(lexer.KwAs) {
		p.advance()
		ref.As = p.expectName().Text
	}
	ref.Span = syntax.Span{Start: start, End: p.cur().Start}
	return ref
}

// parsePrincipal parses `principal name: Type from (header|cookie) "name"
// by binder else refusal`, or `from earlier` for a principal settled from
// another (ADR 0137 §1, §2). `by` is contextual; `else` is a keyword.
func (p *Parser) parsePrincipal() *syntax.PrincipalDecl {
	kw := p.advance() // wire, or principal before ADR 0143
	start := kw.Start
	pr := &syntax.PrincipalDecl{Keyword: kw.Text, Name: p.expectName().Text}
	p.expect(lexer.Colon)
	pr.Type = p.parseType()
	if p.atContextual("from") {
		p.advance()
		switch {
		case p.atContextual("header"), p.atContextual("cookie"):
			pr.Source = p.advance().Text
			lit := p.parseStringLit()
			pr.SourceName = plainText(lit)
			if hasInterp(lit) {
				p.errorf(lit.Start, "interpolation is not allowed in a %s name", pr.Source)
			}
		case (p.atContextual("env") || p.atContextual("setting") || p.atContextual("startup")) && p.peek(1).Kind == lexer.StringStart:
			// ADR 0143 §3: a process or run source is a field of a group.
			source := p.advance().Text
			p.parseStringLit()
			p.errorf(pr.Start, "[U10] wire %s reads %s, which is a field of a grouped wire: `wire Name { %s: Type from %s \"...\" }`; a single wire is read from a cookie, a header, or another wire (ADR 0143 §3)", pr.Name, source, pr.Name, source)
			for !p.at(lexer.EOF) && p.cur().Start.Line == start.Line {
				p.advance()
			}
			pr.Span = syntax.Span{Start: start, End: p.cur().Start}
			return pr
		default:
			tok := p.expectName()
			pr.From = &syntax.Ident{Span: syntax.Span{Start: tok.Start, End: tok.End}, Name: tok.Text}
		}
		p.parseWireScheme(pr)
	} else {
		p.errorf(p.cur().Start, "expected 'from' after principal %s's type: from cookie \"name\", from header \"Name\", or from an earlier principal (ADR 0137 §1)", pr.Name)
	}
	if p.atContextual("by") {
		p.advance()
		pr.By = p.parseQualifiedRef()
	} else {
		p.errorf(p.cur().Start, "expected 'by' and the workflow that settles principal %s (ADR 0137 §1)", pr.Name)
	}
	if p.at(lexer.KwElse) {
		p.advance()
		pr.Else = p.parseQualifiedRef()
	} else {
		p.errorf(p.cur().Start, "expected 'else' and the conversion that answers when principal %s is refused (ADR 0137 §1)", pr.Name)
	}
	pr.Span = syntax.Span{Start: start, End: p.cur().Start}
	return pr
}

// parseWireScheme parses `scheme "Bearer"` after a header source (ADR
// 0144 §1). Anywhere else, or naming another scheme, it is U11.
func (p *Parser) parseWireScheme(pr *syntax.PrincipalDecl) {
	if !p.atContextual("scheme") || p.peek(1).Kind != lexer.StringStart {
		return
	}
	at := p.advance().Start
	lit := p.parseStringLit()
	scheme := plainText(lit)
	switch {
	case pr.Source != "header":
		source := "another wire"
		if pr.Source != "" {
			source = pr.Source + " " + strconv.Quote(pr.SourceName)
		}
		p.errorf(at, "[U11] wire %s reads %s, and only a header's value carries an auth scheme: `from header \"Authorization\" scheme \"Bearer\"` (ADR 0144 §1)", pr.Name, source)
	case hasInterp(lit) || scheme != "Bearer":
		p.errorf(lit.Start, "[U11] wire %s names scheme %q; the one scheme a wire reads is \"Bearer\" (ADR 0144 §4)", pr.Name, scheme)
	default:
		pr.Scheme = scheme
	}
}

// parseQualifiedRef parses `name` or `alias.name`.
func (p *Parser) parseQualifiedRef() *syntax.QualifiedRef {
	first := p.expectName()
	ref := &syntax.QualifiedRef{Path: []string{first.Text}}
	end := first.End
	for p.at(lexer.Dot) {
		p.advance()
		next := p.expectName()
		ref.Path = append(ref.Path, next.Text)
		end = next.End
	}
	ref.Span = syntax.Span{Start: first.Start, End: end}
	return ref
}

func (p *Parser) parseRoute(kind syntax.AdapterKind) *syntax.Route {
	start := p.cur().Start
	var verb string
	if kind == syntax.AdapterStream {
		return p.parseStreamRoute(start)
	}
	if kind == syntax.AdapterSchedule {
		return p.parseScheduleRoute(start)
	}
	if kind == syntax.AdapterStartup {
		return p.parseStartupRoute(start)
	}
	if kind == syntax.AdapterCLI {
		p.expect(lexer.KwCommand)
		verb = "command"
	} else {
		// HTTP methods are validated against ADR 0021's allow-list by the
		// checker, not here — accept any identifier rather than hardcoding
		// one verb.
		verb = p.expectName().Text
	}
	path := p.parsePlainString()
	route := &syntax.Route{Verb: verb, Path: path}
	// `public` is contextual after an http route's path (ADR 0137 §3).
	if kind == syntax.AdapterHTTP && p.atContextual("public") {
		p.advance()
		route.Public = true
	}
	p.expect(lexer.Arrow)
	nameTok := p.expectName()
	route.Workflow = &syntax.Ident{Span: syntax.Span{Start: nameTok.Start, End: nameTok.End}, Name: nameTok.Text}
	route.Args = p.parseRouteArgs()
	// `consumes` and `produces` are contextual (ADR 0069 §5), in either
	// order; the checker reports a repeated or misplaced clause.
	for p.atContextual("consumes") || p.atContextual("produces") {
		word := p.advance()
		lit := p.parseStringLit()
		mt := &syntax.MediaType{Span: syntax.Span{Start: word.Start, End: lit.End}, Text: plainText(lit)}
		if hasInterp(lit) {
			p.errorf(lit.Start, "interpolation is not allowed in a media type")
		}
		if word.Text == "consumes" {
			if route.Consumes != nil {
				p.errorf(word.Start, "route has two consumes clauses")
			}
			route.Consumes = mt
		} else {
			if route.Produces != nil {
				p.errorf(word.Start, "route has two produces clauses")
			}
			route.Produces = mt
		}
	}
	if p.at(lexer.LBrace) {
		route.Mapping = p.parseResponseMapping()
	}
	route.Span = syntax.Span{Start: start, End: p.cur().Start}
	return route
}

// parseStreamRoute parses `source "name" [key field] -> workflow(arg:
// Type) { pattern -> disposition ... }` (ADR 0111 §3). `source` and `key`
// are contextual.
func (p *Parser) parseStreamRoute(start lexer.Pos) *syntax.Route {
	if !p.atContextual("source") {
		p.errorf(p.cur().Start, "expected 'source' to start a stream route, got %s", p.cur().Kind)
	} else {
		p.advance()
	}
	lit := p.parseStringLit()
	if hasInterp(lit) {
		p.errorf(lit.Start, "interpolation is not allowed in a source name")
	}
	route := &syntax.Route{Verb: "source", Path: plainText(lit)}
	// `group "name"` and `from earliest|latest` (ADR 0111 §3a); the checker
	// reports either missing (I8).
	if p.atContextual("group") {
		p.advance()
		g := p.parseStringLit()
		if hasInterp(g) {
			p.errorf(g.Start, "interpolation is not allowed in a group name")
		}
		route.Group = plainText(g)
	}
	if p.atContextual("from") {
		p.advance()
		if p.atContextual("earliest") || p.atContextual("latest") {
			route.From = p.advance().Text
		} else {
			p.errorf(p.cur().Start, "expected 'earliest' or 'latest' after 'from', got %s", p.cur().Kind)
		}
	}
	if p.atContextual("key") {
		p.advance()
		keyTok := p.expectName()
		route.Key = &syntax.Ident{Span: syntax.Span{Start: keyTok.Start, End: keyTok.End}, Name: keyTok.Text}
	}
	p.expect(lexer.Arrow)
	nameTok := p.expectName()
	route.Workflow = &syntax.Ident{Span: syntax.Span{Start: nameTok.Start, End: nameTok.End}, Name: nameTok.Text}
	route.Args = p.parseRouteArgs()
	if p.at(lexer.LBrace) {
		p.expect(lexer.LBrace)
		route.Mapping = []syntax.ResponseArm{}
		for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
			p.guardProgress(func() {
				armStart := p.cur().Start
				pat := p.parsePattern()
				p.expect(lexer.Arrow)
				val := p.parseDisposition(true)
				route.Mapping = append(route.Mapping, syntax.ResponseArm{Span: syntax.Span{Start: armStart, End: p.cur().Start}, Pattern: pat, Value: val})
			})
		}
		p.expect(lexer.RBrace)
	}
	route.Span = syntax.Span{Start: start, End: p.cur().Start}
	return route
}

// parseScheduleRoute parses `every "15m" -> workflow(...) { pattern ->
// done | halt ... }` or the same with `cron "0 3 * * *"` (ADR 0112 §1).
// `every`, `cron`, and `done` are contextual.
func (p *Parser) parseScheduleRoute(start lexer.Pos) *syntax.Route {
	verb := ""
	if p.atContextual("every") || p.atContextual("cron") {
		verb = p.advance().Text
	} else {
		p.errorf(p.cur().Start, "expected 'every' or 'cron' to start a schedule route, got %s", p.cur().Kind)
	}
	lit := p.parseStringLit()
	if hasInterp(lit) {
		p.errorf(lit.Start, "interpolation is not allowed in a schedule")
	}
	route := &syntax.Route{Verb: verb, Path: plainText(lit)}
	p.expect(lexer.Arrow)
	nameTok := p.expectName()
	route.Workflow = &syntax.Ident{Span: syntax.Span{Start: nameTok.Start, End: nameTok.End}, Name: nameTok.Text}
	route.Args = p.parseRouteArgs()
	if p.at(lexer.LBrace) {
		p.expect(lexer.LBrace)
		route.Mapping = []syntax.ResponseArm{}
		for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
			p.guardProgress(func() {
				armStart := p.cur().Start
				pat := p.parsePattern()
				p.expect(lexer.Arrow)
				dStart := p.cur().Start
				d := &syntax.DispositionResponse{}
				if p.atContextual("done") || p.atContextual("halt") {
					d.Kind = p.advance().Text
				} else {
					p.errorf(p.cur().Start, "expected 'done' or 'halt' in a schedule route's mapping, got %s", p.cur().Kind)
					p.advance()
				}
				d.Span = syntax.Span{Start: dStart, End: p.cur().Start}
				route.Mapping = append(route.Mapping, syntax.ResponseArm{Span: syntax.Span{Start: armStart, End: p.cur().Start}, Pattern: pat, Value: d})
			})
		}
		p.expect(lexer.RBrace)
	}
	route.Span = syntax.Span{Start: start, End: p.cur().Start}
	return route
}

// parseStartupRoute parses `run -> workflow() { pattern -> proceed | halt
// ... }` (ADR 0124 §1). `run` and `proceed` are contextual.
func (p *Parser) parseStartupRoute(start lexer.Pos) *syntax.Route {
	if !p.atContextual("run") {
		p.errorf(p.cur().Start, "expected 'run' to start a startup route, got %s", p.cur().Kind)
	} else {
		p.advance()
	}
	route := &syntax.Route{Verb: "run"}
	// `once` and `within "10s"`, in that order (ADR 0124 §6); both are
	// contextual, and the checker reports a duration that isn't one (I10).
	if p.atContextual("once") {
		p.advance()
		route.Once = true
	}
	if p.atContextual("within") {
		p.advance()
		lit := p.parseStringLit()
		if hasInterp(lit) {
			p.errorf(lit.Start, "interpolation is not allowed in a deadline")
		}
		route.Within = plainText(lit)
	}
	p.expect(lexer.Arrow)
	nameTok := p.expectName()
	route.Workflow = &syntax.Ident{Span: syntax.Span{Start: nameTok.Start, End: nameTok.End}, Name: nameTok.Text}
	route.Args = p.parseRouteArgs()
	if p.at(lexer.LBrace) {
		p.expect(lexer.LBrace)
		route.Mapping = []syntax.ResponseArm{}
		for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
			p.guardProgress(func() {
				armStart := p.cur().Start
				pat := p.parsePattern()
				p.expect(lexer.Arrow)
				dStart := p.cur().Start
				d := &syntax.DispositionResponse{}
				if p.atContextual("proceed") || p.atContextual("degrade") || p.atContextual("halt") {
					d.Kind = p.advance().Text
				} else {
					p.errorf(p.cur().Start, "expected 'proceed', 'degrade' or 'halt' in a startup route's mapping, got %s", p.cur().Kind)
					p.advance()
				}
				d.Span = syntax.Span{Start: dStart, End: p.cur().Start}
				route.Mapping = append(route.Mapping, syntax.ResponseArm{Span: syntax.Span{Start: armStart, End: p.cur().Start}, Pattern: pat, Value: d})
			})
		}
		p.expect(lexer.RBrace)
	}
	route.Span = syntax.Span{Start: start, End: p.cur().Start}
	return route
}

// parseDisposition parses `ack`, `halt`, `deadLetter "target"`, or, when
// retry is allowed, `retry(attempts: N) [else halt | else deadLetter
// "target"]` (ADR 0111 §3).
func (p *Parser) parseDisposition(retry bool) *syntax.DispositionResponse {
	start := p.cur().Start
	d := &syntax.DispositionResponse{}
	switch {
	case p.atContextual("ack"), p.atContextual("halt"):
		d.Kind = p.advance().Text
	case p.atContextual("deadLetter"):
		p.advance()
		d.Kind = "deadLetter"
		lit := p.parseStringLit()
		if hasInterp(lit) {
			p.errorf(lit.Start, "interpolation is not allowed in a dead-letter target")
		}
		d.Target = plainText(lit)
	case retry && p.at(lexer.KwRetry):
		p.advance()
		d.Kind = "retry"
		p.expect(lexer.LParen)
		p.expect(lexer.KwAttempts)
		p.expect(lexer.Colon)
		n := p.expect(lexer.Int)
		d.Attempts, _ = strconv.Atoi(n.Text)
		p.expect(lexer.RParen)
		if p.at(lexer.KwElse) {
			p.advance()
			d.Else = p.parseDisposition(false)
			if d.Else.Kind == "ack" {
				p.errorf(d.Else.Start, "a retry that runs out of attempts can't ack; write else halt or else deadLetter \"target\"")
			}
		}
	default:
		want := "'ack', 'retry', 'halt', or 'deadLetter'"
		if !retry {
			want = "'halt' or 'deadLetter'"
		}
		p.errorf(p.cur().Start, "expected %s in a stream route's mapping, got %s", want, p.cur().Kind)
		p.advance()
	}
	d.Span = syntax.Span{Start: start, End: p.cur().Start}
	return d
}

// parseRouteArgs parses a route's argument list: `name: Type`, optionally
// followed by `from header "Name"` or `from cookie "name"` (ADR 0069 §1).
// `from`, `header`, and `cookie` are contextual here only.
func (p *Parser) parseRouteArgs() []syntax.RouteArg {
	p.expect(lexer.LParen)
	var args []syntax.RouteArg
	for !p.at(lexer.RParen) && !p.at(lexer.EOF) {
		start := p.cur().Start
		name := p.expectName()
		if p.at(lexer.Comma) || p.at(lexer.RParen) {
			// A bare name passes the adapter's principal (ADR 0137 §1).
			args = append(args, syntax.RouteArg{Span: syntax.Span{Start: start, End: name.End}, Name: name.Text, Principal: true})
			if !p.at(lexer.Comma) {
				break
			}
			p.advance()
			continue
		}
		p.expect(lexer.Colon)
		arg := syntax.RouteArg{Name: name.Text, Type: p.parseType()}
		if p.atContextual("from") {
			p.advance()
			switch {
			case p.atContextual("header"), p.atContextual("cookie"):
				arg.Source = p.advance().Text
			default:
				p.errorf(p.cur().Start, "expected 'header' or 'cookie' after 'from', got %s", p.cur().Kind)
			}
			lit := p.parseStringLit()
			arg.SourceName = plainText(lit)
			if hasInterp(lit) {
				p.errorf(lit.Start, "interpolation is not allowed in a %s name", arg.Source)
			}
		}
		arg.Span = syntax.Span{Start: start, End: p.cur().Start}
		args = append(args, arg)
		if !p.at(lexer.Comma) {
			break
		}
		p.advance()
	}
	p.expect(lexer.RParen)
	return args
}

// atContextual reports whether the current token is the identifier word,
// a keyword only where the caller expects it.
func (p *Parser) atContextual(word string) bool {
	return p.at(lexer.Ident) && p.cur().Text == word
}

// plainText is a string literal's text parts, ignoring interpolations.
func plainText(lit *syntax.StringLit) string {
	s := ""
	for _, part := range lit.Parts {
		if part.Interp == nil {
			s += part.Text
		}
	}
	return s
}

// hasInterp reports whether a string literal interpolates.
func hasInterp(lit *syntax.StringLit) bool {
	for _, part := range lit.Parts {
		if part.Interp != nil {
			return true
		}
	}
	return false
}

// parsePlainString parses a string literal that must contain no
// interpolation — used for route paths, which are templates, not runtime
// text.
func (p *Parser) parsePlainString() string {
	lit := p.parseStringLit()
	s := ""
	for _, part := range lit.Parts {
		if part.Interp != nil {
			p.errorf(lit.Start, "interpolation is not allowed in a route path")
			continue
		}
		s += part.Text
	}
	return s
}

func (p *Parser) parseResponseMapping() []syntax.ResponseArm {
	p.expect(lexer.LBrace)
	var arms []syntax.ResponseArm
	for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
		p.guardProgress(func() { arms = append(arms, p.parseResponseArm()) })
	}
	p.expect(lexer.RBrace)
	return arms
}

func (p *Parser) parseResponseArm() syntax.ResponseArm {
	start := p.cur().Start
	pat := p.parsePattern()
	p.expect(lexer.Arrow)
	val := p.parseResponseValue()
	return syntax.ResponseArm{Span: syntax.Span{Start: start, End: p.cur().Start}, Pattern: pat, Value: val}
}

func (p *Parser) parseResponseValue() syntax.ResponseValue {
	start := p.cur().Start
	switch {
	case p.at(lexer.KwExit):
		p.advance()
		codeTok := p.expect(lexer.Int)
		code := &syntax.IntLit{Span: syntax.Span{Start: codeTok.Start, End: codeTok.End}, Text: codeTok.Text}
		var body syntax.Expr
		if !p.atResponseArmBoundary() {
			body = p.parseExpression()
		}
		return &syntax.ExitResponse{Span: syntax.Span{Start: start, End: p.cur().Start}, Code: code, Body: body}
	case p.at(lexer.Int):
		codeTok := p.advance()
		code := &syntax.IntLit{Span: syntax.Span{Start: codeTok.Start, End: codeTok.End}, Text: codeTok.Text}
		var body syntax.Expr
		if !p.atResponseArmBoundary() {
			body = p.parseExpression()
		}
		return &syntax.StatusResponse{Span: syntax.Span{Start: start, End: p.cur().Start}, Code: code, Body: body}
	default:
		val := p.parseExpression()
		return &syntax.ExprResponse{Span: syntax.Span{Start: start, End: p.cur().Start}, Value: val}
	}
}

// atResponseArmBoundary decides whether a status/exit response value's
// optional trailing body expression is absent: either the mapping block is
// ending, or what follows looks like the *next* arm's pattern (a qualified
// name, optionally called, followed by '->'). This lookahead exists
// because `200 customer` and `404` are only distinguishable by scanning
// ahead — flagged as a rough edge in grammar.md's response-mapping notes.
func (p *Parser) atResponseArmBoundary() bool {
	return p.at(lexer.RBrace) || p.at(lexer.EOF) || p.looksLikeArmStart()
}

func (p *Parser) looksLikeArmStart() bool {
	save := p.pos
	defer func() { p.pos = save }()

	if p.at(lexer.Underscore) {
		p.advance()
	} else if p.atName() {
		p.advance()
		for p.at(lexer.Dot) {
			p.advance()
			if !p.atName() {
				return false
			}
			p.advance()
		}
		if p.at(lexer.LParen) {
			depth := 0
			for {
				if p.at(lexer.EOF) {
					return false
				}
				if p.at(lexer.LParen) {
					depth++
					p.advance()
					continue
				}
				if p.at(lexer.RParen) {
					depth--
					p.advance()
					if depth == 0 {
						break
					}
					continue
				}
				p.advance()
			}
		}
	} else {
		return false
	}
	return p.at(lexer.Arrow)
}

// ---- types ---------------------------------------------------------------

func (p *Parser) parseType() syntax.Type {
	start := p.cur().Start
	first := p.parseNamedType()
	if !p.at(lexer.Pipe) {
		return first
	}
	members := []syntax.Type{first}
	for p.at(lexer.Pipe) {
		p.advance()
		members = append(members, p.parseNamedType())
	}
	return &syntax.UnionType{Span: syntax.Span{Start: start, End: p.cur().Start}, Members: members}
}

func (p *Parser) parseNamedType() syntax.Type {
	if p.at(lexer.LBrace) {
		return p.parseRecordType()
	}
	start := p.cur().Start
	path := p.parseQualifiedName()
	var args []syntax.Type
	if p.at(lexer.LAngle) {
		p.advance()
		args = append(args, p.parseType())
		for p.at(lexer.Comma) {
			p.advance()
			args = append(args, p.parseType())
		}
		p.expect(lexer.RAngle)
	}
	return &syntax.QualifiedType{Span: syntax.Span{Start: start, End: p.cur().Start}, Path: path, Args: args}
}

func (p *Parser) parseRecordType() *syntax.RecordType {
	start := p.expect(lexer.LBrace).Start
	var fields []syntax.Field
	if !p.at(lexer.RBrace) {
		fields = append(fields, p.parseField(false))
		for p.at(lexer.Comma) {
			p.advance()
			fields = append(fields, p.parseField(false))
		}
	}
	end := p.expect(lexer.RBrace).End
	return &syntax.RecordType{Span: syntax.Span{Start: start, End: end}, Fields: fields}
}

func (p *Parser) parseQualifiedName() []string {
	first := p.expectName()
	path := []string{first.Text}
	for p.at(lexer.Dot) {
		p.advance()
		seg := p.expectName()
		path = append(path, seg.Text)
	}
	return path
}

// ---- statements ------------------------------------------------------

func (p *Parser) parseBlock() *syntax.Block {
	start := p.expect(lexer.LBrace).Start
	var stmts []syntax.Stmt
	for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
		before := p.pos
		stmts = append(stmts, p.parseStatement())
		if p.pos == before {
			p.errorf(p.cur().Start, "unexpected token %s in statement", p.cur().Kind)
			p.advance()
		}
		// ADR 0142 §1: a statement ends with `;`. Until the old form is
		// retired (§5), it may also end without one.
		if p.at(lexer.Semicolon) {
			p.advance()
		}
	}
	end := p.expect(lexer.RBrace).End
	return &syntax.Block{Span: syntax.Span{Start: start, End: end}, Stmts: stmts}
}

func (p *Parser) parseStatement() syntax.Stmt {
	switch {
	case p.at(lexer.KwIf):
		return p.parseIfStmt()
	case p.at(lexer.KwReturn):
		return p.parseReturnStmt()
	case p.at(lexer.KwMatch):
		return p.parseMatchStmt()
	case p.at(lexer.KwTransaction):
		return p.parseTransactionStmt()
	case p.at(lexer.KwFor):
		return p.parseForStmt("", p.cur().Start)
	case p.at(lexer.KwWhile):
		return p.parseWhileStmt("", p.cur().Start)
	case p.at(lexer.KwBreak):
		return p.parseBreakStmt()
	case p.at(lexer.KwContinue):
		return p.parseContinueStmt()
	case p.atExpect():
		start := p.advance().Start // 'expect'
		cond := p.parseDefault()   // not parseExpression, which refuses a trailing `is`
		var pat syntax.Pattern
		if p.atContextual("is") && p.cur().Start.Line == p.peek(-1).End.Line {
			p.advance() // 'is'
			pat = p.parsePattern()
		}
		return &syntax.ExpectStmt{Span: syntax.Span{Start: start, End: p.cur().Start}, Cond: cond, Pattern: pat}
	case p.atName() && p.peek(1).Kind == lexer.Colon && p.peek(2).Kind == lexer.KwFor:
		labelTok := p.advance()
		p.advance() // ':'
		return p.parseForStmt(labelTok.Text, labelTok.Start)
	case p.atName() && p.peek(1).Kind == lexer.Colon && p.peek(2).Kind == lexer.KwWhile:
		labelTok := p.advance()
		p.advance() // ':'
		return p.parseWhileStmt(labelTok.Text, labelTok.Start)
	case p.atName() && p.peek(1).Kind == lexer.Colon:
		return p.parseTypedBinding()
	case p.atName() && p.peek(1).Kind == lexer.Equals:
		return p.parseBinding()
	default:
		start := p.cur().Start
		expr := p.parseExpression()
		return &syntax.ExprStmt{Span: syntax.Span{Start: start, End: p.cur().Start}, Value: expr}
	}
}

// atStatementOnly reports whether a statement that is never an
// expression starts here, and its first word.
func (p *Parser) atStatementOnly() (string, bool) {
	switch {
	case p.at(lexer.KwReturn), p.at(lexer.KwFor), p.at(lexer.KwWhile), p.at(lexer.KwBreak),
		p.at(lexer.KwContinue), p.at(lexer.KwTransaction), p.atExpect():
		return p.cur().Text, true
	case p.atName() && (p.peek(1).Kind == lexer.Equals || p.peek(1).Kind == lexer.Colon):
		return p.cur().Text + " =", true
	}
	return "", false
}

// atExpect reports whether an `expect` statement (ADR 0091 §3) starts
// here: the word `expect` with an expression after it on the same line.
// `expect = ...`, `expect: T = ...`, and `expect.x` keep their ordinary
// meanings.
func (p *Parser) atExpect() bool {
	if !p.atContextual("expect") {
		return false
	}
	next := p.peek(1)
	if next.Start.Line != p.cur().Start.Line {
		return false
	}
	switch next.Kind {
	case lexer.Equals, lexer.Colon, lexer.Dot, lexer.RBrace, lexer.EOF:
		return false
	}
	return true
}

func (p *Parser) parseBinding() *syntax.Binding {
	nameTok := p.advance()
	p.expect(lexer.Equals)
	val := p.parseValue()
	return &syntax.Binding{Span: syntax.Span{Start: nameTok.Start, End: p.cur().Start}, Name: nameTok.Text, Value: val}
}

// parseTypedBinding is `name: Type = expr`, which declares a reassignable
// local (ADR 0053).
func (p *Parser) parseTypedBinding() *syntax.Binding {
	nameTok := p.advance()
	p.expect(lexer.Colon)
	typ := p.parseType()
	p.expect(lexer.Equals)
	val := p.parseValue()
	return &syntax.Binding{Span: syntax.Span{Start: nameTok.Start, End: p.cur().Start}, Name: nameTok.Text, Type: typ, Value: val}
}

// parseForStmt is `for x in xs { ... }`, with an optional label already
// consumed by the caller (ADR 0053). start is where the statement begins,
// which is the label when there is one.
func (p *Parser) parseForStmt(label string, start lexer.Pos) *syntax.ForStmt {
	p.expect(lexer.KwFor)
	// ADR 0142 §4: the head is parenthesised, `for (x in xs)`. Until the
	// old form is retired (§5), it may also be bare.
	parens := p.at(lexer.LParen)
	if parens {
		p.advance()
	}
	name := p.loopName()
	value := ""
	if p.at(lexer.Comma) { // `for key, value in m` (ADR 0065 §7)
		p.advance()
		value = p.loopName()
	}
	p.expect(lexer.KwIn)
	iterable := p.parseExpression()
	if parens {
		p.expect(lexer.RParen)
	}
	body := p.parseBlock()
	return &syntax.ForStmt{Span: syntax.Span{Start: start, End: body.End}, Label: label, Var: name, Value: value, Iterable: iterable, Body: body}
}

// loopName is a for loop's name: a name, or `_` to bind nothing (ADR 0065
// §7).
func (p *Parser) loopName() string {
	if p.at(lexer.Underscore) {
		p.advance()
		return "_"
	}
	return p.expectName().Text
}

// parseWhileStmt is `while cond { ... }`, with an optional label already
// consumed by the caller (ADR 0059).
func (p *Parser) parseWhileStmt(label string, start lexer.Pos) *syntax.WhileStmt {
	p.expect(lexer.KwWhile)
	cond := p.parseExpression()
	body := p.parseBlock()
	return &syntax.WhileStmt{Span: syntax.Span{Start: start, End: body.End}, Label: label, Cond: cond, Body: body}
}

// optionalJumpLabel reads the optional label after break/continue. The label
// must sit on the same line as the keyword: `break` alone on a line followed
// by a statement on the next line must not swallow that statement's name.
func (p *Parser) optionalJumpLabel(kw lexer.Token) string {
	if p.atName() && p.cur().Start.Line == kw.Start.Line {
		return p.advance().Text
	}
	return ""
}

func (p *Parser) parseBreakStmt() *syntax.BreakStmt {
	kw := p.advance()
	label := p.optionalJumpLabel(kw)
	return &syntax.BreakStmt{Span: syntax.Span{Start: kw.Start, End: p.cur().Start}, Label: label}
}

func (p *Parser) parseContinueStmt() *syntax.ContinueStmt {
	kw := p.advance()
	label := p.optionalJumpLabel(kw)
	return &syntax.ContinueStmt{Span: syntax.Span{Start: kw.Start, End: p.cur().Start}, Label: label}
}

func (p *Parser) parseIfStmt() *syntax.IfStmt {
	start := p.advance().Start // 'if'
	cond := p.parseExpression()
	then := p.parseBlock()
	stmt := &syntax.IfStmt{Cond: cond, Then: then}
	for p.at(lexer.KwElse) {
		p.advance()
		if p.at(lexer.KwIf) {
			p.advance()
			c := p.parseExpression()
			b := p.parseBlock()
			stmt.ElseIfs = append(stmt.ElseIfs, syntax.ElseIf{Cond: c, Then: b})
			continue
		}
		stmt.Else = p.parseBlock()
		break
	}
	stmt.Span = syntax.Span{Start: start, End: p.cur().Start}
	return stmt
}

func (p *Parser) parseReturnStmt() *syntax.ReturnStmt {
	start := p.advance().Start // 'return'
	if p.at(lexer.RBrace) || p.at(lexer.Semicolon) {
		return &syntax.ReturnStmt{Span: syntax.Span{Start: start, End: p.cur().Start}}
	}
	val := p.parseValue()
	return &syntax.ReturnStmt{Span: syntax.Span{Start: start, End: p.cur().Start}, Value: val}
}

// parseValue is a `return`'s or a binding's value: an expression, or a
// whole `match` (ADR 0142 §2), whose closing `}` ends the statement.
func (p *Parser) parseValue() syntax.Expr {
	if p.at(lexer.KwMatch) {
		m := p.parseMatchStmt()
		return &syntax.MatchExpr{Span: m.Span, Match: m}
	}
	return p.parseExpression()
}

func (p *Parser) parseMatchStmt() *syntax.MatchStmt {
	start := p.advance().Start // 'match'
	subject := p.parseExpression()
	p.expect(lexer.LBrace)
	var arms []syntax.MatchArm
	for !p.at(lexer.RBrace) && !p.at(lexer.EOF) {
		p.guardProgress(func() { arms = append(arms, p.parseMatchArm()) })
	}
	end := p.expect(lexer.RBrace).End
	return &syntax.MatchStmt{Span: syntax.Span{Start: start, End: end}, Subject: subject, Arms: arms}
}

func (p *Parser) parseMatchArm() syntax.MatchArm {
	start := p.cur().Start
	pat := p.parsePattern()
	p.expect(lexer.Arrow)
	var body syntax.Body
	if word, ok := p.atStatementOnly(); ok {
		// A habit from languages where an arm takes any statement.
		stmtStart := p.cur().Start
		p.errorf(stmtStart, "`%s` is a statement, and an arm takes an expression or a block; wrap it in braces: `-> { %s ... }`", word, word)
		stmt := p.parseStatement()
		body = &syntax.Block{Span: syntax.Span{Start: stmtStart, End: p.cur().Start}, Stmts: []syntax.Stmt{stmt}}
	} else {
		body = p.parseBody()
	}
	return syntax.MatchArm{Span: syntax.Span{Start: start, End: p.cur().Start}, Pattern: pat, Body: body}
}

func (p *Parser) parsePattern() syntax.Pattern {
	start := p.cur().Start
	if p.at(lexer.LShift) {
		segs, span := p.parseSegments(true)
		return &syntax.BytesPattern{Span: span, Segments: segs}
	}
	if p.at(lexer.Underscore) {
		p.advance()
		return &syntax.WildcardPattern{Span: syntax.Span{Start: start, End: p.cur().Start}}
	}
	switch p.cur().Kind {
	case lexer.Int:
		return p.parseIntPattern(start, false)
	case lexer.Minus:
		if p.peek(1).Kind == lexer.Int {
			p.advance()
			return p.parseIntPattern(start, true)
		}
	case lexer.Float:
		// ADR 0130: a float or decimal has no literal pattern, since two
		// numbers that should agree can differ in the last bit (ADR 0048), and
		// a decimal's scale is part of what it prints as (ADR 0064).
		text := p.advance().Text
		p.errorf(start, "a pattern can't hold a float or decimal literal (%s); bind a name and compare it, with math.closeTo for a float (ADR 0130)", text)
		return &syntax.NamePattern{Span: syntax.Span{Start: start, End: p.cur().Start}, Path: []string{syntax.ErrorName}}
	case lexer.StringStart:
		lit := p.parseStringLit()
		if hasInterp(lit) {
			p.errorf(start, "a string pattern can't interpolate; it matches the text it is written as (ADR 0130)")
			return &syntax.NamePattern{Span: syntax.Span{Start: start, End: p.cur().Start}, Path: []string{syntax.ErrorName}}
		}
		return &syntax.LiteralPattern{Span: syntax.Span{Start: start, End: p.cur().Start}, Kind: "string", Str: plainText(lit), Lit: lit}
	case lexer.Ident:
		// `true` and `false` are names to the lexer; as a pattern they are the
		// values, not a binding that always matches.
		if name := p.cur().Text; (name == "true" || name == "false") && p.peek(1).Kind != lexer.Dot && p.peek(1).Kind != lexer.LParen {
			p.advance()
			return &syntax.LiteralPattern{Span: syntax.Span{Start: start, End: p.cur().Start}, Kind: "bool", Bool: name == "true"}
		}
	}
	path := p.parseQualifiedName()
	if p.at(lexer.LParen) {
		p.advance()
		var args []syntax.Pattern
		if !p.at(lexer.RParen) {
			args = append(args, p.parsePattern())
			for p.at(lexer.Comma) {
				p.advance()
				args = append(args, p.parsePattern())
			}
		}
		end := p.expect(lexer.RParen).End
		return &syntax.ConstructorPattern{Span: syntax.Span{Start: start, End: end}, Path: path, Args: args}
	}
	return &syntax.NamePattern{Span: syntax.Span{Start: start, End: p.cur().Start}, Path: path}
}

// parseIntPattern is an int literal in a pattern, after its minus sign when
// neg: decimal or hex, as in an expression (ADR 0130).
func (p *Parser) parseIntPattern(start lexer.Pos, neg bool) syntax.Pattern {
	tok := p.expect(lexer.Int)
	n, err := syntax.IntValue(tok.Text)
	if err != nil {
		p.errorf(start, "the int pattern %s is out of range", tok.Text)
	}
	if neg {
		n = -n
	}
	return &syntax.LiteralPattern{Span: syntax.Span{Start: start, End: p.cur().Start}, Kind: "int", Int: n}
}

func (p *Parser) parseTransactionStmt() *syntax.TransactionStmt {
	start := p.advance().Start // 'transaction'
	cap_ := p.expectName()
	s := &syntax.TransactionStmt{Capability: cap_.Text}
	// ADR 0088 §1: an isolation level, then retry(attempts: N), each
	// optional; the levels are contextual words.
	if p.atContextual("serializable") || p.atContextual("repeatableRead") {
		s.Isolation = p.advance().Text
	}
	if p.at(lexer.KwRetry) {
		p.advance()
		p.expect(lexer.LParen)
		p.expect(lexer.KwAttempts)
		p.expect(lexer.Colon)
		countTok := p.expect(lexer.Int)
		p.expect(lexer.RParen)
		s.AttemptsPos = countTok.Start
		if n, err := syntax.IntValue(countTok.Text); err == nil {
			s.Attempts = n
		}
	}
	s.Body = p.parseBlock()
	s.Span = syntax.Span{Start: start, End: s.Body.End}
	return s
}

// parseBody parses the `( expression | block )` alternative shared by if
// (already inlined above since if always needs a block, not this
// alternative), match arms, concurrent, and retry.
func (p *Parser) parseBody() syntax.Body {
	if p.at(lexer.LBrace) {
		return p.parseBlock()
	}
	return syntax.ExprBody{Expr: p.parseExpression()}
}

// ---- expressions -----------------------------------------------------

func (p *Parser) parseExpression() syntax.Expr {
	e := p.parseDefault()
	if p.atContextual("is") && p.sameLine() {
		// abs-recommendations GAPS G20: `is` reads like a boolean test,
		// but only `expect` takes a pattern this way.
		p.errorf(p.cur().Start, "`is` takes a pattern only in `expect x is Pattern`; elsewhere, match on the value, or write a helper that does")
		p.advance()
		if p.at(lexer.KwNot) {
			p.advance()
		}
		p.parsePattern()
		// The whole test stands for nothing the checker should judge, so no
		// second error follows the first (`a count body must be bool`).
		start, _ := exprBounds(e)
		return &syntax.Ident{Span: syntax.Span{Start: start, End: p.cur().Start}, Name: syntax.ErrorName}
	}
	return e
}

// binaryExpr builds a BinaryExpr spanning both operands, so tooling that
// asks for its position (the formatter's trailing-comment placement,
// checker diagnostics) gets the real line instead of 0.
func binaryExpr(op syntax.BinaryOp, left, right syntax.Expr) *syntax.BinaryExpr {
	start, _ := exprBounds(left)
	_, end := exprBounds(right)
	return &syntax.BinaryExpr{Span: syntax.Span{Start: start, End: end}, Op: op, Left: left, Right: right}
}

// exprBounds is e's span, or zero positions for an expression error
// recovery left nil.
func exprBounds(e syntax.Expr) (lexer.Pos, lexer.Pos) {
	if sp, ok := e.(syntax.Spanned); ok {
		return sp.Bounds()
	}
	return lexer.Pos{}, lexer.Pos{}
}

// parseDefault is `x ?? d` (ADR 0142 §3), the loosest operator. Mixing it
// with `&&` or `||` needs parentheses, as in JavaScript, because which
// grouping was meant is never obvious.
func (p *Parser) parseDefault() syntax.Expr {
	left := p.parseOr()
	for p.at(lexer.QuestionQuestion) {
		op := p.advance()
		p.refuseUngroupedLogic(left, op)
		right := p.parseOr()
		p.refuseUngroupedLogic(right, op)
		left = binaryExpr(syntax.OpDefault, left, right)
	}
	return left
}

// refuseUngroupedLogic reports an `&&` or `||` operand of `??` that isn't
// in parentheses.
func (p *Parser) refuseUngroupedLogic(e syntax.Expr, op lexer.Token) {
	if b, ok := e.(*syntax.BinaryExpr); ok && (b.Op == syntax.OpAnd || b.Op == syntax.OpOr) {
		p.errorf(op.Start, "`??` beside `%s` needs parentheses to say which comes first (ADR 0142 §3)", logicSpelling(b))
	}
}

func logicSpelling(b *syntax.BinaryExpr) string {
	switch {
	case b.Op == syntax.OpAnd && b.Symbol:
		return "&&"
	case b.Op == syntax.OpOr && b.Symbol:
		return "||"
	}
	return string(b.Op)
}

// parseOr is `||`, or the older `or` (ADR 0142 §3).
func (p *Parser) parseOr() syntax.Expr {
	left := p.parseAnd()
	for p.at(lexer.KwOr) || p.at(lexer.OrOr) {
		symbol := p.advance().Kind == lexer.OrOr
		right := p.parseAnd()
		left = binaryExpr(syntax.OpOr, left, right)
		left.(*syntax.BinaryExpr).Symbol = symbol
	}
	return left
}

// parseAnd is `&&`, or the older `and` (ADR 0142 §3).
func (p *Parser) parseAnd() syntax.Expr {
	left := p.parseComparison()
	for p.at(lexer.KwAnd) || p.at(lexer.AndAnd) {
		symbol := p.advance().Kind == lexer.AndAnd
		right := p.parseComparison()
		left = binaryExpr(syntax.OpAnd, left, right)
		left.(*syntax.BinaryExpr).Symbol = symbol
	}
	return left
}

var comparisonOps = map[lexer.Kind]syntax.BinaryOp{
	lexer.EqEq:   syntax.OpEq,
	lexer.NotEq:  syntax.OpNe,
	lexer.LAngle: syntax.OpLt,
	lexer.LtEq:   syntax.OpLe,
	lexer.RAngle: syntax.OpGt,
	lexer.GtEq:   syntax.OpGe,
}

func (p *Parser) parseComparison() syntax.Expr {
	left := p.parseAdditive()
	if op, ok := comparisonOps[p.cur().Kind]; ok {
		p.advance()
		right := p.parseAdditive()
		left = binaryExpr(op, left, right)
	}
	if p.at(lexer.Question) {
		q := p.advance()
		start, _ := exprBounds(left)
		left = &syntax.TryExpr{Span: syntax.Span{Start: start, End: q.End}, Operand: left}
	}
	return left
}

// additiveOps and multiplicativeOps are ADR 0019: fixed, int-only,
// non-overloadable arithmetic, slotted between comparison and unary `-` in
// precedence (`not > unary '-' > '*' '/' '%' > '+' '-' > comparison`).
var additiveOps = map[lexer.Kind]syntax.BinaryOp{
	lexer.Plus:  syntax.OpAdd,
	lexer.Minus: syntax.OpSub,
}

var multiplicativeOps = map[lexer.Kind]syntax.BinaryOp{
	lexer.Star:    syntax.OpMul,
	lexer.Slash:   syntax.OpDiv,
	lexer.Percent: syntax.OpMod,
}

func (p *Parser) parseAdditive() syntax.Expr {
	left := p.parseMultiplicative()
	for {
		op, ok := additiveOps[p.cur().Kind]
		if !ok {
			return left
		}
		// `- 1 ->` is the next match arm's literal pattern (ADR 0130), never a
		// subtraction: an expression isn't followed by an arrow, so the minus
		// starts an arm whose body is the line above.
		if p.at(lexer.Minus) && p.peek(1).Kind == lexer.Int && p.peek(2).Kind == lexer.Arrow {
			return left
		}
		p.advance()
		right := p.parseMultiplicative()
		left = binaryExpr(op, left, right)
	}
}

func (p *Parser) parseMultiplicative() syntax.Expr {
	left := p.parseUnary()
	for {
		op, ok := multiplicativeOps[p.cur().Kind]
		if !ok {
			return left
		}
		p.advance()
		right := p.parseUnary()
		left = binaryExpr(op, left, right)
	}
}

// parseUnary handles prefix `-` (ADR 0019). It wraps a full parseUnary (not
// just a primary), so `-a.b` means `-(a.b)` and `-f(x)` means `-(f(x))` —
// unary minus binds looser than postfix access/calls, tighter than
// multiplicative, same as every C-family language.
func (p *Parser) parseUnary() syntax.Expr {
	if p.at(lexer.Minus) {
		start := p.cur().Start
		p.advance()
		operand := p.parseUnary()
		return &syntax.NegExpr{Span: syntax.Span{Start: start, End: p.cur().Start}, Operand: operand}
	}
	return p.parsePostfix()
}

func (p *Parser) parsePostfix() syntax.Expr {
	left := p.parsePrimary()
	// start is the leftmost position of the whole postfix chain (e.g. `a` in
	// `a.b.c()`), not whatever `left` was most recently rebound to — every
	// FieldAccess/CallExpr built below spans from there to wherever parsing
	// currently stands, not just its own trailing piece.
	start, _ := left.(syntax.Spanned).Bounds()
	for {
		switch {
		case p.at(lexer.Dot):
			p.advance()
			field := p.expectFieldName()
			left = &syntax.FieldAccess{Span: syntax.Span{Start: start, End: p.cur().Start}, Target: left, Field: field.Text, FieldStart: field.Start}
		case p.at(lexer.LParen):
			args := p.parseArguments()
			left = &syntax.CallExpr{Span: syntax.Span{Start: start, End: p.cur().Start}, Callee: left, Args: args}
		default:
			return left
		}
	}
}

func (p *Parser) parseArguments() []syntax.Arg {
	p.expect(lexer.LParen)
	var args []syntax.Arg
	if !p.at(lexer.RParen) {
		args = append(args, p.parseArgument())
		for p.at(lexer.Comma) {
			p.advance()
			if p.at(lexer.RParen) {
				// Trailing comma before the closing paren — attested in
				// examples/order-processing/gateways/record_order.fiig.
				break
			}
			args = append(args, p.parseArgument())
		}
	}
	p.expect(lexer.RParen)
	return args
}

// parseArgument parses one argument. A name and a colon make it a named
// one; the name may be a keyword, since a field's name may be (ADR 0073
// §7a) and no expression starts with a keyword followed by a colon.
func (p *Parser) parseArgument() syntax.Arg {
	if p.atFieldName() && p.peek(1).Kind == lexer.Colon {
		name := p.advance().Text
		p.advance() // ':'
		return syntax.Arg{Name: name, Value: p.parseExpression()}
	}
	return syntax.Arg{Value: p.parseExpression()}
}

func (p *Parser) parsePrimary() syntax.Expr {
	start := p.cur().Start
	if p.atSortedForm() {
		return p.parseSortedExpr()
	}
	if p.atListForm() {
		return p.parseListForm()
	}
	if p.atName() {
		tok := p.advance()
		return &syntax.Ident{Span: syntax.Span{Start: tok.Start, End: tok.End}, Name: tok.Text}
	}
	switch p.cur().Kind {
	case lexer.Int:
		tok := p.advance()
		return &syntax.IntLit{Span: syntax.Span{Start: tok.Start, End: tok.End}, Text: tok.Text}
	case lexer.Float:
		tok := p.advance()
		return &syntax.FloatLit{Span: syntax.Span{Start: tok.Start, End: tok.End}, Text: tok.Text}
	case lexer.StringStart:
		return p.parseStringLit()
	case lexer.LParen:
		p.advance()
		inner := p.parseExpression()
		end := p.expect(lexer.RParen).End
		return &syntax.ParenExpr{Span: syntax.Span{Start: start, End: end}, Inner: inner}
	case lexer.LBracket:
		return p.parseListLit()
	case lexer.LShift:
		segs, span := p.parseSegments(false)
		return &syntax.BytesLit{Span: span, Segments: segs}
	case lexer.KwNot, lexer.Bang:
		// `!` is ADR 0142 §3's spelling of `not`. Either binds to the full
		// postfix chain, not just the bare primary: `not
		// input.email.value.contains("@")` (validate_new_customer.fiig) is
		// `not (input.email.value.contains("@"))`, never `(not
		// input).email.value.contains("@")`. parsePostfix, not parsePrimary
		// — postfix access/calls bind tighter than `not`, same as every
		// other prefix operator in this grammar (parseUnary's own doc
		// comment makes the identical point for unary `-`).
		bang := p.advance().Kind == lexer.Bang
		operand := p.parsePostfix()
		return &syntax.NotExpr{Span: syntax.Span{Start: start, End: p.cur().Start}, Operand: operand, Bang: bang}
	case lexer.KwConcurrent:
		return p.parseConcurrentExpr()
	case lexer.KwRetry:
		return p.parseRetryExpr()
	case lexer.KwIf, lexer.KwMatch:
		// `x = if a > b { a } else { b }`, a habit from Kotlin and Rust:
		// say once that it isn't a value, then parse it as the statement it
		// is, so its braces don't turn the rest of the file into errors.
		if p.at(lexer.KwIf) {
			p.errorf(start, "`if` is a statement, not a value; assign or return in each branch (for the larger or smaller of two numbers, use math.max or math.min)")
			p.parseIfStmt()
		} else {
			p.errorf(start, "a `match` is a value only as a whole `return` or binding value, `return match (x) { ... }` or `name = match (x) { ... }` (ADR 0142 §2); bind it first, then use the name")
			p.parseMatchStmt()
		}
		return &syntax.Ident{Span: syntax.Span{Start: start, End: p.cur().Start}, Name: syntax.ErrorName}
	default:
		if tok := p.cur(); p.keywordNames[tok.Text] && lexer.IsKeyword(tok.Kind) {
			// A keyword this file already used as a name, already reported
			// there (GAPS G33): its uses read as that name, so it's said once.
			p.advance()
			return &syntax.Ident{Span: syntax.Span{Start: tok.Start, End: tok.End}, Name: tok.Text}
		}
		p.errorf(start, "expected an expression, got %s", p.cur().Kind)
		p.advance()
		return &syntax.Ident{Span: syntax.Span{Start: start, End: p.cur().Start}, Name: syntax.ErrorName}
	}
}

// parseSegments parses `<< segment, ... >>` (ADR 0058), the closing `>>`
// being two `>` tokens. In a pattern a segment's left side is a name, `_`,
// or a literal; in an expression it is any expression.
func (p *Parser) parseSegments(pattern bool) ([]syntax.Segment, syntax.Span) {
	start := p.expect(lexer.LShift).Start
	var segs []syntax.Segment
	for !p.at(lexer.RAngle) && !p.at(lexer.EOF) {
		segs = append(segs, p.parseSegment(pattern))
		if !p.at(lexer.Comma) {
			break
		}
		p.advance()
	}
	p.expect(lexer.RAngle)
	end := p.expect(lexer.RAngle).End
	return segs, syntax.Span{Start: start, End: end}
}

func (p *Parser) parseSegment(pattern bool) syntax.Segment {
	start := p.cur().Start
	var seg syntax.Segment
	switch {
	case !pattern:
		seg.Value = p.parseExpression()
	case p.at(lexer.Underscore):
		p.advance()
		seg.Name = "_"
	case p.at(lexer.Int):
		tok := p.advance()
		seg.Literal = &syntax.IntLit{Span: syntax.Span{Start: tok.Start, End: tok.End}, Text: tok.Text}
	case p.at(lexer.StringStart):
		seg.Literal = p.parseStringLit()
	default:
		seg.Name = p.expectName().Text
	}
	p.expect(lexer.Colon)
	seg.Encoding = p.parseEncoding()
	seg.Span = syntax.Span{Start: start, End: seg.Encoding.End}
	return seg
}

// parseEncoding reads an encoding name and, for bytes and utf8, an
// optional `(size)`. The parser only splits the name into its parts; which
// combinations are legal is the checker's job (A14).
func (p *Parser) parseEncoding() syntax.Encoding {
	tok := p.expectName()
	enc := syntax.Encoding{Span: syntax.Span{Start: tok.Start, End: tok.End}, Name: tok.Text}
	switch tok.Text {
	case "bytes":
		enc.Kind = syntax.SegBytes
	case "utf8":
		enc.Kind = syntax.SegUTF8
	default:
		enc.Kind = syntax.SegInt
		enc.Bits, enc.Signed, enc.Little = syntax.ParseIntEncoding(tok.Text)
	}
	if enc.Kind != syntax.SegInt && p.at(lexer.LParen) {
		p.advance()
		enc.Size = p.parseExpression()
		enc.End = p.expect(lexer.RParen).End
	}
	return enc
}

// parseListLit is ADR 0057's `[a, b, c]`, trailing comma allowed, or ADR
// 0065 §5's map literal: `[:]`, or a first element followed by `:`.
func (p *Parser) parseListLit() syntax.Expr {
	start := p.expect(lexer.LBracket).Start
	if p.at(lexer.Colon) && p.peek(1).Kind == lexer.RBracket {
		p.advance()
		end := p.advance().End
		return &syntax.MapLit{Span: syntax.Span{Start: start, End: end}}
	}
	var elems []syntax.Expr
	for !p.at(lexer.RBracket) && !p.at(lexer.EOF) {
		elem := p.parseExpression()
		if len(elems) == 0 && p.at(lexer.Colon) {
			return p.parseMapEntries(start, elem)
		}
		elems = append(elems, elem)
		if !p.at(lexer.Comma) {
			break
		}
		p.advance()
	}
	end := p.expect(lexer.RBracket).End
	return &syntax.ListLit{Span: syntax.Span{Start: start, End: end}, Elems: elems}
}

// parseMapEntries finishes a map literal whose first key is parsed and
// whose `:` is next.
func (p *Parser) parseMapEntries(start lexer.Pos, key syntax.Expr) *syntax.MapLit {
	var entries []syntax.MapEntry
	for {
		p.expect(lexer.Colon)
		entries = append(entries, syntax.MapEntry{Key: key, Value: p.parseExpression()})
		if !p.at(lexer.Comma) {
			break
		}
		p.advance()
		if p.at(lexer.RBracket) {
			break
		}
		key = p.parseExpression()
	}
	end := p.expect(lexer.RBracket).End
	return &syntax.MapLit{Span: syntax.Span{Start: start, End: end}, Entries: entries}
}

func (p *Parser) parseStringLit() *syntax.StringLit {
	start := p.expect(lexer.StringStart).Start
	var parts []syntax.StringPart
	for {
		switch p.cur().Kind {
		case lexer.StringText:
			tok := p.advance()
			parts = append(parts, syntax.StringPart{Text: tok.Text})
		case lexer.InterpStart:
			p.advance()
			expr := p.parseExpression()
			p.expect(lexer.InterpEnd)
			parts = append(parts, syntax.StringPart{Interp: expr})
		case lexer.StringEnd:
			end := p.advance().End
			return &syntax.StringLit{Span: syntax.Span{Start: start, End: end}, Parts: parts}
		default:
			p.errorf(p.cur().Start, "unterminated string literal")
			return &syntax.StringLit{Span: syntax.Span{Start: start, End: p.cur().Start}, Parts: parts}
		}
	}
}

func (p *Parser) parseConcurrentExpr() *syntax.ConcurrentExpr {
	start := p.advance().Start // 'concurrent'
	p.expect(lexer.LParen)
	varTok := p.expectName()
	p.expect(lexer.KwIn)
	iterable := p.parseExpression()
	var limit *syntax.LimitValue
	var preserveOrder bool
	for p.at(lexer.Comma) {
		p.advance()
		if p.at(lexer.KwPreserveOrder) {
			p.advance()
			preserveOrder = true
			continue
		}
		limitStart := p.expect(lexer.KwLimit).Start
		p.expect(lexer.Colon)
		if p.atName() && p.cur().Text == "unbounded" {
			p.advance()
			limit = &syntax.LimitValue{Span: syntax.Span{Start: limitStart, End: p.cur().Start}, Unbounded: true}
		} else {
			val := p.parseExpression()
			limit = &syntax.LimitValue{Span: syntax.Span{Start: limitStart, End: p.cur().Start}, Value: val}
		}
	}
	p.expect(lexer.RParen)
	p.expect(lexer.Colon)
	body := p.parseBody()
	return &syntax.ConcurrentExpr{Span: syntax.Span{Start: start, End: p.cur().Start}, Var: varTok.Text, Iterable: iterable, Limit: limit, PreserveOrder: preserveOrder, Body: body}
}

// atSortedForm reports whether the parser is at ADR 0065 §3's keyed sort.
// `sorted` is contextual, not reserved, so `xs.sorted()`, a local named
// sorted, and a call to a declaration named sorted all stay legal: the form
// starts only at `sorted ( name in`, which no call can begin with, since
// `in` is a keyword.
func (p *Parser) atSortedForm() bool {
	return p.cur().Text == "sorted" && p.peek(1).Kind == lexer.LParen && isNameToken(p.peek(2).Kind) && p.peek(3).Kind == lexer.KwIn
}

// parseSortedExpr parses `sorted(x in xs): key [descending] [then key
// [descending]]...` (ADR 0093 §4), the shape of concurrent's header with
// expressions for a body. `descending` and `then` are contextual: each
// counts only on the line its key ends on, since after a whole expression
// a name can't continue it. The old header modifier, `sorted(x in xs,
// descending): key`, still parses, so the checker can say what to write
// and fiig fmt can rewrite it.
func (p *Parser) parseSortedExpr() *syntax.SortedExpr {
	start := p.advance().Start // 'sorted'
	p.expect(lexer.LParen)
	varTok := p.expectName()
	p.expect(lexer.KwIn)
	iterable := p.parseExpression()
	header := false
	if p.at(lexer.Comma) {
		p.advance()
		mod := p.expectName()
		if mod.Text == "descending" {
			header = true
		} else {
			p.errorf(mod.Start, "sorted takes no modifier here; write `descending` after a key")
		}
	}
	p.expect(lexer.RParen)
	p.expect(lexer.Colon)
	var keys []syntax.SortKey
	for {
		k := syntax.SortKey{Key: p.parseExpression(), Descending: header}
		if p.atContextual("descending") && p.sameLine() {
			p.advance()
			k.Descending = true
		}
		keys = append(keys, k)
		if !(p.atContextual("then") && p.sameLine()) {
			break
		}
		p.advance()
	}
	return &syntax.SortedExpr{Span: syntax.Span{Start: start, End: p.cur().Start}, Var: varTok.Text, Iterable: iterable, Keys: keys, HeaderDescending: header}
}

// sameLine reports whether the current token starts on the line the
// previous one ended on.
func (p *Parser) sameLine() bool {
	return p.cur().Start.Line == p.peek(-1).End.Line
}

// atListForm reports whether an ADR 0093 list form starts here. Like
// sorted, each name is contextual: the form starts only at `name ( ident
// in`, which no call can begin with.
func (p *Parser) atListForm() bool {
	return syntax.ListForms[p.cur().Text] && isNameToken(p.cur().Kind) && p.peek(1).Kind == lexer.LParen && isNameToken(p.peek(2).Kind) && p.peek(3).Kind == lexer.KwIn
}

// parseListForm parses `form(x in xs): body`. A mapErr's body is an error
// value, so a `?` that ends it belongs to the whole form (ADR 0095 §1):
// `mapErr(e in r): Wrap(e)?` is `(mapErr(e in r): Wrap(e))?`.
func (p *Parser) parseListForm() syntax.Expr {
	formTok := p.advance()
	p.expect(lexer.LParen)
	varTok := p.expectName()
	p.expect(lexer.KwIn)
	iterable := p.parseExpression()
	p.expect(lexer.RParen)
	p.expect(lexer.Colon)
	body := p.parseExpression()
	form := &syntax.ListForm{Span: syntax.Span{Start: formTok.Start, End: p.cur().Start}, Form: formTok.Text, Var: varTok.Text, Iterable: iterable, Body: body}
	if try, ok := body.(*syntax.TryExpr); ok && form.Form == "mapErr" {
		form.Body = try.Operand
		return &syntax.TryExpr{Span: form.Span, Operand: form}
	}
	return form
}

func (p *Parser) parseRetryExpr() *syntax.RetryExpr {
	start := p.advance().Start // 'retry'
	p.expect(lexer.LParen)
	p.expect(lexer.KwAttempts)
	p.expect(lexer.Colon)
	countTok := p.expect(lexer.Int)
	p.expect(lexer.RParen)
	p.expect(lexer.Colon)
	body := p.parseBody()
	attempts := &syntax.IntLit{Span: syntax.Span{Start: countTok.Start, End: countTok.End}, Text: countTok.Text}
	return &syntax.RetryExpr{Span: syntax.Span{Start: start, End: p.cur().Start}, Attempts: attempts, Body: body}
}

// allTests reports whether every declaration in decls is a test.
func allTests(decls []syntax.Decl) bool {
	for _, d := range decls {
		if _, ok := d.(*syntax.TestDecl); !ok {
			return false
		}
	}
	return true
}
