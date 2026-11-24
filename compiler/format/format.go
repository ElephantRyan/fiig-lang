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
// a call expression, gets that call's arguments split one-per-line instead
// (attested by order-processing/gateways/record_order.fiig), with no comma
// after the last (ADR 0147). A list literal
// (ADR 0057) and a binary expression (ADR 0058) wrap the same way, one
// element or segment per line. Nothing else wraps.
const maxLineWidth = 100

// Format parses src and renders it back out in Fiig's canonical style.
// Returns the parse errors instead of formatted text if src doesn't parse.
// A second public declaration (G1, ADR 0054) doesn't count: the parser
// still builds the whole tree for it, so there is nothing to lose by
// formatting, and the checker is what reports it. Nor does a comma before a
// closing bracket (ADR 0147) or `preserveOrder` (ADR 0148), which the parse
// skips and the output drops.
func Format(src string) (string, []parser.Error) {
	out, errs, _ := FormatTyped(src, nil)
	return out, errs
}

// FormatTyped is Format with the checker's verdict on each `or` in src,
// keyed by its byte offset: true for a default, written `??`, false for a
// boolean one, written `||` (ADR 0142 §5). It also returns where each `or`
// with no verdict starts, since it can only be left as written.
func FormatTyped(src string, ors map[int]bool) (string, []parser.Error, []lexer.Pos) {
	file, comments, errs := parser.ParseWithComments(src)
	for _, e := range errs {
		if e.Rule != "G1" && !parser.FormatFixes(e) {
			return "", errs, nil
		}
	}
	var unknown []lexer.Pos
	syntax.Inspect(file, func(n any) {
		b, ok := n.(*syntax.BinaryExpr)
		if !ok || b.Op != syntax.OpOr || b.Symbol {
			return
		}
		if isDefault, ok := ors[b.Start.Offset]; ok {
			b.OrKnown, b.OrDefault = true, isDefault
			return
		}
		unknown = append(unknown, b.Start)
	})
	p := &printer{comments: comments}
	p.printFile(file)
	return blankAfterBlocks(dropTrailingCommas(p.out.String())), nil, unknown
}

// dropTrailingCommas removes each `,` whose next token is `)`, `]`, `}`, or
// `>` (ADR 0147). The printer ends every line of a list it splits with `,`,
// so this is where the last one goes; it reads src with the lexer, so a
// comma in a string literal or a comment is never touched.
func dropTrailingCommas(src string) string {
	toks := lexer.New(src).AllTokens()
	var b strings.Builder
	from := 0
	for i := 0; i+1 < len(toks); i++ {
		if toks[i].Kind != lexer.Comma {
			continue
		}
		switch toks[i+1].Kind {
		case lexer.RParen, lexer.RBracket, lexer.RBrace, lexer.RAngle:
			at := toks[i].Start.Offset
			b.WriteString(src[from:at])
			from = at + 1
		}
	}
	b.WriteString(src[from:])
	return b.String()
}

// blankAfterBlocks puts a blank line after a line that only closes a block
// (`}`, or a block arm's `},` before another arm),
// so a block stands apart from what follows it. It doesn't when what
// follows closes something too (`}`, `)`, `]`), continues the block
// (`else`), or is already a blank line. It reads src with the lexer, so a
// line inside a string literal is never one it changes or counts.
func blankAfterBlocks(src string) string {
	lines := strings.Split(src, "\n")
	inString := make([]bool, len(lines)+2) // a line that starts inside a string literal
	first := make([]lexer.Kind, len(lines)+2)
	closesOnly := make([]bool, len(lines)+2)
	count := make([]int, len(lines)+2)
	for _, t := range lexer.New(src).AllTokens() {
		if t.Kind == lexer.EOF {
			break
		}
		if t.Kind == lexer.StringText {
			for l := t.Start.Line + 1; l <= t.End.Line && l < len(inString); l++ {
				inString[l] = true
			}
		}
		l := t.Start.Line
		if count[l] == 0 {
			first[l] = t.Kind
		}
		count[l]++
		// `},` closes a block arm (ADR 0145 §2), as `}` closes a block.
		closesOnly[l] = count[l] == 1 && t.Kind == lexer.RBrace ||
			count[l] == 2 && t.Kind == lexer.Comma && first[l] == lexer.RBrace
	}
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
		n := i + 2 // the next line's number; lexer lines count from 1
		if i+1 >= len(lines) || !closesOnly[i+1] || inString[i+1] || inString[n] {
			continue
		}
		next := strings.TrimSpace(lines[i+1])
		if next == "" {
			continue
		}
		if count[n] > 0 {
			switch first[n] {
			case lexer.RBrace, lexer.RParen, lexer.RBracket, lexer.KwElse:
				continue
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

type printer struct {
	out      strings.Builder
	indent   int
	comments []lexer.Comment
	ci       int // index of the next not-yet-emitted comment
	// after is what follows the next block's closing `}`: `,` for a block
	// arm (ADR 0145 §2). A block takes it as it opens, so blocks inside it
	// close plain.
	after string
}

// takeAfter returns and clears what follows the next closing `}`.
func (p *printer) takeAfter() string {
	a := p.after
	p.after = ""
	return a
}

func (p *printer) writeIndent() {
	p.out.WriteString(strings.Repeat("    ", p.indent))
}

// emitLeading prints, each on its own line, every not-yet-emitted comment
// that starts strictly before beforeLine — a standalone comment that
// appeared before whatever comes next, rather than trailing something
// already printed.
//
// A blank line between two comments in the source stays, so comment
// paragraphs stay apart.
func (p *printer) emitLeading(beforeLine int) {
	prevEnd := -1
	for p.ci < len(p.comments) && p.comments[p.ci].Start.Line < beforeLine {
		if prevEnd >= 0 && p.comments[p.ci].Start.Line > prevEnd+1 {
			p.out.WriteByte('\n')
		}
		prevEnd = p.comments[p.ci].End.Line
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

// endLine is the line n ends on: for a block or a declaration, its "}".
func endLine(n syntax.Spanned) int {
	_, end := n.Bounds()
	return end.Line
}

// emitInside prints, one level in, the comments left between a body's last
// item and its "}" on closeLine. Only lines before the brace's are taken,
// so a comment after the "}" stays with whatever follows it.
func (p *printer) emitInside(closeLine int) {
	p.indent++
	p.emitLeading(closeLine)
	p.indent--
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
	sb.WriteString(";") // ADR 0145 §1
	line := startLine(imp)
	p.emitLine(sb.String(), line, line)
}

// ---- declarations ------------------------------------------------------

func (p *printer) printDecl(d syntax.Decl) {
	switch d := d.(type) {
	case *syntax.ModelDecl:
		p.printFieldedDecl("model "+d.Name, fieldsToSpanned(d.Fields), startLine(d), endLine(d), func(i int) string {
			return printField(d.Fields[i]) + ";"
		})
	case *syntax.EnumDecl:
		if d.Header != nil {
			rows := enumRows(d.Variants)
			p.printFieldedDecl("enum "+d.Name+printEnumHeader(d.Header), variantsToSpanned(d.Variants), startLine(d), endLine(d), func(i int) string {
				return rows[i] + ","
			})
			break
		}
		p.printFieldedDecl("enum "+d.Name, variantsToSpanned(d.Variants), startLine(d), endLine(d), func(i int) string {
			return printEnumVariant(d.Variants[i]) + ","
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
	case *syntax.ApplicationDecl:
		p.printApplicationDecl(d)
	case *syntax.PrincipalDecl:
		p.printPrincipal(d)
	case *syntax.InterfaceDecl:
		p.printFieldedDecl("interface "+d.Name, membersToSpanned(d.Members), startLine(d), endLine(d), func(i int) string {
			m := d.Members[i]
			return fmt.Sprintf("%s%s -> %s;", m.Name, printParams(m.Params), printType(m.ReturnType))
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
		header := "wire " + d.Name
		// ADR 0142 §4: a condition is parenthesised.
		if d.When != nil {
			when := printExpr(d.When)
			if _, paren := d.When.(*syntax.ParenExpr); !paren {
				when = "(" + when + ")"
			}
			header += " when " + when
		}
		p.printFieldedDecl(header, spanned, startLine(d), endLine(d), func(i int) string {
			f := d.Fields[i]
			line := fmt.Sprintf("%s: %s from %s", f.Name, printType(f.Type), configSources(f))
			if f.Startup != "" {
				line = fmt.Sprintf("%s: %s from startup %s", f.Name, printType(f.Type), strconv.Quote(f.Startup))
			}
			if f.Default != nil {
				line += " = " + printExpr(f.Default)
			}
			return line + ";"
		})
	}
}

// configSources is a field's sources in the order written (ADR 0140 §1).
func configSources(f syntax.ConfigField) string {
	env := "env " + strconv.Quote(f.Env)
	if f.Setting == "" {
		return env
	}
	setting := "setting " + strconv.Quote(f.Setting)
	if f.OnRestart {
		setting += " on restart"
	}
	switch {
	case f.Env == "":
		return setting
	case f.SettingFirst:
		return setting + " ?? " + env
	}
	return env + " ?? " + setting
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
	p.emitInside(endLine(d))
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
		p.printValueLineEnd(v.Name+": ", v.Value, vLine, ";")
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
		var clauses []string
		if m.Requires != "" {
			clauses = append(clauses, "requires "+m.Requires)
		}
		mLine := startLine(m)
		tail := " {"
		if m.Body == nil {
			tail = ";" // a native leaf (ADR 0049 §2.4) is an item (ADR 0145 §1)
		}
		p.printSignature("formula "+m.Name, m.Params, " -> "+printType(m.ReturnType), clauses, tail, mLine)
		if m.Body != nil {
			p.printProducingBody(m.Body)
		}
	}
	p.indent--
	p.emitInside(endLine(d))
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
			p.printSignature(h.Name, h.Params, " -> "+printType(h.ReturnType), nil, " {", line)
			p.printProducingBody(h.Body)
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
func (p *printer) printFieldedDecl(header string, items []syntax.Spanned, headerLine, closeLine int, renderItem func(i int) string) {
	p.emitLine(header+" {", headerLine, headerLine)
	p.indent++
	for i, item := range items {
		line := startLine(item)
		p.emitLeading(line)
		p.emitLine(renderItem(i), line, line)
	}
	p.indent--
	p.emitInside(closeLine)
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
	p.printSignature(keyword+" "+name, params, " -> "+printType(ret), nil, " {", line)
	p.printProducingBody(body)
}

// printGatewayDecl prints a gateway's `requires Capability` and
// `idempotent` clauses on the signature's line when the whole header fits,
// and otherwise under it (printClauses). `{` is never on a line of its own.
func (p *printer) printGatewayDecl(d *syntax.GatewayDecl) {
	sigLine := startLine(d)
	var clauses []string
	if d.Requires != "" {
		clauses = append(clauses, "requires "+d.Requires)
	}
	if d.Idempotent {
		clauses = append(clauses, "idempotent")
	}
	p.printSignature("gateway "+d.Name, d.Params, " -> "+printType(d.ReturnType), clauses, " {", sigLine)
	p.printProducingBody(d.Body)
}

// printSignature prints a declaration's header: lead (`gateway name`), its
// parameters, after (` -> Type`), its clauses (`requires X`), then tail
// (` {`, or nothing for a formula without a body). It takes the first shape
// that fits maxLineWidth:
//
//  1. all of it on one line;
//  2. the signature on one line and the clauses under it (printClauses);
//  3. the name and parameters on one line, and `-> Type` under it as the
//     first of the clauses;
//  4. the parameters one per line, `)` ending the last, then `-> Type` and
//     the clauses under them.
//
// `{` ends the last line in every shape, never a line of its own, and a
// wrapped result type sits with the clauses at two indents, apart from the
// parameters and the body at one.
func (p *printer) printSignature(lead string, params []syntax.Param, after string, clauses []string, tail string, line int) {
	sig := lead + printParams(params) + after
	lines := make([]int, len(clauses)+1)
	for i := range lines {
		lines[i] = line
	}
	if p.fits(joinClauses(sig, clauses, tail)) || (len(params) == 0 && len(clauses) == 0) {
		p.emitLine(joinClauses(sig, clauses, tail), line, line)
		return
	}
	if len(params) == 0 || (len(clauses) > 0 && p.fits(sig)) {
		p.emitLine(sig, line, line)
		p.printClauses(clauses, tail, lines)
		return
	}
	withResult := append([]string{strings.TrimSpace(after)}, clauses...)
	if head := lead + printParams(params); p.fits(head) {
		p.emitLine(head, line, line)
		p.printClauses(withResult, tail, lines)
		return
	}
	p.emitLine(lead+"(", line, line)
	p.indent++
	for i, prm := range params {
		end := ","
		if i == len(params)-1 {
			end = ")"
		}
		p.emitLine(fmt.Sprintf("%s: %s%s", prm.Name, printType(prm.Type), end), line, line)
	}
	p.indent--
	p.printClauses(withResult, tail, lines)
}

// joinClauses is a header with its clauses on one line, then tail.
func joinClauses(head string, clauses []string, tail string) string {
	if len(clauses) == 0 {
		return head + tail
	}
	return head + " " + strings.Join(clauses, " ") + tail
}

// fits reports whether line fits within maxLineWidth at the current indent.
func (p *printer) fits(line string) bool {
	return len(strings.Repeat("    ", p.indent))+len(line) <= maxLineWidth
}

// printClauses prints a header's clauses that didn't fit on its line, one
// per line at two indents, so they stand apart from the body at one; the
// last carries tail, the header's `{` when it has a body. lines are the
// clauses' source lines, for their comments.
func (p *printer) printClauses(clauses []string, tail string, lines []int) {
	p.indent += 2
	for i, c := range clauses {
		if i == len(clauses)-1 {
			c += tail
		}
		p.emitLine(c, lines[i], lines[i])
	}
	p.indent -= 2
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
	// Principals sit together, then one blank line before the routes, as a
	// test's fakes do before its body.
	for _, pr := range d.Principals {
		p.emitLeading(startLine(pr))
		p.printPrincipal(pr)
	}
	for _, r := range d.PrincipalRefs {
		p.emitLeading(startLine(r))
		ref := "wire " + r.Name()
		if r.As != "" {
			ref += " as " + r.As
		}
		p.emitLine(ref+";", startLine(r), startLine(r))
	}
	if d.HasPrincipals() && len(d.Routes) > 0 {
		p.out.WriteByte('\n')
	}
	for _, r := range d.Routes {
		p.emitLeading(startLine(r))
		p.printRoute(r)
	}
	p.indent--
	p.emitInside(endLine(d))
	p.writeIndent()
	p.out.WriteString("}\n")
}

// printApplicationDecl prints `application Name { serve [role] { adapter... } }`
// (ADR 0132), one adapter to a line.
func (p *printer) printApplicationDecl(d *syntax.ApplicationDecl) {
	line := startLine(d)
	p.emitLine("application "+d.Name+" {", line, line)
	p.indent++
	for _, b := range d.Serves {
		bl := startLine(b)
		p.emitLeading(bl)
		head := "serve"
		if b.Role != "" {
			head += " " + b.Role
		}
		p.emitLine(head+" {", bl, bl)
		p.indent++
		for _, a := range b.Adapters {
			al := startLine(a)
			p.emitLeading(al)
			p.emitLine(strings.Join(a.Path, ".")+";", al, al)
		}
		p.indent--
		p.emitInside(endLine(b))
		p.writeIndent()
		p.out.WriteString("}\n")
	}
	p.indent--
	p.emitInside(endLine(d))
	p.writeIndent()
	p.out.WriteString("}\n")
}

func (p *printer) printRoute(r *syntax.Route) {
	line := startLine(r)
	args := make([]string, len(r.Args))
	for i, a := range r.Args {
		args[i] = printRouteArg(a)
	}
	public := ""
	if r.Public {
		public = " public"
	}
	head := fmt.Sprintf("%s %s%s -> %s", r.Verb, strconv.Quote(r.Path), public, r.Workflow.Name)
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
	// The media clauses (consumes first) join the route's line when the
	// whole of it fits, and otherwise go under it (printClauses).
	var clauses []string
	var clauseLines []int
	if r.Consumes != nil {
		clauses = append(clauses, "consumes "+strconv.Quote(r.Consumes.Text))
		clauseLines = append(clauseLines, startLine(r.Consumes))
	}
	if r.Produces != nil {
		clauses = append(clauses, "produces "+strconv.Quote(r.Produces.Text))
		clauseLines = append(clauseLines, startLine(r.Produces))
	}
	tail := ";" // ADR 0145 §1
	if len(r.Mapping) > 0 {
		tail = " {"
	}
	call := head + "(" + strings.Join(args, ", ") + ")"
	brace := tail
	if len(clauses) > 0 {
		brace = ""
	}
	if whole := joinClauses(call, clauses, tail); len(clauses) > 0 && p.fits(whole) {
		p.emitLine(whole, line, line)
		clauses = nil
	} else if len(args) == 0 || p.fits(call+brace) {
		p.emitLine(call+brace, line, line)
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
	if len(clauses) > 0 {
		p.printClauses(clauses, tail, clauseLines)
	}
	if len(r.Mapping) == 0 {
		return
	}
	p.indent++
	for _, arm := range r.Mapping {
		al := startLine(arm)
		p.emitLeading(al)
		content := fmt.Sprintf("%s -> %s,", printPattern(arm.Pattern), printResponseValue(arm.Value))
		p.emitLine(content, al, al)
	}
	p.indent--
	p.emitInside(endLine(r))
	p.writeIndent()
	p.out.WriteString("}\n")
}

// printPrincipal prints `principal name: Type from ... by ... else ...`
// (ADR 0137 §1) on one line, or, when that is too long, with `by` and
// `else` on a continuation line.
func (p *printer) printPrincipal(pr *syntax.PrincipalDecl) {
	line := startLine(pr)
	head := "wire " + pr.Name + ": " + printType(pr.Type)
	if pr.From != nil {
		head += " from " + pr.From.Name
	} else {
		head += " from " + pr.Source + " " + strconv.Quote(pr.SourceName)
		if pr.Scheme != "" {
			head += " scheme " + strconv.Quote(pr.Scheme)
		}
	}
	tail := "by " + pr.By.Name() + " else " + pr.Else.Name() + ";"
	if len(strings.Repeat("    ", p.indent))+len(head)+1+len(tail) <= maxLineWidth {
		p.emitLine(head+" "+tail, line, line)
		return
	}
	p.emitLine(head, line, line)
	p.indent++
	p.emitLine(tail, line, line)
	p.indent--
}

func printRouteArg(a syntax.RouteArg) string {
	if a.Principal {
		return a.Name
	}
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
	after := p.takeAfter()
	p.indent++
	for _, s := range b.Stmts {
		p.emitLeading(startLine(s.(syntax.Spanned)))
		p.printStmt(s)
	}
	p.indent--
	p.emitInside(endLine(b))
	p.writeIndent()
	p.out.WriteString("}" + after + "\n")
}

// printProducingBody prints the body of a declaration that returns a value
// (ADR 0142 §2): its last statement, a bare expression or `match` that was
// the value, is written with `return` (printProducingStmt).
func (p *printer) printProducingBody(b *syntax.Block) {
	after := p.takeAfter()
	p.indent++
	for i, s := range b.Stmts {
		p.emitLeading(startLine(s.(syntax.Spanned)))
		if i == len(b.Stmts)-1 {
			p.printProducingStmt(s)
		} else {
			p.printStmt(s)
		}
	}
	p.indent--
	p.emitInside(endLine(b))
	p.writeIndent()
	p.out.WriteString("}" + after + "\n")
}

// printProducingStmt prints the statement whose value a block produces. A
// bare expression gets `return`. A bare `match` becomes `return match` when
// every one of its block arms ends in a value too; otherwise it stays a
// statement, its arms written as producing blocks.
func (p *printer) printProducingStmt(s syntax.Stmt) {
	switch s := s.(type) {
	case *syntax.ExprStmt:
		p.printValueLineEnd("return ", s.Value, startLine(s), ";")
	case *syntax.MatchStmt:
		prefix := ""
		if producesThroughout(s) {
			prefix = "return "
		}
		p.printMatchArms(prefix, s, true)
	default:
		p.printStmt(s)
	}
}

// producesThroughout reports whether every block arm of m ends in a value,
// so m can be written `return match` (ADR 0142 §2).
func producesThroughout(m *syntax.MatchStmt) bool {
	for _, arm := range m.Arms {
		b, ok := arm.Body.(*syntax.Block)
		if !ok {
			continue
		}
		if len(b.Stmts) == 0 {
			return false
		}
		switch last := b.Stmts[len(b.Stmts)-1].(type) {
		case *syntax.ReturnStmt, *syntax.ExprStmt:
		case *syntax.MatchStmt:
			if !producesThroughout(last) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// condition is a condition or loop head in its parentheses (ADR 0142 §4),
// once, even when the source already had them.
func condition(e syntax.Expr) string {
	if _, ok := e.(*syntax.ParenExpr); ok {
		return printExpr(e)
	}
	return "(" + printExpr(e) + ")"
}

func (p *printer) printStmt(s syntax.Stmt) {
	switch s := s.(type) {
	case *syntax.Binding:
		prefix := s.Name + " = "
		if s.Type != nil {
			prefix = s.Name + ": " + printType(s.Type) + " = "
		}
		p.printValueLineEnd(prefix, s.Value, startLine(s), ";")
	case *syntax.ForStmt:
		line := startLine(s)
		names := s.Var
		if s.Value != "" {
			names += ", " + s.Value
		}
		header := fmt.Sprintf("for (%s in %s) {", names, printExpr(s.Iterable))
		if s.Label != "" {
			header = s.Label + ": " + header
		}
		p.emitLine(header, line, line)
		p.printBlockBody(s.Body)
	case *syntax.WhileStmt:
		line := startLine(s)
		header := fmt.Sprintf("while %s {", condition(s.Cond))
		if s.Label != "" {
			header = s.Label + ": " + header
		}
		p.emitLine(header, line, line)
		p.printBlockBody(s.Body)
	case *syntax.BreakStmt:
		line := startLine(s)
		p.emitLine(jumpText("break", s.Label)+";", line, line)
	case *syntax.ContinueStmt:
		line := startLine(s)
		p.emitLine(jumpText("continue", s.Label)+";", line, line)
	case *syntax.ReturnStmt:
		if s.Value == nil {
			line := startLine(s)
			p.emitLine("return;", line, line)
			return
		}
		p.printValueLineEnd("return ", s.Value, startLine(s), ";")
	case *syntax.ExprStmt:
		p.printValueLineEnd("", s.Value, startLine(s), ";")
	case *syntax.ExpectStmt:
		if s.Pattern != nil {
			p.emitLine("expect "+printExpr(s.Cond)+" is "+printPattern(s.Pattern)+";", startLine(s), deepestLine(s.Cond))
			break
		}
		p.printValueLineEnd("expect ", s.Cond, startLine(s), ";")
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

// printValueLine prints prefix+expr as one statement line, except when that
// would exceed maxLineWidth and expr can wrap at an opening `(` or `[`
// (canWrap): then it wraps there, one argument or element per line with a
// trailing comma (the record_order.fiig case), and so on inward.
func (p *printer) printValueLine(prefix string, expr syntax.Expr, stmtLine int) {
	p.printValueLineEnd(prefix, expr, stmtLine, "")
}

// printValueLineEnd is printValueLine for a statement, which ends with end,
// `;` (ADR 0142 §1). A match used as a value ends with its `}` instead.
func (p *printer) printValueLineEnd(prefix string, expr syntax.Expr, stmtLine int, end string) {
	if m, ok := expr.(*syntax.MatchExpr); ok {
		p.printMatch(prefix, m.Match)
		return
	}
	upper := deepestLine(expr)
	content := prefix + printExpr(expr) + end
	indentWidth := 4 * p.indent
	if _, bytes := expr.(*syntax.BytesLit); !bytes && indentWidth+len(content) > maxLineWidth && canWrap(expr) {
		p.writeIndent()
		p.wrapValue(prefix, expr)
		p.out.WriteString(end)
		p.emitTrailingOnly(stmtLine, upper)
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
		p.out.WriteString(">>" + end)
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
		p.printWrappedValue("", e)
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
		p.printWrappedValue(printArgName(a), a.Value)
		p.out.WriteString(",\n")
	}
	p.indent--
	p.writeIndent()
	p.out.WriteString(")")
}

// printWrappedValue writes prefix and e, at the current indent, as one
// argument or element of a call or list being wrapped. When that line
// would be too long and e is itself a call (or `call?`) or a list, e wraps
// the same way, one level further in, so no nested call runs past
// maxLineWidth (Hearsay: a 12-field model inside `Ok(...)` printed as one
// 575-character line).
func (p *printer) printWrappedValue(prefix string, e syntax.Expr) {
	flat := prefix + printExpr(e)
	if 4*p.indent+len(flat)+1 <= maxLineWidth || !canWrap(e) {
		p.out.WriteString(flat)
		return
	}
	p.wrapValue(prefix, e)
}

// canWrap reports whether e has an opening `(` or `[` to wrap at: a call
// with arguments, a list with elements, either followed by `?`, or a list
// form (`mapErr(e in r): Wrap(e)`) whose body can wrap.
func canWrap(e syntax.Expr) bool {
	switch e := e.(type) {
	case *syntax.CallExpr:
		return len(e.Args) > 0
	case *syntax.ListLit:
		return len(e.Elems) > 0
	case *syntax.TryExpr:
		return canWrap(e.Operand)
	case *syntax.ListForm:
		return canWrap(e.Body)
	}
	return false
}

// wrapValue writes prefix and e wrapped at e's outermost opening `(` or
// `[`, for an e that canWrap. A list form breaks after its head's `:`
// instead, and its body goes on the next line at two indents, wrapping
// there only if it still doesn't fit (`mapErr(e in r):` then `Wrap(e)?`).
func (p *printer) wrapValue(prefix string, e syntax.Expr) {
	switch e := e.(type) {
	case *syntax.CallExpr:
		p.out.WriteString(prefix)
		p.printMultilineCall(e)
	case *syntax.ListLit:
		p.out.WriteString(prefix)
		p.printMultilineList(e)
	case *syntax.TryExpr:
		p.wrapValue(prefix, e.Operand)
		p.out.WriteString("?")
	case *syntax.ListForm:
		fmt.Fprintf(&p.out, "%s%s(%s in %s):", prefix, e.Form, e.Var, printExpr(e.Iterable))
		p.out.WriteByte('\n')
		p.indent += 2
		p.writeIndent()
		p.printWrappedValue("", e.Body)
		p.indent -= 2
	}
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
	p.emitLine(fmt.Sprintf("if %s {", condition(s.Cond)), line, line)
	p.printBlockBodyNoClose(s.Then)
	// A "} else ... {" line is where the block before it closed, so a
	// comment trailing it in the source trails it here.
	closed := endLine(s.Then)
	for _, ei := range s.ElseIfs {
		p.emitLine(fmt.Sprintf("} else if %s {", condition(ei.Cond)), closed, closed)
		p.printBlockBodyNoClose(ei.Then)
		closed = endLine(ei.Then)
	}
	if s.Else != nil {
		p.emitLine("} else {", closed, closed)
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
	p.emitInside(endLine(b))
}

func (p *printer) printMatchStmt(s *syntax.MatchStmt) {
	p.printMatch("", s)
}

// printMatch prints a match after prefix: "" for a match statement, or
// `return ` or `name = ` for a match used as a value (ADR 0142 §2).
func (p *printer) printMatch(prefix string, s *syntax.MatchStmt) {
	p.printMatchArms(prefix, s, false)
}

// printMatchArms is printMatch, with each block arm printed as a producing
// body when producing: the arms of the match that is a declaration's value.
func (p *printer) printMatchArms(prefix string, s *syntax.MatchStmt, producing bool) {
	after := p.takeAfter()
	line := startLine(s)
	p.emitLine(fmt.Sprintf("%smatch %s {", prefix, condition(s.Subject)), line, line)
	p.indent++
	for _, arm := range s.Arms {
		al := startLine(arm)
		p.emitLeading(al)
		if prefix == "return " {
			arm = p.shortArm(arm, producing)
		}
		p.printMatchArm(arm, producing)
	}
	p.indent--
	p.emitInside(endLine(s))
	p.writeIndent()
	p.out.WriteString("}" + after + "\n")
}

// shortArm writes an arm of a `return match` whose block only returns a
// value as that value: `None -> { return Err(e); }` is `None -> Err(e)`,
// since a `return` there leaves the function with the value the arm would
// have given (ADR 0142 §2). In a binding's match the `return` leaves the
// function instead, so only `return match` arms are shortened. A comment
// after the `return` follows the shorter arm; an arm with any other comment
// keeps its block, and so does a `match` value, which an arm can't be, and
// a value too wide for one line that has no `(` to wrap at.
func (p *printer) shortArm(arm syntax.MatchArm, producing bool) syntax.MatchArm {
	b, ok := arm.Body.(*syntax.Block)
	if !ok || len(b.Stmts) != 1 {
		return arm
	}
	var value syntax.Expr
	kept := 0
	switch s := b.Stmts[0].(type) {
	case *syntax.ReturnStmt:
		value, kept = s.Value, startLine(s)
	case *syntax.ExprStmt:
		if producing {
			value, kept = s.Value, startLine(s)
		}
	}
	if value == nil {
		return arm
	}
	if _, isMatch := value.(*syntax.MatchExpr); isMatch {
		return arm
	}
	short := printPattern(arm.Pattern) + " -> " + printExpr(value) + ","
	if 4*p.indent+len(short) > maxLineWidth && !canWrap(value) {
		return arm
	}
	from, to := startLine(arm), endLine(b)
	for _, c := range p.comments[p.ci:] {
		if c.Start.Line > to {
			break
		}
		if c.Start.Line >= from && c.Start.Line != kept {
			return arm
		}
	}
	arm.Body = syntax.ExprBody{Expr: value}
	return arm
}

func (p *printer) printMatchArm(arm syntax.MatchArm, producing bool) {
	prefix := printPattern(arm.Pattern) + " -> "
	switch body := arm.Body.(type) {
	case syntax.ExprBody:
		// An arm's value wraps at its opening `(` as a statement's does, and
		// is followed by `,` (ADR 0145 §2), after its `}` if it is a match.
		p.after = ","
		p.printValueLineEnd(prefix, body.Expr, startLine(arm), ",")
		p.after = ""
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
		p.after = ","
		if producing {
			p.printProducingBody(body)
		} else {
			p.printBlockBody(body)
		}
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

// printBinary writes ADR 0142 §3's operators: `&&`, `||` for a boolean
// `or`, and `??` for a default. An `or` the checker couldn't place stays
// `or` (FormatTyped reports it). `??` beside `&&` or `||` gets the
// parentheses §3 requires, which the old `or` didn't need.
func printBinary(e *syntax.BinaryExpr) string {
	op := string(e.Op)
	left, right := printExpr(e.Left), printExpr(e.Right)
	switch {
	case isDefault(e):
		op = "??"
		left, right = groupedLogic(e.Left, left), groupedLogic(e.Right, right)
	case e.Op == syntax.OpAnd || e.Op == syntax.OpOr && (e.Symbol || e.OrKnown):
		op = "&&"
		if e.Op == syntax.OpOr {
			op = "||"
		}
		if isDefault(e.Left) {
			left = "(" + left + ")"
		}
		if isDefault(e.Right) {
			right = "(" + right + ")"
		}
	}
	return left + " " + op + " " + right
}

// isDefault reports whether e is a `??`, or an `or` the checker found is one.
func isDefault(e syntax.Expr) bool {
	b, ok := e.(*syntax.BinaryExpr)
	return ok && (b.Op == syntax.OpDefault || b.Op == syntax.OpOr && b.OrDefault)
}

// groupedLogic is text, a `??` operand, in parentheses when it is an `&&`
// or `||`.
func groupedLogic(e syntax.Expr, text string) string {
	if b, ok := e.(*syntax.BinaryExpr); ok && (b.Op == syntax.OpAnd || b.Op == syntax.OpOr) && !isDefault(b) {
		return "(" + text + ")"
	}
	return text
}

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
		return "!" + printExpr(e.Operand)
	case *syntax.NegExpr:
		return "-" + printExpr(e.Operand)
	case *syntax.BinaryExpr:
		return printBinary(e)
	case *syntax.TryExpr:
		return printExpr(e.Operand) + "?"
	case *syntax.MatchExpr:
		return "match " + printExpr(e.Match.Subject) + " { ... }"
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
	return printArgName(a) + printExpr(a.Value)
}

// printArgName is "name: " for a named argument, "" for a positional one.
func printArgName(a syntax.Arg) string {
	if a.Name == "" {
		return ""
	}
	return a.Name + ": "
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
