package lsp

import (
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
)

// Names the checker's index doesn't record: a local or parameter (found
// here by fiig's lexical scoping, within the declaration that holds the
// position), and a declaration's own name, for find usages from it.

// identAt is the identifier under offset, read from the text itself so
// one inside a string's ${...} is found too, which no token covers alone.
func identAt(src string, offset int) (lexer.Token, bool) {
	isWord := func(b byte) bool {
		return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
	}
	start, end := offset, offset
	for start > 0 && isWord(src[start-1]) {
		start--
	}
	for end < len(src) && isWord(src[end]) {
		end++
	}
	if start == end || src[start] >= '0' && src[start] <= '9' {
		return lexer.Token{}, false
	}
	return lexer.Token{Kind: lexer.Ident, Text: src[start:end], Start: lexer.Pos{Offset: start}, End: lexer.Pos{Offset: end}}, true
}

// callable is a declaration with parameters and a body.
type callable struct {
	start, end lexer.Pos
	params     []syntax.Param
	body       *syntax.Block
}

func callables(f *syntax.File) []callable {
	var out []callable
	var add func(d syntax.Decl)
	add = func(d syntax.Decl) {
		start, end := d.(syntax.Spanned).Bounds()
		switch d := d.(type) {
		case *syntax.RuleDecl:
			out = append(out, callable{start, end, d.Params, d.Body})
		case *syntax.ConversionDecl:
			out = append(out, callable{start, end, d.Params, d.Body})
		case *syntax.ActivityDecl:
			out = append(out, callable{start, end, d.Params, d.Body})
		case *syntax.GatewayDecl:
			out = append(out, callable{start, end, d.Params, d.Body})
		case *syntax.WorkflowDecl:
			out = append(out, callable{start, end, d.Params, d.Body})
		case *syntax.HelperDecl:
			out = append(out, callable{start, end, d.Params, d.Body})
		case *syntax.CapabilityDecl:
			for _, g := range d.Gateways {
				add(g)
			}
		case *syntax.FormulasDecl:
			for _, m := range d.Members {
				fs, fe := m.Bounds()
				out = append(out, callable{fs, fe, m.Params, m.Body})
			}
		}
	}
	for _, d := range f.Decls {
		add(d)
	}
	if f.Private != nil {
		for _, d := range f.Private.Members {
			add(d)
		}
	}
	return out
}

// binding is one place a local name is introduced.
type binding struct {
	name       string
	start, end lexer.Pos
}

// localDefinition is where the local or parameter under offset was
// introduced: the latest binding of its name above it in the declaration
// that holds it.
func localDefinition(f *syntax.File, src string, offset int) (lexer.Pos, lexer.Pos, bool) {
	tok, ok := identAt(src, offset)
	if !ok {
		return lexer.Pos{}, lexer.Pos{}, false
	}
	for _, c := range callables(f) {
		if offset < c.start.Offset || offset > c.end.Offset {
			continue
		}
		var bs []binding
		for _, p := range c.params {
			s, e := nameAt(src, p.Start, p.Name)
			bs = append(bs, binding{p.Name, s, e})
		}
		collectBlock(src, c.body, &bs)
		var best *binding
		for i := range bs {
			b := &bs[i]
			if b.name == tok.Text && b.start.Offset <= tok.Start.Offset && (best == nil || b.start.Offset > best.start.Offset) {
				best = b
			}
		}
		if best != nil {
			return best.start, best.end, true
		}
	}
	return lexer.Pos{}, lexer.Pos{}, false
}

func collectBlock(src string, b *syntax.Block, out *[]binding) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		collectStmt(src, st, out)
	}
}

func collectStmt(src string, st syntax.Stmt, out *[]binding) {
	switch s := st.(type) {
	case *syntax.Binding:
		ns, ne := nameAt(src, s.Start, s.Name)
		*out = append(*out, binding{s.Name, ns, ne})
	case *syntax.IfStmt:
		collectBlock(src, s.Then, out)
		for _, ei := range s.ElseIfs {
			collectBlock(src, ei.Then, out)
		}
		collectBlock(src, s.Else, out)
	case *syntax.MatchStmt:
		for _, arm := range s.Arms {
			collectPattern(src, arm.Pattern, out)
			if b, ok := arm.Body.(*syntax.Block); ok {
				collectBlock(src, b, out)
			}
		}
	case *syntax.ForStmt:
		for _, name := range []string{s.Var, s.Value} {
			if name != "" {
				ns, ne := nameAt(src, s.Start, name)
				*out = append(*out, binding{name, ns, ne})
			}
		}
		collectBlock(src, s.Body, out)
	case *syntax.WhileStmt:
		collectBlock(src, s.Body, out)
	case *syntax.TransactionStmt:
		collectBlock(src, s.Body, out)
	}
}

func collectPattern(src string, p syntax.Pattern, out *[]binding) {
	switch p := p.(type) {
	case *syntax.NamePattern:
		if len(p.Path) == 1 && p.Path[0] != "None" {
			ns, ne := nameAt(src, p.Start, p.Path[0])
			*out = append(*out, binding{p.Path[0], ns, ne})
		}
	case *syntax.ConstructorPattern:
		for _, a := range p.Args {
			collectPattern(src, a, out)
		}
	}
}

// declUnder is the declaration whose own name is under offset, or the
// field, variant, or gateway of one, for find usages started there.
func declUnder(f *syntax.File, src string, offset int) (syntax.Decl, string, bool) {
	decls := append([]syntax.Decl(nil), f.Decls...)
	if f.Private != nil {
		decls = append(decls, f.Private.Members...)
	}
	for _, d := range decls {
		start, _ := d.(syntax.Spanned).Bounds()
		ns, ne := nameAt(src, start, syntax.DeclName(d))
		if ns.Offset <= offset && offset <= ne.Offset && ne.Offset > ns.Offset {
			return d, "", true
		}
		var members []string
		switch d := d.(type) {
		case *syntax.ModelDecl:
			for _, fl := range d.Fields {
				members = append(members, fl.Name)
			}
		case *syntax.EnumDecl:
			for _, v := range d.Variants {
				members = append(members, v.Name)
			}
		case *syntax.CapabilityDecl:
			for _, g := range d.Gateways {
				members = append(members, g.Name)
			}
		}
		for _, m := range members {
			if ms, me, ok := memberSpan(src, d, m); ok && ms.Offset <= offset && offset <= me.Offset {
				return d, m, true
			}
		}
	}
	return nil, "", false
}
