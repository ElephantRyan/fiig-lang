// Package format implements Fiig's canonical, non-configurable formatter
// (fiig-lang.md Milestone 1: "Canonical non-configurable formatter").
//
// Format picks one deterministic rendering for every construct the grammar
// allows — it does not try to preserve an input file's own line-wrapping
// choices (Milestone 1's exit criteria only requires that formatting is
// deterministic and that reformatting already-formatted source is a no-op,
// not that it reproduce any particular hand-written layout). The one thing
// it does preserve is comments: the lexer discards them from the token
// stream entirely (they're not part of the grammar), so the formatter
// reattaches them itself from the side channel lexer.Comment /
// parser.ParseWithComments provide, using source line numbers to decide
// whether a comment is a standalone line before something, or a trailing
// comment sharing a line with it.
package format

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
)

// maxLineWidth is the only layout threshold the formatter has: a statement
// whose single-line rendering would exceed it, and whose value is directly
// a call expression, gets that call's arguments split one-per-line with a
// trailing comma instead (grammar.md's `arguments` trailing-comma note,
// attested by order-processing/gateways/record_order.fiig). A list literal
// (ADR 0057) and a binary expression (ADR 0058) wrap the same way, one
// element or segment per line. Nothing else wraps.
const maxLineWidth = 100

// Format parses src and renders it back out in Fiig's canonical style.
// Returns the parse errors instead of formatted text if src doesn't parse.
// A second public declaration (G1, ADR 0054) doesn't count: the parser
// still builds the whole tree for it, so there is nothing to lose by
// formatting, and the checker is what reports it.
func Format(src string) (string, []parser.Error) {
	file, comments, errs := parser.ParseWithComments(src)
	for _, e := range errs {
		if e.Rule != "G1" {
			return "", errs
		}
	}
	p := &printer{comments: comments}
	p.printFile(file)
	return p.out.String(), nil
}

type printer struct {
	out      strings.Builder
	indent   int
	comments []lexer.Comment
	ci       int // index of the next not-yet-emitted comment
}

func (p *printer) writeIndent() {
	p.out.WriteString(strings.Repeat("    ", p.indent))
}

// emitLeading prints, each on its own line, every not-yet-emitted comment
// that starts strictly before beforeLine — a standalone comment that
// appeared before whatever comes next, rather than trailing something
// already printed.
func (p *printer) emitLeading(beforeLine int) {
	for p.ci < len(p.comments) && p.comments[p.ci].Start.Line < beforeLine {
		p.writeIndent()
		p.out.WriteString(p.comments[p.ci].Text)
		p.out.WriteByte('\n')
		p.ci++
	}
}

// emitLine writes one logical line of output — indent, content, then any
// pending comment whose source line falls within [fromLine, toLine] as a
// trailing comment, then a newline. The range (rather than a single line)
// exists because some content a single output line collapses onto (a
// nested concurrent/retry chain, for instance) can span several original
// source lines, each carrying its own trailing comment — see deepestLine.
func (p *printer) emitLine(content string, fromLine, toLine int) {
	p.writeIndent()
	p.out.WriteString(content)
	for p.ci < len(p.comments) && p.comments[p.ci].Start.Line >= fromLine && p.comments[p.ci].Start.Line <= toLine {
		p.out.WriteString(" ")
		p.out.WriteString(p.comments[p.ci].Text)
		p.ci++
	}
	p.out.WriteByte('\n')
}

func startLine(n syntax.Spanned) int {
	start, _ := n.Bounds()
	return start.Line
}

// ---- file ------------------------------------------------------------

func (p *printer) printFile(f *syntax.File) {
	first := -1
	if len(f.Imports) > 0 {
		first = startLine(f.Imports[0])
	} else if len(f.Decls) > 0 {
		first = startLine(f.Decls[0].(syntax.Spanned))
	}
	if first >= 0 {
		p.emitLeading(first)
	}

	for i, imp := range f.Imports {
		p.printImport(imp)
		var next int
		if i+1 < len(f.Imports) {
			next = startLine(f.Imports[i+1])
		} else if len(f.Decls) > 0 {
			p.out.WriteByte('\n') // blank line between imports and the declaration(s)
			next = startLine(f.Decls[0].(syntax.Spanned))
		} else {
			next = 1 << 30
		}
		p.emitLeading(next)
	}

	for i, d := range f.Decls {
		p.printDecl(d)
		if i+1 < len(f.Decls) {
			p.out.WriteByte('\n')
			p.emitLeading(startLine(f.Decls[i+1].(syntax.Spanned)))
		}
	}
	if f.Private != nil {
		p.out.WriteByte('\n')
		p.printPrivateBlock(f.Private)
	}

	// Drain anything left over rather than silently dropping it — shouldn't
	// normally happen, since every comment should be claimed as leading or
	// trailing something above.
	p.emitLeading(1 << 30)
}

func (p *printer) printImport(imp *syntax.Import) {
	var sb strings.Builder
	sb.WriteString("use ")
	sb.WriteString(strings.Join(imp.Path, "."))
	if imp.Alias != "" {
		sb.WriteString(" as ")
		sb.WriteString(imp.Alias)
	}
	line := startLine(imp)
	p.emitLine(sb.String(), line, line)
}

// ---- declarations ------------------------------------------------------

func (p *printer) printDecl(d syntax.Decl) {
	switch d := d.(type) {
	case *syntax.ModelDecl:
		p.printFieldedDecl("model "+d.Name, fieldsToSpanned(d.Fields), startLine(d), func(i int) string {
			return printField(d.Fields[i])
		})
	case *syntax.EnumDecl:
		if d.Header != nil {
			rows := enumRows(d.Variants)
			p.printFieldedDecl("enum "+d.Name+printEnumHeader(d.Header), variantsToSpanned(d.Variants), startLine(d), func(i int) string {
				return rows[i]
			})
			break
		}
		p.printFieldedDecl("enum "+d.Name, variantsToSpanned(d.Variants), startLine(d), func(i int) string {
			return printEnumVariant(d.Variants[i])
		})
	case *syntax.RuleDecl:
		p.printCallableDecl("rule", d.Name, d.Params, d.ReturnType, d.Body, startLine(d))
	case *syntax.ConversionDecl:
		p.printCallableDecl("conversion", d.Name, d.Params, d.ReturnType, d.Body, startLine(d))
	case *syntax.WorkflowDecl:
		p.printCallableDecl("workflow", d.Name, d.Params, d.ReturnType, d.Body, startLine(d))
	case *syntax.ActivityDecl:
		p.printCallableDecl("activity", d.Name, d.Params, d.ReturnType, d.Body, startLine(d))
	case *syntax.GatewayDecl:
		p.printGatewayDecl(d)
	case *syntax.AdapterDecl:
		p.printAdapterDecl(d)
	case *syntax.InterfaceDecl:
		p.printFieldedDecl("interface "+d.Name, membersToSpanned(d.Members), startLine(d), func(i int) string {
			m := d.Members[i]
			return fmt.Sprintf("%s%s -> %s", m.Name, printParams(m.Params), printType(m.ReturnType))
		})
	case *syntax.CapabilityDecl:
		p.printCapabilityDecl(d)
	case *syntax.TestDecl:
		p.printTestDecl(d)
	case *syntax.FormulasDecl:
		p.printFormulasDecl(d)
	case *syntax.ConfigDecl:
		spanned := make([]syntax.Spanned, len(d.Fields))
		for i, f := range d.Fields {
			spanned[i] = f
		}
		header := "config " + d.Name
		if d.When != nil {
			header += " when " + printExpr(d.When)
		}
		p.printFieldedDecl(header, spanned, startLine(d), func(i int) string {
			f := d.Fields[i]
			line := fmt.Sprintf("%s: %s from env %s", f.Name, printType(f.Type), strconv.Quote(f.Env))
			if f.Startup != "" {
				line = fmt.Sprintf("%s: %s from startup %s", f.Name, printType(f.Type), strconv.Quote(f.Startup))
			}
			if f.Default != nil {
				line += " = " + printExpr(f.Default)
			}
			return line
		})
	}
}

func membersToSpanned(members []syntax.InterfaceMember) []syntax.Spanned {
	out := make([]syntax.Spanned, len(members))
	for i, m := range members {
		out[i] = m
	}
	return out
}

// printCapabilityDecl prints a capability group (ADR 0043 §5a): its
// gateways one level in, a blank line between them, the same layout a
// private block uses for its members.
func (p *printer) printCapabilityDecl(d *syntax.CapabilityDecl) {
	line := startLine(d)
	header := "capability " + d.Name
	if len(d.Declares) > 0 {
		header += " provides " + strings.Join(d.Declares, ".") // ADR 0105 §3
	}
	p.emitLine(header+" {", line, line)
	p.indent++
	for i, g := range d.Gateways {
		if i > 0 {
			p.out.WriteByte('\n')
		}
		p.emitLeading(startLine(g))
		p.printGatewayDecl(g)
	}
	p.indent--
	p.writeIndent()
	p.out.WriteString("}\n")
}

// printTestDecl prints a test (ADR 0091): its fakes first, a blank line
// after each, then its statements.
func (p *printer) printTestDecl(d *syntax.TestDecl) {
	line := startLine(d)
	p.emitLine("test "+d.Name+" {", line, line)
	p.indent++
	// Fakes sit together, with one blank line between them and the body.
	for i, f := range d.Fakes {
		p.emitLeading(startLine(f))
		p.printFakeDecl(f)
		if i == len(d.Fakes)-1 && len(d.Body.Stmts) > 0 {
			p.out.WriteByte('\n')
		}
	}
	p.indent--
	p.printBlockBody(d.Body)
}

// printFakeDecl prints a fake's gateways the way a capability group's are
// printed, and its values one `name: value` per line.
func (p *printer) printFakeDecl(f *syntax.FakeDecl) {
	line := startLine(f)
	p.emitLine("fake "+f.TargetName()+" {", line, line)
	p.indent++
	for i, g := range f.Gateways {
		if i > 0 {
			p.out.WriteByte('\n')
		}
		p.emitLeading(startLine(g))
		p.printGatewayDecl(g)
	}
	for _, v := range f.Values {
		vLine := startLine(v.Value.(syntax.Spanned))
		p.emitLeading(vLine)
		p.printValueLine(v.Name+": ", v.Value, vLine)
	}
	_, end := f.Bounds()
	p.emitLeading(end.Line)
	p.indent--
	p.emitLine("}", end.Line, end.Line)
}

// printFormulasDecl prints a formulas group (ADR 0049 §2.1) the way
// printCapabilityDecl prints a capability group. A native member has no
// body, so it is its header line alone; a `requires` clause (K4) is kept as
// written, since the formatter never drops source.
func (p *printer) printFormulasDecl(d *syntax.FormulasDecl) {
	line := startLine(d)
	p.emitLine("formulas "+d.Name+" {", line, line)
	p.indent++
	for i, m := range d.Members {
		if i > 0 {
			p.out.WriteByte('\n')
		}
		p.emitLeading(startLine(m))
		header := fmt.Sprintf("formula %s%s -> %s", m.Name, printParams(m.Params), printType(m.ReturnType))
		if m.Requires != "" {
			header += " requires " + m.Requires
		}
		mLine := startLine(m)
		if m.Body == nil {
			p.emitLine(header, mLine, mLine)
			continue
		}
		p.emitLine(header+" {", mLine, mLine)
		p.printBlockBody(m.Body)
	}
	p.indent--
	p.writeIndent()
	p.out.WriteString("}\n")
}

// printPrivateBlock prints ADR 0054's `private { ... }` after the public
// declaration: members in source order, one level in, a blank line
// between them.
func (p *printer) printPrivateBlock(b *syntax.PrivateBlock) {
	p.emitLeading(startLine(b))
	p.emitLine("private {", startLine(b), startLine(b))
	p.indent++
	for i, m := range b.Members {
		if i > 0 {
			p.out.WriteByte('\n')
		}
		p.emitLeading(startLine(m.(syntax.Spanned)))
		if h, ok := m.(*syntax.HelperDecl); ok {
			line := startLine(h)
			p.emitLine(fmt.Sprintf("%s%s -> %s {", h.Name, printParams(h.Params), printType(h.ReturnType)), line, line)
			p.printBlockBody(h.Body)
			continue
		}
		p.printDecl(m)
	}
	_, end := b.Bounds()
	p.emitLeading(end.Line)
	p.indent--
	p.emitLine("}", end.Line, end.Line)
}

// fieldsToSpanned/variantsToSpanned adapt a concrete slice to
// []syntax.Spanned so printFieldedDecl can compute line numbers generically.
func fieldsToSpanned(fields []syntax.Field) []syntax.Spanned {
	out := make([]syntax.Spanned, len(fields))
	for i, f := range fields {
		out[i] = f
	}
	return out
}

func variantsToSpanned(variants []syntax.EnumVariant) []syntax.Spanned {
	out := make([]syntax.Spanned, len(variants))
	for i, v := range variants {
		out[i] = v
	}
	return out
}

// printFieldedDecl handles model/enum declarations: a single-line header
// ending in "{", one item per line, and a closing "}" — the one shape both
// kinds share, differing only in how each item renders.
func (p *printer) printFieldedDecl(header string, items []syntax.Spanned, headerLine int, renderItem func(i int) string) {
	p.emitLine(header+" {", headerLine, headerLine)
	p.indent++
	for i, item := range items {
		line := startLine(item)
		p.emitLeading(line)
		p.emitLine(renderItem(i), line, line)
	}
	p.indent--
	// No drain here: a comment between the last item and "}" isn't attested
	// anywhere in the corpus, and using an unbounded sentinel as "the closing
	// brace's line" wrongly sweeps up comments belonging to whatever follows
	// this declaration in the file instead. Leaving it unclaimed just means
	// the next emitLeading call up the call stack picks it up as a leading
	// comment on the next sibling instead; nothing is lost.
	p.out.WriteString(strings.Repeat("    ", p.indent))
	p.out.WriteString("}\n")
}

func printField(f syntax.Field) string {
	if f.Embedded {
		return printType(f.Type)
	}
	s := fmt.Sprintf("%s: %s", f.Name, printType(f.Type))
	if f.JSON == "" && !f.Base64URL {
		return s
	}
	s += " from json"
	if f.JSON != "" {
		s += " " + strconv.Quote(f.JSON)
	}
	if f.Base64URL {
		s += " base64url" // ADR 0078 §6
	}
	return s
}

func printEnumHeader(header []syntax.EnumField) string {
	parts := make([]string, len(header))
	for i, f := range header {
		parts[i] = fmt.Sprintf("%s: %s", f.Name, printType(f.Type))
		if f.Key {
			parts[i] = "key " + parts[i]
		}
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// enumRows renders a headed enum's variants aligned by column, so the
// declaration reads as a table: the header is the column list, each row
// one variant (ADR 0046 §4). Columns align only when every row names its
// values in the same order; otherwise each row prints as written.
func enumRows(variants []syntax.EnumVariant) []string {
	cells := make([][]string, len(variants))
	aligned := true
	for i, v := range variants {
		if len(v.Values) != len(variants[0].Values) {
			aligned = false
		}
		for j, a := range v.Values {
			cell := printExpr(a.Value)
			if a.Name != "" {
				cell = a.Name + ": " + cell
			}
			cells[i] = append(cells[i], cell)
			if aligned && a.Name != variants[0].Values[j].Name {
				aligned = false
			}
		}
	}
	nameWidth := 0
	widths := make([]int, len(variants[0].Values))
	if aligned {
		for i, v := range variants {
			nameWidth = max(nameWidth, utf8.RuneCountInString(v.Name))
			for j, cell := range cells[i] {
				widths[j] = max(widths[j], utf8.RuneCountInString(cell))
			}
		}
	}
	out := make([]string, len(variants))
	for i, v := range variants {
		if len(v.Values) == 0 {
			out[i] = v.Name + printSpelling(v)
			continue
		}
		var b strings.Builder
		b.WriteString(v.Name)
		b.WriteString("(")
		if aligned {
			b.WriteString(strings.Repeat(" ", nameWidth-utf8.RuneCountInString(v.Name)))
		}
		for j, cell := range cells[i] {
			b.WriteString(cell)
			if j == len(cells[i])-1 {
				break
			}
			b.WriteString(", ")
			if aligned {
				b.WriteString(strings.Repeat(" ", widths[j]-utf8.RuneCountInString(cell)))
			}
		}
		b.WriteString(")")
		b.WriteString(printSpelling(v))
		out[i] = b.String()
	}
	return out
}

func printEnumVariant(v syntax.EnumVariant) string {
	if len(v.Fields) == 0 {
		return v.Name + printSpelling(v)
	}
	parts := make([]string, len(v.Fields))
	for i, f := range v.Fields {
		parts[i] = printField(f)
	}
	return fmt.Sprintf("%s(%s)%s", v.Name, strings.Join(parts, ", "), printSpelling(v))
}

// printSpelling is a variant's `from json` clause (ADR 0080 §1), or "".
func printSpelling(v syntax.EnumVariant) string {
	if v.JSON == "" {
		return ""
	}
	return " from json " + strconv.Quote(v.JSON)
}

// printCallableDecl handles rule/conversion/workflow, whose header is
// always `keyword name(params) -> type {` on one line (attested uniformly;
// unlike gateway, none of them ever split `requires`/`idempotent` onto their
// own line, because they don't have one).
func (p *printer) printCallableDecl(keyword, name string, params []syntax.Param, ret syntax.Type, body *syntax.Block, line int) {
	header := fmt.Sprintf("%s %s%s -> %s {", keyword, name, printParams(params), printType(ret))
	p.emitLine(header, line, line)
	p.printBlockBody(body)
}

// printGatewayDecl handles gateway's distinct header shape (ADR 0031,
// renamed from step): signature on its own line, then `requires
// Capability` and (if present) `idempotent` each on their own line, then
// `{` on its own line — attested with zero exceptions across every
// gateway in the corpus, regardless of how short the signature is
// (get_customer.fiig's one-param signature still splits this way), so
// it's treated as gateway's fixed shape rather than a width-driven wrap.
func (p *printer) printGatewayDecl(d *syntax.GatewayDecl) {
	sigLine := startLine(d)
	sig := fmt.Sprintf("gateway %s%s -> %s", d.Name, printParams(d.Params), printType(d.ReturnType))
	if d.Requires == "" && !d.Idempotent {
		// A fake's operation (ADR 0091 §4) may require nothing.
		p.emitLine(sig+" {", sigLine, sigLine)
		p.printBlockBody(d.Body)
		return
	}
	p.emitLine(sig, sigLine, sigLine)
	p.indent++
	if d.Requires != "" {
		p.writeIndent()
		p.out.WriteString("requires " + d.Requires + "\n")
	}
	if d.Idempotent {
		p.writeIndent()
		p.out.WriteString("idempotent\n")
	}
	p.indent--
	p.writeIndent()
	p.out.WriteString("{\n")
	p.printBlockBody(d.Body)
}

func printParams(params []syntax.Param) string {
	parts := make([]string, len(params))
	for i, prm := range params {
		parts[i] = fmt.Sprintf("%s: %s", prm.Name, printType(prm.Type))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func (p *printer) printAdapterDecl(d *syntax.AdapterDecl) {
	line := startLine(d)
	p.emitLine(fmt.Sprintf("adapter %s %s {", string(d.Kind), d.Name), line, line)
	p.indent++
	for _, r := range d.Routes {
		p.emitLeading(startLine(r))
		p.printRoute(r)
	}
	p.indent--
	// See printFieldedDecl: no drain before "}" here either.
	p.writeIndent()
	p.out.WriteString("}\n")
}

func (p *printer) printRoute(r *syntax.Route) {
	line := startLine(r)
	args := make([]string, len(r.Args))
	for i, a := range r.Args {
		args[i] = printRouteArg(a)
	}
	head := fmt.Sprintf("%s %s -> %s", r.Verb, strconv.Quote(r.Path), r.Workflow.Name)
	if r.Verb == "run" {
		// A startup route has no path (ADR 0124 §1).
		head = "run"
		if r.Once {
			head += " once"
		}
		if r.Within != "" {
			head += " within " + strconv.Quote(r.Within)
		}
		head += " -> " + r.Workflow.Name
	}
	if r.Verb == "source" {
		head = r.Verb + " " + strconv.Quote(r.Path)
		if r.Group != "" {
			head += " group " + strconv.Quote(r.Group)
		}
		if r.From != "" {
			head += " from " + r.From
		}
		if r.Key != nil {
			head += " key " + r.Key.Name
		}
		head += " -> " + r.Workflow.Name
	}
	var clauses []*syntax.MediaType
	if r.Consumes != nil {
		clauses = append(clauses, r.Consumes)
	}
	if r.Produces != nil {
		clauses = append(clauses, r.Produces)
	}
	brace := ""
	if len(r.Mapping) > 0 && len(clauses) == 0 {
		brace = " {"
	}
	oneLine := head + "(" + strings.Join(args, ", ") + ")" + brace
	if len(args) == 0 || len(strings.Repeat("    ", p.indent))+len(oneLine) <= maxLineWidth {
		p.emitLine(oneLine, line, line)
	} else {
		// Too long: one argument per line with a trailing comma, as a
		// long call is wrapped.
		p.emitLine(head+"(", line, line)
		p.indent++
		for i, a := range r.Args {
			al := startLine(a)
			p.emitLine(args[i]+",", al, al)
		}
		p.indent--
		p.emitLine(")"+brace, line, line)
	}
	p.indent++
	for _, c := range clauses {
		cl := startLine(c)
		word := "consumes"
		if c == r.Produces {
			word = "produces"
		}
		p.emitLine(word+" "+strconv.Quote(c.Text), cl, cl)
	}
	p.indent--
	if len(r.Mapping) == 0 {
		return
	}
	if len(clauses) > 0 {
		p.emitLine("{", line, line)
	}
	p.indent++
	for _, arm := range r.Mapping {
		al := startLine(arm)
		p.emitLeading(al)
		content := fmt.Sprintf("%s -> %s", printPattern(arm.Pattern), printResponseValue(arm.Value))
		p.emitLine(content, al, al)
	}
	p.indent--
	p.writeIndent()
	p.out.WriteString("}\n")
}

func printRouteArg(a syntax.RouteArg) string {
	s := a.Name + ": " + printType(a.Type)
	if a.Source != "" {
		s += " from " + a.Source + " " + strconv.Quote(a.SourceName)
	}
	return s
}

func printResponseValue(v syntax.ResponseValue) string {
	switch v := v.(type) {
	case *syntax.StatusResponse:
		s := printExpr(v.Code)
		if v.Body != nil {
			s += " " + printExpr(v.Body)
		}
		return s
	case *syntax.ExitResponse:
		s := "exit " + printExpr(v.Code)
		if v.Body != nil {
			s += " " + printExpr(v.Body)
		}
		return s
	case *syntax.ExprResponse:
		return printExpr(v.Value)
	case *syntax.DispositionResponse:
		return printDisposition(v)
	default:
		return "?"
	}
}

// printDisposition prints a stream route's disposition (ADR 0111 §3).
func printDisposition(d *syntax.DispositionResponse) string {
	switch d.Kind {
	case "deadLetter":
		return "deadLetter " + strconv.Quote(d.Target)
	case "retry":
		s := fmt.Sprintf("retry(attempts: %d)", d.Attempts)
		if d.Else != nil {
			s += " else " + printDisposition(d.Else)
		}
		return s
	}
	return d.Kind
}

// ---- types ---------------------------------------------------------------

func printType(t syntax.Type) string {
	switch t := t.(type) {
	case *syntax.QualifiedType:
		s := strings.Join(t.Path, ".")
		if len(t.Args) > 0 {
			args := make([]string, len(t.Args))
			for i, a := range t.Args {
				args[i] = printType(a)
			}
			s += "<" + strings.Join(args, ", ") + ">"
		}
		return s
	case *syntax.RecordType:
		fields := make([]string, len(t.Fields))
		for i, f := range t.Fields {
			fields[i] = printField(f)
		}
		return "{ " + strings.Join(fields, ", ") + " }"
	case *syntax.UnionType:
		members := make([]string, len(t.Members))
		for i, m := range t.Members {
			members[i] = printType(m)
		}
		return strings.Join(members, " | ")
	default:
		return "?"
	}
}

// ---- blocks and statements ------------------------------------------------

// printBlockBody prints a block's statements and closing "}", assuming the
// opening "{" and its newline were already written by the caller (it's
// always the tail of a header line the caller already emitted).
func (p *printer) printBlockBody(b *syntax.Block) {
	p.indent++
	for _, s := range b.Stmts {
		p.emitLeading(startLine(s.(syntax.Spanned)))
		p.printStmt(s)
	}
	p.indent--
	// See printFieldedDecl: no drain before "}" here either.
	p.writeIndent()
	p.out.WriteString("}\n")
}

func (p *printer) printStmt(s syntax.Stmt) {
	switch s := s.(type) {
	case *syntax.Binding:
		prefix := s.Name + " = "
		if s.Type != nil {
			prefix = s.Name + ": " + printType(s.Type) + " = "
		}
		p.printValueLine(prefix, s.Value, startLine(s))
	case *syntax.ForStmt:
		line := startLine(s)
		names := s.Var
		if s.Value != "" {
			names += ", " + s.Value
		}
		header := fmt.Sprintf("for %s in %s {", names, printExpr(s.Iterable))
		if s.Label != "" {
			header = s.Label + ": " + header
		}
		p.emitLine(header, line, line)
		p.printBlockBody(s.Body)
	case *syntax.WhileStmt:
		line := startLine(s)
		header := fmt.Sprintf("while %s {", printExpr(s.Cond))
		if s.Label != "" {
			header = s.Label + ": " + header
		}
		p.emitLine(header, line, line)
		p.printBlockBody(s.Body)
	case *syntax.BreakStmt:
		line := startLine(s)
		p.emitLine(jumpText("break", s.Label), line, line)
	case *syntax.ContinueStmt:
		line := startLine(s)
		p.emitLine(jumpText("continue", s.Label), line, line)
	case *syntax.ReturnStmt:
		if s.Value == nil {
			line := startLine(s)
			p.emitLine("return", line, line)
			return
		}
		p.printValueLine("return ", s.Value, startLine(s))
	case *syntax.ExprStmt:
		p.printValueLine("", s.Value, startLine(s))
	case *syntax.ExpectStmt:
		if s.Pattern != nil {
			p.emitLine("expect "+printExpr(s.Cond)+" is "+printPattern(s.Pattern), startLine(s), deepestLine(s.Cond))
			break
		}
		p.printValueLine("expect ", s.Cond, startLine(s))
	case *syntax.IfStmt:
		p.printIfStmt(s)
	case *syntax.MatchStmt:
		p.printMatchStmt(s)
	case *syntax.TransactionStmt:
		line := startLine(s)
		head := "transaction " + s.Capability
		if s.Isolation != "" {
			head += " " + s.Isolation
		}
		if s.Attempts != 0 {
			head += fmt.Sprintf(" retry(attempts: %d)", s.Attempts)
		}
		p.emitLine(head+" {", line, line)
		p.printBlockBody(s.Body)
	}
}

// printValueLine prints prefix+expr as one statement line, except when expr
// is directly a call expression whose single-line rendering would exceed
// maxLineWidth — in that case the call's arguments are split one per line
// with a trailing comma instead (the record_order.fiig case).
func (p *printer) printValueLine(prefix string, expr syntax.Expr, stmtLine int) {
	upper := deepestLine(expr)
	content := prefix + printExpr(expr)
	indentWidth := 4 * p.indent
	if call, ok := expr.(*syntax.CallExpr); ok && indentWidth+len(content) > maxLineWidth {
		p.writeIndent()
		p.out.WriteString(prefix)
		p.printMultilineCall(call)
		p.emitTrailingOnly(upper, stmtLine)
		return
	}
	if lit, ok := expr.(*syntax.BytesLit); ok && len(lit.Segments) > 0 && indentWidth+len(content) > maxLineWidth {
		p.writeIndent()
		p.out.WriteString(prefix)
		p.out.WriteString("<<\n")
		p.indent++
		for _, seg := range lit.Segments {
			p.writeIndent()
			p.out.WriteString(printSegment(seg))
			p.out.WriteString(",\n")
		}
		p.indent--
		p.writeIndent()
		p.out.WriteString(">>")
		p.emitTrailingOnly(stmtLine, upper)
		return
	}
	if lit, ok := expr.(*syntax.ListLit); ok && len(lit.Elems) > 0 && indentWidth+len(content) > maxLineWidth {
		p.writeIndent()
		p.out.WriteString(prefix)
		p.printMultilineList(lit)
		p.emitTrailingOnly(stmtLine, upper)
		return
	}
	p.emitLine(content, stmtLine, upper)
}

// emitTrailingOnly appends a trailing comment (if any lands in range) plus
// the newline after content that printMultilineCall already wrote directly
// to p.out without going through emitLine.
func (p *printer) emitTrailingOnly(fromLine, toLine int) {
	for p.ci < len(p.comments) && p.comments[p.ci].Start.Line >= fromLine && p.comments[p.ci].Start.Line <= toLine {
		p.out.WriteString(" ")
		p.out.WriteString(p.comments[p.ci].Text)
		p.ci++
	}
	p.out.WriteByte('\n')
}

func (p *printer) printMultilineList(lit *syntax.ListLit) {
	p.out.WriteString("[\n")
	p.indent++
	for _, e := range lit.Elems {
		p.writeIndent()
		p.out.WriteString(printExpr(e))
		p.out.WriteString(",\n")
	}
	p.indent--
	p.writeIndent()
	p.out.WriteString("]")
}

func (p *printer) printMultilineCall(call *syntax.CallExpr) {
	p.out.WriteString(printExpr(call.Callee))
	p.out.WriteString("(\n")
	p.indent++
	for _, a := range call.Args {
		p.writeIndent()
		p.out.WriteString(printArg(a))
		p.out.WriteString(",\n")
	}
	p.indent--
	p.writeIndent()
	p.out.WriteString(")")
}

// deepestLine returns the deepest source line reachable by following only
// concurrent/retry body chains — the only expressions that can span several
// original source lines yet render onto a single output line. For anything
// else it's just the expression's own start line, so callers can use it
// uniformly as the upper end of a [start, deepestLine] trailing-comment
// range without it ever widening that range for an ordinary expression.
func deepestLine(e syntax.Expr) int {
	switch e := e.(type) {
	case *syntax.ConcurrentExpr:
		if b, ok := e.Body.(syntax.ExprBody); ok {
			return deepestLine(b.Expr)
		}
	case *syntax.RetryExpr:
		if b, ok := e.Body.(syntax.ExprBody); ok {
			return deepestLine(b.Expr)
		}
	case *syntax.ListLit:
		// A wrapped literal's element lines belong to its statement, so
		// their trailing comments stay with it.
		return e.End.Line
	case *syntax.BytesLit:
		return e.End.Line
	}
	return startLine(e.(syntax.Spanned))
}

// jumpText renders `break` or `continue`, with its label when it has one.
func jumpText(keyword, label string) string {
	if label == "" {
		return keyword
	}
	return keyword + " " + label
}

func (p *printer) printIfStmt(s *syntax.IfStmt) {
	line := startLine(s)
	p.emitLine(fmt.Sprintf("if %s {", printExpr(s.Cond)), line, line)
	p.printBlockBodyNoClose(s.Then)
	for _, ei := range s.ElseIfs {
		// ElseIf carries no Span of its own (unattested in the corpus —
		// grammar.md notes else/else-if are decided but never used by a real
		// example), so there's no source line to anchor comment placement to;
		// best effort, no trailing-comment support on this line.
		p.writeIndent()
		p.out.WriteString(fmt.Sprintf("} else if %s {\n", printExpr(ei.Cond)))
		p.printBlockBodyNoClose(ei.Then)
	}
	if s.Else != nil {
		p.writeIndent()
		p.out.WriteString("} else {\n")
		p.printBlockBodyNoClose(s.Else)
	}
	p.writeIndent()
	p.out.WriteString("}\n")
}

// printBlockBodyNoClose is printBlockBody without the trailing "}" line,
// for if/else-if/else chains where the next clause's "} else ... {" (or the
// final close) is written by the caller instead.
func (p *printer) printBlockBodyNoClose(b *syntax.Block) {
	p.indent++
	for _, s := range b.Stmts {
		line := startLine(s.(syntax.Spanned))
		p.emitLeading(line)
		p.printStmt(s)
	}
	p.indent--
	// See printFieldedDecl: no drain before the caller's "}"/"} else ..." here either.
}

func (p *printer) printMatchStmt(s *syntax.MatchStmt) {
	line := startLine(s)
	p.emitLine(fmt.Sprintf("match %s {", printExpr(s.Subject)), line, line)
	p.indent++
	for _, arm := range s.Arms {
		al := startLine(arm)
		p.emitLeading(al)
		p.printMatchArm(arm)
	}
	p.indent--
	// See printFieldedDecl: no drain before "}" here either.
	p.writeIndent()
	p.out.WriteString("}\n")
}

func (p *printer) printMatchArm(arm syntax.MatchArm) {
	prefix := printPattern(arm.Pattern) + " -> "
	switch body := arm.Body.(type) {
	case syntax.ExprBody:
		line := startLine(arm)
		p.emitLine(prefix+printExpr(body.Expr), line, line)
	case *syntax.Block:
		// Deliberately not emitLine for the opening brace: a block-bodied arm
		// attested in the corpus (checkout.fiig) is written as one compact
		// source line — `pattern -> { stmt } // comment` — so the trailing
		// comment's source line equals the arm's *inner statement's* line,
		// not a separate line of its own. Leaving the opening-brace line as a
		// plain write lets the inner statement's own per-line comment check
		// (inside printBlockBody) claim it naturally, attached to the
		// statement it actually follows instead of the brace above it.
		p.writeIndent()
		p.out.WriteString(prefix + "{\n")
		p.printBlockBody(body)
	}
}

// ---- patterns --------------------------------------------------------

func printPattern(pat syntax.Pattern) string {
	switch pat := pat.(type) {
	case *syntax.WildcardPattern:
		return "_"
	case *syntax.NamePattern:
		return strings.Join(pat.Path, ".")
	case *syntax.LiteralPattern:
		switch pat.Kind {
		case "int":
			return strconv.FormatInt(pat.Int, 10)
		case "string":
			return printStringLit(pat.Lit)
		}
		return strconv.FormatBool(pat.Bool)
	case *syntax.ConstructorPattern:
		parts := make([]string, len(pat.Args))
		for i, a := range pat.Args {
			parts[i] = printPattern(a)
		}
		return strings.Join(pat.Path, ".") + "(" + strings.Join(parts, ", ") + ")"
	case *syntax.BytesPattern:
		return "<<" + printSegments(pat.Segments) + ">>"
	default:
		return "?"
	}
}

// printSegments renders ADR 0058 segments: `name: encoding` or
// `literal: encoding` in a pattern, `value: encoding` in an expression.
func printSegments(segs []syntax.Segment) string {
	parts := make([]string, len(segs))
	for i, seg := range segs {
		parts[i] = printSegment(seg)
	}
	return strings.Join(parts, ", ")
}

func printSegment(seg syntax.Segment) string {
	left := seg.Name
	switch {
	case seg.Value != nil:
		left = printExpr(seg.Value)
	case seg.Literal != nil:
		left = printExpr(seg.Literal)
	}
	enc := seg.Encoding.Name
	if seg.Encoding.Size != nil {
		enc += "(" + printExpr(seg.Encoding.Size) + ")"
	}
	return left + ": " + enc
}

// ---- expressions -----------------------------------------------------

// Expr renders an expression the way the formatter prints it: the
// canonical spelling the checker and registry use for a header constant
// (ADR 0046).
func Expr(e syntax.Expr) string { return printExpr(e) }

func printExpr(e syntax.Expr) string {
	switch e := e.(type) {
	case *syntax.Ident:
		return e.Name
	case *syntax.IntLit:
		return e.Text
	case *syntax.FloatLit:
		return e.Text
	case *syntax.StringLit:
		return printStringLit(e)
	case *syntax.FieldAccess:
		return printExpr(e.Target) + "." + e.Field
	case *syntax.CallExpr:
		return printExpr(e.Callee) + "(" + printArgs(e.Args) + ")"
	case *syntax.NotExpr:
		return "not " + printExpr(e.Operand)
	case *syntax.NegExpr:
		return "-" + printExpr(e.Operand)
	case *syntax.BinaryExpr:
		return printExpr(e.Left) + " " + string(e.Op) + " " + printExpr(e.Right)
	case *syntax.TryExpr:
		return printExpr(e.Operand) + "?"
	case *syntax.ParenExpr:
		return "(" + printExpr(e.Inner) + ")"
	case *syntax.BytesLit:
		return "<<" + printSegments(e.Segments) + ">>"
	case *syntax.ListLit:
		parts := make([]string, len(e.Elems))
		for i, el := range e.Elems {
			parts[i] = printExpr(el)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *syntax.ConcurrentExpr:
		return printConcurrentExpr(e)
	case *syntax.MapLit:
		if len(e.Entries) == 0 {
			return "[:]"
		}
		parts := make([]string, len(e.Entries))
		for i, en := range e.Entries {
			parts[i] = printExpr(en.Key) + ": " + printExpr(en.Value)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *syntax.SortedExpr:
		// The old header `descending` is printed on the key (ADR 0093 §4).
		keys := make([]string, len(e.Keys))
		for i, k := range e.Keys {
			keys[i] = printExpr(k.Key)
			if k.Descending {
				keys[i] += " descending"
			}
		}
		return fmt.Sprintf("sorted(%s in %s): %s", e.Var, printExpr(e.Iterable), strings.Join(keys, " then "))
	case *syntax.ListForm:
		return fmt.Sprintf("%s(%s in %s): %s", e.Form, e.Var, printExpr(e.Iterable), printExpr(e.Body))
	case *syntax.RetryExpr:
		return fmt.Sprintf("retry(attempts: %s): %s", printExpr(e.Attempts), printBodyInline(e.Body))
	default:
		return "?"
	}
}

func printConcurrentExpr(e *syntax.ConcurrentExpr) string {
	s := fmt.Sprintf("concurrent(%s in %s", e.Var, printExpr(e.Iterable))
	if e.Limit != nil {
		if e.Limit.Unbounded {
			s += ", limit: unbounded"
		} else {
			s += ", limit: " + printExpr(e.Limit.Value)
		}
	}
	if e.PreserveOrder {
		s += ", preserveOrder"
	}
	s += "): " + printBodyInline(e.Body)
	return s
}

// printBodyInline renders a match-arm/concurrent/retry Body as a single-line
// string. Every attested concurrent/retry body in the corpus is a bare
// expression; a Block body is legal grammar but unattested here, so it gets
// a best-effort single-line rendering (statements joined with "; ") rather
// than the multi-line treatment match-arm blocks get, since nothing
// exercises it.
func printBodyInline(b syntax.Body) string {
	switch b := b.(type) {
	case syntax.ExprBody:
		return printExpr(b.Expr)
	case *syntax.Block:
		parts := make([]string, len(b.Stmts))
		for i, s := range b.Stmts {
			parts[i] = printStmtInline(s)
		}
		return "{ " + strings.Join(parts, "; ") + " }"
	default:
		return "?"
	}
}

// printStmtInline is the best-effort single-line statement rendering
// printBodyInline's unattested Block fallback needs; it doesn't handle
// nested if/match, since those can't sensibly collapse to one line and
// nothing in the corpus requires it.
func printStmtInline(s syntax.Stmt) string {
	switch s := s.(type) {
	case *syntax.Binding:
		return s.Name + " = " + printExpr(s.Value)
	case *syntax.ReturnStmt:
		if s.Value == nil {
			return "return"
		}
		return "return " + printExpr(s.Value)
	case *syntax.ExprStmt:
		return printExpr(s.Value)
	default:
		return "?"
	}
}

func printArgs(args []syntax.Arg) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = printArg(a)
	}
	return strings.Join(parts, ", ")
}

func printArg(a syntax.Arg) string {
	if a.Name == "" {
		return printExpr(a.Value)
	}
	return a.Name + ": " + printExpr(a.Value)
}

// printStringLit re-escapes a string literal's decoded text/interpolation
// parts back into source form — the lexer resolves \", \\, \n, \t, \r, \$ into
// literal characters when it scans a string (scanStringPart), so printing
// has to reverse that, not just echo the parts back.
func printStringLit(s *syntax.StringLit) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, part := range s.Parts {
		if part.Interp != nil {
			sb.WriteString("${")
			sb.WriteString(printExpr(part.Interp))
			sb.WriteString("}")
			continue
		}
		sb.WriteString(escapeStringText(part.Text))
	}
	sb.WriteByte('"')
	return sb.String()
}

func escapeStringText(s string) string {
	var sb strings.Builder
	for i, r := range s {
		switch r {
		case '\r':
			sb.WriteString(`\r`)
		case '$':
			// Only "${" needs escaping (ADR 0061); a lone '$' is plain text.
			if strings.HasPrefix(s[i+1:], "{") {
				sb.WriteString(`\$`)
			} else {
				sb.WriteRune(r)
			}
		case '\\':
			sb.WriteString(`\\`)
		case '"':
			sb.WriteString(`\"`)
		case '\n':
			sb.WriteString(`\n`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
