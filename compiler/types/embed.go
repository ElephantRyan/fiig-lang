package types

import (
	"fmt"
	"strings"

	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
)

// Model embedding (ADR 0045). A model may name another model with no field
// name; its fields are promoted into the outer model. This file computes
// the flattened field set (ModelDecl.Flat, filled in by BuildUniverse) and
// the declaration errors that flattening can find.

// EmbedError is one embedding problem found in a model declaration. Rule is
// the bug-classes.md ID, or "" for an untagged structural error.
type EmbedError struct {
	At   lexer.Pos
	Rule string
	Msg  string
}

// flattenModels fills in Flat on every model in u, best-effort: an
// embedding that cannot be resolved is skipped here and reported by
// EmbedDiagnostics, so one bad model does not hide the rest.
func flattenModels(u *Universe) {
	for _, d := range u.Decls {
		if m, ok := d.(*syntax.ModelDecl); ok {
			m.Flat = buildFlat(m, u, []*syntax.ModelDecl{m}, nil)
		}
	}
}

// EmbedDiagnostics reports every embedding error in m itself: an embedded
// type that is not a model of this package, the same model embedded twice,
// two embedded models promoting the same name at the same depth (A10,
// whether or not the name is ever read), and an embedding cycle (A11).
// Errors inside an embedded model are reported at that model, not here.
func EmbedDiagnostics(m *syntax.ModelDecl, u *Universe) []EmbedError {
	var errs []EmbedError
	if cycle := embedCycle(m, u); cycle != nil {
		errs = append(errs, EmbedError{At: spanStart(m), Rule: "A11", Msg: fmt.Sprintf("model %q is part of an embedding cycle: %s", m.Name, strings.Join(cycle, " -> "))})
	}
	buildFlat(m, u, []*syntax.ModelDecl{m}, &errs)
	return errs
}

func spanStart(m *syntax.ModelDecl) lexer.Pos {
	start, _ := m.Bounds()
	return start
}

// embeddedModel resolves an embedded field to its model, or says why not.
func embeddedModel(f syntax.Field, u *Universe) (*syntax.ModelDecl, string) {
	qt, ok := f.Type.(*syntax.QualifiedType)
	if !ok || len(qt.Path) != 1 || len(qt.Args) > 0 {
		return nil, "an embedded type must be a plain model name"
	}
	if u.IsAmbiguous(f.Name) {
		return nil, ""
	}
	decl, found := u.Lookup(f.Name)
	if !found {
		return nil, fmt.Sprintf("embedded type %q is not declared", f.Name)
	}
	m, ok := decl.(*syntax.ModelDecl)
	if !ok {
		return nil, fmt.Sprintf("embedded type %q is a %s, not a model", f.Name, DeclKindName(decl))
	}
	return m, ""
}

// embedCycle returns the cycle through m (m -> ... -> m), or nil.
func embedCycle(m *syntax.ModelDecl, u *Universe) []string {
	var path []string
	seen := map[*syntax.ModelDecl]bool{}
	var walk func(cur *syntax.ModelDecl) bool
	walk = func(cur *syntax.ModelDecl) bool {
		path = append(path, cur.Name)
		for _, f := range cur.Fields {
			if !f.Embedded {
				continue
			}
			next, _ := embeddedModel(f, u)
			if next == nil {
				continue
			}
			if next == m {
				path = append(path, next.Name)
				return true
			}
			if seen[next] {
				continue
			}
			seen[next] = true
			if walk(next) {
				return true
			}
		}
		path = path[:len(path)-1]
		return false
	}
	if walk(m) {
		return path
	}
	return nil
}

// buildFlat computes m's flattened fields. stack holds the models being
// expanded (cycle guard). errs, when non-nil, collects m's own errors;
// recursion into an embedded model always runs with errs nil.
func buildFlat(m *syntax.ModelDecl, u *Universe, stack []*syntax.ModelDecl, errs *[]EmbedError) []syntax.FlatField {
	report := func(at lexer.Pos, rule, format string, args ...any) {
		if errs != nil {
			*errs = append(*errs, EmbedError{At: at, Rule: rule, Msg: fmt.Sprintf(format, args...)})
		}
	}

	own := map[string]bool{}
	for _, f := range m.Fields {
		if !f.Embedded {
			own[f.Name] = true
		}
	}

	type promoted struct {
		field syntax.FlatField
		depth int
	}
	cands := map[string][]promoted{}
	embedAt := map[string]int{}      // field name -> index into m.Fields of the embed that first promoted it
	embedNames := map[int][]string{} // m.Fields index -> names that embed promotes, in the embedded model's own order
	seenEmbed := map[string]bool{}
	for i, f := range m.Fields {
		if !f.Embedded {
			continue
		}
		em, problem := embeddedModel(f, u)
		if em == nil {
			if problem != "" {
				report(f.Start, "", "%s (embedding does not cross packages)", problem)
			}
			continue
		}
		if seenEmbed[f.Name] {
			report(f.Start, "A10", "model %q embeds %q more than once, so every one of its fields is ambiguous", m.Name, f.Name)
			continue
		}
		seenEmbed[f.Name] = true
		if inStack(stack, em) {
			continue // a cycle; reported by EmbedDiagnostics
		}
		for _, sf := range buildFlat(em, u, append(stack, em), nil) {
			via := append([]string{f.Name}, sf.Via...)
			cands[sf.Name] = append(cands[sf.Name], promoted{field: syntax.FlatField{Span: sf.Span, Name: sf.Name, Type: sf.Type, Via: via}, depth: len(via)})
			if _, ok := embedAt[sf.Name]; !ok {
				embedAt[sf.Name] = i
			}
			embedNames[i] = append(embedNames[i], sf.Name)
		}
	}

	winner := map[string]syntax.FlatField{}
	for name, cs := range cands {
		if own[name] {
			continue // an explicit field shadows a promoted one
		}
		best := cs[0]
		for _, c := range cs[1:] {
			if c.depth < best.depth {
				best = c
			}
		}
		var tied []string
		for _, c := range cs {
			if c.depth == best.depth {
				tied = append(tied, strings.Join(c.field.Via, ".")+"."+name)
			}
		}
		if len(tied) > 1 {
			report(m.Fields[embedAt[name]].Start, "A10", "field %q is promoted by both %s; declare %q explicitly in %s to choose", name, strings.Join(tied, " and "), name, m.Name)
		}
		winner[name] = best.field
	}

	// Declaration order: an own field where it is declared, a promoted
	// field where its embedding is.
	var out []syntax.FlatField
	emitted := map[string]bool{}
	for i, f := range m.Fields {
		if !f.Embedded {
			out = append(out, syntax.FlatField{Span: f.Span, Name: f.Name, Type: f.Type})
			emitted[f.Name] = true
			continue
		}
		for _, name := range embedNames[i] {
			if w, ok := winner[name]; ok && !emitted[name] && w.Via[0] == f.Name {
				out = append(out, w)
				emitted[name] = true
			}
		}
	}
	if out == nil {
		out = []syntax.FlatField{}
	}
	return out
}

func inStack(stack []*syntax.ModelDecl, m *syntax.ModelDecl) bool {
	for _, s := range stack {
		if s == m {
			return true
		}
	}
	return false
}
