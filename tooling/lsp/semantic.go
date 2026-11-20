package lsp

import (
	"sort"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/syntax"
)

// ADR 0106 §1: semantic tokens, so an editor colours each call by the kind
// of what it calls. The token types are fiig's kinds; an editor maps each
// to a colour the user can change.

// semanticTokenTypes is the legend, in the order the tokens' indexes use.
var semanticTokenTypes = []string{"rule", "conversion", "activity", "gateway", "workflow", "formula", "helper", "capability"}

// semanticKind is the token type index for a reference to d with member,
// or false for a reference that isn't a call (a model, an enum, a field).
func semanticKind(d syntax.Decl, member string) (int, bool) {
	switch d.(type) {
	case *syntax.RuleDecl:
		return 0, true
	case *syntax.ConversionDecl:
		return 1, true
	case *syntax.ActivityDecl:
		return 2, true
	case *syntax.GatewayDecl:
		return 3, true
	case *syntax.WorkflowDecl:
		return 4, true
	case *syntax.FormulaDecl:
		return 5, true
	case *syntax.HelperDecl:
		return 6, true
	case *syntax.CapabilityDecl:
		return 7, member != ""
	}
	return 0, false
}

// semanticTokens is LSP's relative encoding of every call reference in the
// last good check of path: line delta, start delta, length, type, no
// modifiers.
func (s *server) semanticTokens(uri string) map[string]any {
	empty := map[string]any{"data": []int{}}
	path, ok := uriToPath(uri)
	if !ok {
		return empty
	}
	s.mu.Lock()
	c, ok := s.lastGood[key(path)]
	src := s.goodText[key(path)]
	enc := s.enc
	s.mu.Unlock()
	if !ok || c.Index == nil {
		return empty
	}
	refs := append([]checker.Ref(nil), c.Index.Refs...)
	sort.Slice(refs, func(i, j int) bool { return refs[i].Start.Offset < refs[j].Start.Offset })
	data := []int{}
	prevLine, prevChar, lastEnd := 0, 0, -1
	for _, r := range refs {
		kind, ok := semanticKind(r.Decl, r.Member)
		if !ok || r.Start.Offset < lastEnd {
			continue
		}
		rg := rangeOf(src, r.Start, r.End, enc)
		if rg.Start.Line != rg.End.Line || rg.End.Character <= rg.Start.Character {
			continue
		}
		deltaChar := rg.Start.Character
		if rg.Start.Line == prevLine {
			deltaChar -= prevChar
		}
		data = append(data, rg.Start.Line-prevLine, deltaChar, rg.End.Character-rg.Start.Character, kind, 0)
		prevLine, prevChar, lastEnd = rg.Start.Line, rg.Start.Character, r.End.Offset
	}
	return map[string]any{"data": data}
}
