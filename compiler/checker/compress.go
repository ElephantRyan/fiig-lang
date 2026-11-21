package checker

import (
	"strings"

	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0116: the prelude compress group. Every member is pure.

// callCompress types `compress.member(args)` against types.CompressFormula.
func (c *checker) callCompress(member string, args []syntax.Arg, start lexer.Pos, env map[string]types.Type) types.Type {
	m, ok := types.CompressFormula(member)
	if !ok {
		c.report(start, "", "the prelude compress group has no formula %q; its formulas are %s", member, strings.Join(types.CompressFormulaNames(), ", "))
		for _, a := range args {
			c.inferExpr(a.Value, env)
		}
		return types.Unknown{}
	}
	return c.checkMethodArgs(types.CompressGroup, member, m, args, start, env)
}
