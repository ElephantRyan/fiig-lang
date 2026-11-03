package checker

import (
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
)

// checkTransactionForGateway is bug class C4 — called whenever inferCall
// resolves a callee to a gateway (ADR 0031, renamed from step). An active
// transaction block's capability doesn't match the gateway's own
// `requires` capability. C1 ("a network gateway inside transaction Database")
// is explicitly documented in bug-classes.md as one instance of this
// general rule, not a separate mechanism — chargePayment requiring the
// PaymentGateway capability inside `transaction Database` is exactly a
// capability mismatch, the same shape as sendEmail requiring EmailSender
// inside the same block. Both are reported as C4 here; there's no
// capability-contract system (Milestone 5) to classify which capabilities
// are specifically "network" ones, so this doesn't try to report C1 as a
// distinct tag.
//
// C3 ("write+read pattern without transaction block") was dropped outright,
// not deferred — see bug-classes.md's "Explicitly out of scope" section for
// the full reasoning. Short version: the semantic rule still stands (single
// writes are always safe; only multiple operations that must stay
// consistent together need a transaction block), but no sound
// implementation was found. It would need both a way to tell a mutating
// gateway call apart from a non-mutating one (GatewayDecl has no such
// marker) and a way to tell related operations apart from unrelated ones
// on the same capability (two independent writes to different entities
// aren't a hazard) — the second of those isn't infrastructure this
// project is missing, it's a business-logic judgment no static check can
// make.
func (c *checker) checkTransactionForGateway(gw *syntax.GatewayDecl, at lexer.Pos) {
	if c.activeTransaction == "" {
		return
	}
	if gw.Requires != c.activeTransaction {
		c.report(at, "C4", "transaction %s may not call %q, which requires %s", c.activeTransaction, gw.Name, gw.Requires)
	}
}

// limitAsInt extracts a concurrent block's `limit:` value as an int, when
// it's a plain integer literal — the only form D2 can meaningfully compare
// against a parent budget.
func limitAsInt(e syntax.Expr) (int, bool) {
	lit, ok := e.(*syntax.IntLit)
	if !ok {
		return 0, false
	}
	n, err := syntax.IntValue(lit.Text)
	if err != nil {
		return 0, false
	}
	return int(n), true
}
