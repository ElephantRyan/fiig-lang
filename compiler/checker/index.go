package checker

import (
	"unicode/utf8"

	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0089 §4: what a check resolved, kept for tooling. The checker fills
// it at the places it already resolves a name, so an editor reads the
// compiler's own answers instead of resolving names a second time.

// Index is one file's resolved names and expression types.
type Index struct {
	Refs  []Ref
	Types []TypeAt
}

// Ref is a name as written, Start to End, and the declaration it names:
// Decl itself, or the field or variant Member of it.
type Ref struct {
	Start, End lexer.Pos
	Decl       syntax.Decl
	Member     string
}

// TypeAt is an expression's span and the type the checker gave it.
type TypeAt struct {
	Start, End lexer.Pos
	Type       types.Type
}

// ref records that the name text, written at start, refers to decl (or
// its member). Names are on one line, so End is start moved by the
// name's length.
func (c *checker) ref(start lexer.Pos, text string, decl syntax.Decl, member string) {
	c.checkTestRef(start, text, decl)
	if c.index == nil || decl == nil || start.Line == 0 {
		return
	}
	end := start
	end.Column += utf8.RuneCountInString(text)
	end.Offset += len(text)
	c.index.Refs = append(c.index.Refs, Ref{Start: start, End: end, Decl: decl, Member: member})
}

// refAfter records a reference to the last segment of a dotted name that
// starts at start (`pkg.member`, `Enum.Variant`): the segment begins after
// the prefix and its dot.
func (c *checker) refAfter(start lexer.Pos, prefix, text string, decl syntax.Decl, member string) {
	start.Column += utf8.RuneCountInString(prefix) + 1
	start.Offset += len(prefix) + 1
	c.ref(start, text, decl, member)
}

// refField records the field of a FieldAccess, which ends the access.
func (c *checker) refField(e *syntax.FieldAccess, decl syntax.Decl, member string) {
	_, end := e.Bounds()
	start := end
	start.Column -= utf8.RuneCountInString(e.Field)
	start.Offset -= len(e.Field)
	c.ref(start, e.Field, decl, member)
}

// recordType keeps an expression's type for hover.
func (c *checker) recordType(e syntax.Expr, t types.Type) {
	if c.index == nil || t == nil {
		return
	}
	sp, ok := e.(syntax.Spanned)
	if !ok {
		return
	}
	start, end := sp.Bounds()
	if start.Line == 0 {
		return
	}
	c.index.Types = append(c.index.Types, TypeAt{Start: start, End: end, Type: t})
}

// recordTypeRefs records every declaration a written type names:
// `Customer`, `http.Reply`, and the arguments of `List<Customer>`.
func (c *checker) recordTypeRefs(t syntax.Type) {
	if (c.index == nil && c.testOnly == nil) || t == nil {
		return
	}
	switch t := t.(type) {
	case *syntax.QualifiedType:
		for _, a := range t.Args {
			c.recordTypeRefs(a)
		}
		bare := &syntax.QualifiedType{Span: t.Span, Path: t.Path}
		rt, ok := types.ResolveWithImports(bare, c.universe, universesOf(c.imports))
		named, isNamed := rt.(types.Named)
		if !ok || !isNamed || named.Decl == nil {
			return
		}
		last := t.Path[len(t.Path)-1]
		prefix := ""
		for i, seg := range t.Path[:len(t.Path)-1] {
			if i > 0 {
				prefix += "."
			}
			prefix += seg
		}
		if prefix == "" {
			c.ref(t.Start, last, named.Decl, "")
		} else {
			c.refAfter(t.Start, prefix, last, named.Decl, "")
		}
	case *syntax.UnionType:
		for _, m := range t.Members {
			c.recordTypeRefs(m)
		}
	case *syntax.RecordType:
		for _, f := range t.Fields {
			c.recordTypeRefs(f.Type)
		}
	}
}
