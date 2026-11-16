package checker

import (
	"fmt"
	"slices"
	"sort"

	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// checkJsonOverlap is J3 (ADR 0081 §1) on a type that meets JSON at
// start: what says where, as "json.decode into T". A union anywhere in t
// whose members can read the same JSON is reported once.
func (c *checker) checkJsonOverlap(start lexer.Pos, t types.Type, what string) {
	if why := c.overlap(t, map[syntax.Decl]bool{}); why != "" {
		c.report(start, "J3", "%s: %s", what, why)
	}
}

// overlap walks t as refusal does, and at each union compares its members'
// text forms (ADR 0081 §2): "" when no two can read the same JSON.
func (c *checker) overlap(t types.Type, seen map[syntax.Decl]bool) string {
	switch t := t.(type) {
	case types.Union:
		if why := unionOverlap(t); why != "" {
			return why
		}
		for _, m := range t.Members {
			if why := c.overlap(m, seen); why != "" {
				return why
			}
		}
	case types.Record:
		names := make([]string, 0, len(t.Fields))
		for n := range t.Fields {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if why := c.overlap(t.Fields[n], seen); why != "" {
				return why
			}
		}
	case types.Generic:
		for _, a := range t.Args {
			if why := c.overlap(a, seen); why != "" {
				return why
			}
		}
	case types.Named:
		if seen[t.Decl] {
			return ""
		}
		seen[t.Decl] = true
		switch d := t.Decl.(type) {
		case *syntax.ModelDecl:
			for _, f := range d.AllFields() {
				if why := c.overlap(c.declFieldType(d, f.Type), seen); why != "" {
					return why
				}
			}
		case *syntax.EnumDecl:
			for _, v := range d.Variants {
				for _, f := range v.Fields {
					if why := c.overlap(c.declFieldType(d, f.Type), seen); why != "" {
						return why
					}
				}
			}
		}
	}
	return ""
}

// tagged is one union member as a reader tells it apart: a payload-free
// enum is one of its texts as a JSON string, and an enum with payloads or
// a Result is an object whose `variant` is one of its texts.
type tagged struct {
	name   string
	object bool
	texts  []string
}

func taggedOf(t types.Type) (tagged, bool) {
	switch t := t.(type) {
	case types.Named:
		d, ok := t.Decl.(*syntax.EnumDecl)
		// JsonValue reads any JSON at all; it isn't told apart by a tag.
		if !ok || d == types.JsonValueDecl {
			return tagged{}, false
		}
		return tagged{name: t.String(), object: d.HasPayload(), texts: d.Texts()}, true
	case types.Generic:
		if t.Name == "Result" {
			return tagged{name: t.String(), object: true, texts: []string{"Ok", "Err"}}, true
		}
	}
	return tagged{}, false
}

// unionOverlap is the first pair of u's members that read the same JSON,
// described with the fixes, or "". A payload-free enum beside `string`
// isn't one: its spellings decode as the enum (ADR 0081 §3).
func unionOverlap(u types.Union) string {
	var members []tagged
	for _, m := range u.Members {
		if tg, ok := taggedOf(m); ok {
			members = append(members, tg)
		}
	}
	for i, a := range members {
		for _, b := range members[i+1:] {
			if a.object != b.object {
				continue
			}
			for _, text := range a.texts {
				if !slices.Contains(b.texts, text) {
					continue
				}
				read := fmt.Sprintf("%q", text)
				if a.object {
					read = fmt.Sprintf(`{"variant": %q, ...}`, text)
				}
				return fmt.Sprintf("%s and %s both read %s, so a reader can't tell which it is; spell one of the variants (from json \"...\", ADR 0080), or wrap them in an enum of your own whose variants say which (ADR 0081)", a.name, b.name, read)
			}
		}
	}
	return ""
}
