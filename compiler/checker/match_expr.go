package checker

import (
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0142 §2: a `match` used as a value. Its arms are values of one type,
// and an arm that is a block ends the function with `return`.

// checkMatchValueArms reports an arm block that doesn't end with `return`:
// it would give the `match` no value.
func (c *checker) checkMatchValueArms(m *syntax.MatchExpr) {
	for _, arm := range m.Match.Arms {
		b, ok := arm.Body.(*syntax.Block)
		if !ok {
			continue
		}
		if n := len(b.Stmts); n > 0 {
			if _, ok := b.Stmts[n-1].(*syntax.ReturnStmt); ok {
				continue
			}
		}
		c.report(b.Start, "A1", "this arm of a `match` used as a value gives it no value: an arm is a value, or a block that ends with `return` (ADR 0142 §2)")
	}
}

// inferMatchValue is the type of a `match` used as a value with nothing
// expected of it, as in `label = match (x) { ... }`: its first value arm's
// type, which every other value arm must fit. A binding that needs a wider
// type says so, `label: string | None = match ...`.
func (c *checker) inferMatchValue(m *syntax.MatchExpr, env map[string]types.Type) types.Type {
	c.checkMatchValueArms(m)
	s := m.Match
	declReturn := c.declReturn
	subjectType := c.inferExpr(s.Subject, env)
	c.checkExhaustive(s, subjectType)
	c.checkBytesCatchAll(s)
	wholeResult := resultCatchAll(s, subjectType)
	var t types.Type
	for _, arm := range s.Arms {
		armEnv := childEnv(env)
		c.bindPattern(arm.Pattern, subjectType, armEnv)
		if np, ok := arm.Pattern.(*syntax.NamePattern); ok && np == wholeResult {
			c.report(np.Start, "A1", "this arm binds the whole %s, not its Ok value; write `Ok(%s)` to bind the value", subjectType, np.Path[0])
			armEnv[np.Path[0]] = types.Unknown{}
		}
		switch body := arm.Body.(type) {
		case syntax.ExprBody:
			if t == nil {
				t = c.inferExpr(body.Expr, armEnv)
			} else {
				c.checkExpected(body.Expr, t, armEnv)
			}
		case *syntax.Block:
			c.checkBlock(body, declReturn, armEnv, true)
		}
	}
	c.declReturn = declReturn
	if t == nil {
		return types.Unknown{}
	}
	return t
}
