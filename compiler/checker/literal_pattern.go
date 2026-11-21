package checker

import (
	"strconv"
	"strings"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0130: literal patterns. An int, a string or a bool in a pattern matches
// a value equal to it. The checks:
//
//   - the literal's type fits the subject's (A1), and a float or decimal
//     subject has no literal pattern at all;
//   - a match that has a literal arm lists values and not types, so for an int
//     or a string it needs an arm that catches the rest (A9), and for a bool,
//     both `true` and `false` or one.

// literalPrim is the primitive type of a literal pattern.
func literalPrim(p *syntax.LiteralPattern) types.Type {
	return types.Primitive{Name: p.Kind}
}

// describeLiteral is the pattern as written, for a message.
func describeLiteral(p *syntax.LiteralPattern) string {
	switch p.Kind {
	case "int":
		return strconv.FormatInt(p.Int, 10)
	case "string":
		return strconv.Quote(p.Str)
	}
	return strconv.FormatBool(p.Bool)
}

// literalFits reports whether a literal of type lit can match a value of
// type subject: the same primitive, or a union with it as a member.
func literalFits(subject types.Type, lit types.Type) bool {
	switch s := subject.(type) {
	case types.Unknown:
		return true
	case types.Primitive:
		return s == lit
	case types.Union:
		for _, m := range s.Members {
			if literalFits(m, lit) {
				return true
			}
		}
	}
	return false
}

// isFloatOrDecimal reports whether t is float or decimal, alone or in a union.
func isFloatOrDecimal(t types.Type) bool {
	switch s := t.(type) {
	case types.Primitive:
		return s.Name == "float" || s.Name == "decimal"
	case types.Union:
		for _, m := range s.Members {
			if isFloatOrDecimal(m) {
				return true
			}
		}
	}
	return false
}

// checkLiteralPattern is A1 for a literal pattern against its subject.
func (c *checker) checkLiteralPattern(p *syntax.LiteralPattern, subject types.Type) {
	if literalFits(subject, literalPrim(p)) {
		return
	}
	if p.Kind == "int" && isFloatOrDecimal(subject) {
		c.report(p.Start, "A1", "the subject is %s, which has no literal pattern; bind it and compare, with math.closeTo for a float (ADR 0130)", subject)
		return
	}
	c.report(p.Start, "A1", "the pattern %s is %s, and the subject is %s (ADR 0130)", describeLiteral(p), p.Kind, subject)
}

// hasLiteralArm reports whether any arm of s is a literal pattern.
func hasLiteralArm(s *syntax.MatchStmt) bool {
	for _, arm := range s.Arms {
		if _, ok := arm.Pattern.(*syntax.LiteralPattern); ok {
			return true
		}
	}
	return false
}

// checkLiteralExhaustive is A9 for a match with a literal arm. An int or a
// string has more values than any match lists, so it needs an arm for the
// rest; a bool is covered by `true` and `false`; and a `T | None` subject
// needs its None arm as everywhere else.
func (c *checker) checkLiteralExhaustive(s *syntax.MatchStmt, subject types.Type) {
	if _, unknown := subject.(types.Unknown); unknown {
		return
	}
	var wildcard, restByName, noneArm, sawTrue, sawFalse bool
	for _, arm := range s.Arms {
		switch p := arm.Pattern.(type) {
		case *syntax.WildcardPattern:
			wildcard = true
		case *syntax.NamePattern:
			if len(p.Path) == 1 && p.Path[0] == noneTag {
				noneArm = true
			} else if len(p.Path) == 1 {
				restByName = true
			}
		case *syntax.LiteralPattern:
			if p.Kind == "bool" {
				sawTrue = sawTrue || p.Bool
				sawFalse = sawFalse || !p.Bool
			}
		}
	}
	if wildcard {
		return
	}
	var missing []string
	if hasNone(subject) && !noneArm {
		missing = append(missing, noneTag)
	}
	base := withoutNone(subject)
	if p, ok := base.(types.Primitive); ok && p.Name == "bool" {
		if !restByName {
			if !sawTrue {
				missing = append(missing, "true")
			}
			if !sawFalse {
				missing = append(missing, "false")
			}
		}
	} else if !restByName {
		missing = append(missing, "every other value (add a `_` arm)")
	}
	if len(missing) > 0 {
		c.report(s.Start, "A9", "match is not exhaustive; missing case(s): %s", strings.Join(missing, ", "))
	}
}
