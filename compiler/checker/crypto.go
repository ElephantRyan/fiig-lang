package checker

import (
	"strings"

	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0075: the prelude crypto group. Every member is pure but
// hashPassword, whose salt comes from Random, so it may be called only
// where Random may (§5).

// callCrypto types `crypto.member(args)` against types.CryptoFormula.
func (c *checker) callCrypto(member string, args []syntax.Arg, start lexer.Pos, env map[string]types.Type) types.Type {
	m, ok := types.CryptoFormula(member)
	if !ok {
		c.report(start, "", "the prelude crypto group has no formula %q; its formulas are %s", member, strings.Join(types.CryptoFormulaNames(), ", "))
		for _, a := range args {
			c.inferExpr(a.Value, env)
		}
		return types.Unknown{}
	}
	if member == types.HashPasswordFormula {
		switch c.callerKind {
		case kindActivity, kindGateway:
		case kindWorkflow:
			c.report(start, "B10", "a workflow may not call crypto.hashPassword, whose salt comes from Random; call an activity that does, so the workflow stays repeatable (ADR 0075 §5)")
		default:
			c.report(start, "B10", "a %s may not call crypto.hashPassword, whose salt comes from Random, so its result changes from call to call; hash in the activity or gateway that calls this %s (ADR 0075 §5)", c.callerKind, c.callerKind)
		}
	}
	return c.checkMethodArgs(types.CryptoGroup, member, m, args, start, env)
}
