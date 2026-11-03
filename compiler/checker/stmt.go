package checker

import (
	"slices"
	"strings"

	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// childEnv copies env for a nested scope (if/match-arm/transaction bodies), so
// bindings introduced inside don't leak back out once the block ends.
func childEnv(env map[string]types.Type) map[string]types.Type {
	child := make(map[string]types.Type, len(env))
	for k, v := range env {
		child[k] = v
	}
	return child
}

// reassignableMarker is the env key prefix that records a local as
// reassignable (ADR 0053). Its key contains a space, so no identifier can
// collide with it, and childEnv's copy scopes the marker exactly as it
// scopes the binding it belongs to.
const reassignableMarker = "reassignable "

func isReassignable(env map[string]types.Type, name string) bool {
	_, ok := env[reassignableMarker+name]
	return ok
}

// declare binds name in env. A reassignable binding also sets the marker; any
// other binding clears it, so a same-named immutable binding in an inner scope
// hides an outer reassignable one.
func declare(env map[string]types.Type, name string, t types.Type, reassignable bool) {
	env[name] = t
	if reassignable {
		env[reassignableMarker+name] = types.Unknown{}
	} else {
		delete(env, reassignableMarker+name)
	}
}

// checkBinding handles `name = expr` (immutable binding, or reassignment of a
// reassignable local) and `name: Type = expr` (a reassignable declaration).
// ADR 0053.
func (c *checker) checkBinding(s *syntax.Binding, env map[string]types.Type) {
	if s.Type != nil {
		declared := c.resolve(s.Type)
		if _, exists := env[s.Name]; exists {
			c.report(s.Start, "R1", "%s is already bound in this scope; a reassignable local cannot shadow another name", s.Name)
		}
		c.checkExpected(s.Value, declared, env)
		declare(env, s.Name, declared, true)
		c.checkLocalName(s.Name, declared, s.Start)
		return
	}
	if isReassignable(env, s.Name) {
		if c.inConcurrent {
			c.report(s.Start, "R3", "%s is reassigned inside a concurrent body; concurrent bodies run in parallel, so a shared local cannot be written", s.Name)
		}
		c.checkExpected(s.Value, env[s.Name], env)
		return
	}
	if _, exists := env[s.Name]; exists {
		c.report(s.Start, "R2", "%s is immutable and cannot be reassigned; declare it with a type (`%s: Type = expr`) to make it reassignable", s.Name, s.Name)
		return
	}
	declare(env, s.Name, c.inferExpr(s.Value, env), false)
	c.checkLocalName(s.Name, env[s.Name], s.Start)
}

// checkForStmt checks a sequential loop over a List<T> (ADR 0053). The loop
// variable is an immutable binding of the element type. The body is checked as
// a non-producing block, and its label, if any, is pushed on c.loops for
// break/continue to find.
func (c *checker) checkForStmt(s *syntax.ForStmt, declReturn types.Type, env map[string]types.Type) {
	iterType := c.inferExpr(s.Iterable, env)
	elemType := types.Type(types.Unknown{})
	switch it := iterType.(type) {
	case types.Generic:
		if it.Name == "List" && len(it.Args) == 1 {
			elemType = it.Args[0]
		} else {
			c.report(s.Start, "I1", "for ... in expects a List, got %s", iterType)
		}
	case types.Unknown:
		// Not modeled (e.g. an opaque capability result): stay silent, as
		// the rest of this checker does for unmodeled types.
	case types.Primitive:
		// ADR 0058 §1: a for over bytes yields each byte as an int.
		if it.Name == "bytes" {
			elemType = types.Primitive{Name: "int"}
		} else {
			c.report(s.Start, "I1", "for ... in expects a List or bytes, got %s", iterType)
		}
	default:
		c.report(s.Start, "I1", "for ... in expects a List, got %s", iterType)
	}

	bodyEnv := childEnv(env)
	declare(bodyEnv, s.Var, elemType, false)
	c.checkLocalName(s.Var, elemType, s.Start)
	c.checkLoopBody(s.Label, s.Start, s.Body, declReturn, bodyEnv)
}

// checkWhileStmt checks `while cond { ... }` (ADR 0059): the condition is a
// bool, as for `if`, and the body is checked like a for body.
func (c *checker) checkWhileStmt(s *syntax.WhileStmt, declReturn types.Type, env map[string]types.Type) {
	c.checkExpected(s.Cond, types.Primitive{Name: "bool"}, env)
	c.checkLoopBody(s.Label, s.Start, s.Body, declReturn, childEnv(env))
}

// checkLoopBody checks a loop's body as a non-producing block with the loop's
// label, if any, pushed on c.loops for break/continue to find.
func (c *checker) checkLoopBody(label string, at lexer.Pos, body *syntax.Block, declReturn types.Type, bodyEnv map[string]types.Type) {
	if label != "" && slices.Contains(c.loops, label) {
		c.report(at, "L3", "label %s is already used by an enclosing loop", label)
	}
	c.loops = append(c.loops, label)
	c.checkBlock(body, declReturn, bodyEnv, false)
	c.loops = c.loops[:len(c.loops)-1]
}

// checkJump checks `break` or `continue` (ADR 0053). Unlabeled, it needs an
// enclosing loop. Labeled, the label must name one.
func (c *checker) checkJump(keyword, label string, at lexer.Pos) {
	if label == "" {
		if len(c.loops) == 0 {
			c.report(at, "L1", "%s is outside a loop", keyword)
		}
		return
	}
	if !slices.Contains(c.loops, label) {
		c.report(at, "L2", "%s %s: no enclosing loop is labeled %s", keyword, label, label)
	}
}

// checkBlock checks every statement in b. declReturn is the enclosing
// declaration's return type — every `return` statement anywhere inside is
// checked against it, regardless of nesting. producing is whether b's own
// final statement, if it isn't an explicit `return`, implicitly produces
// declReturn as this block's value (grammar.md §5's implicit-return rule):
// true for a declaration's outermost block and for a match arm's block
// body, false for if/transaction sub-blocks, which aren't attested as
// implicit-return positions anywhere in the corpus.
func (c *checker) checkBlock(b *syntax.Block, declReturn types.Type, env map[string]types.Type, producing bool) {
	for i, s := range b.Stmts {
		if i == len(b.Stmts)-1 && producing {
			c.checkProducingStmt(s, declReturn, env)
		} else {
			c.checkStmt(s, declReturn, env)
		}
	}
}

func (c *checker) checkStmt(s syntax.Stmt, declReturn types.Type, env map[string]types.Type) {
	switch s := s.(type) {
	case *syntax.Binding:
		c.checkBinding(s, env)
	case *syntax.ReturnStmt:
		c.checkReturnValue(s, declReturn, env)
	case *syntax.ExprStmt:
		c.checkUnhandledResult(s, env)
	case *syntax.ForStmt:
		c.checkForStmt(s, declReturn, env)
	case *syntax.WhileStmt:
		c.checkWhileStmt(s, declReturn, env)
	case *syntax.BreakStmt:
		c.checkJump("break", s.Label, s.Start)
	case *syntax.ContinueStmt:
		c.checkJump("continue", s.Label, s.Start)
	case *syntax.IfStmt:
		c.checkExpected(s.Cond, types.Primitive{Name: "bool"}, env)
		c.checkBlock(s.Then, declReturn, childEnv(env), false)
		for _, ei := range s.ElseIfs {
			c.checkExpected(ei.Cond, types.Primitive{Name: "bool"}, env)
			c.checkBlock(ei.Then, declReturn, childEnv(env), false)
		}
		if s.Else != nil {
			c.checkBlock(s.Else, declReturn, childEnv(env), false)
		}
	case *syntax.MatchStmt:
		c.checkMatchStmt(s, declReturn, nil, env)
	case *syntax.TransactionStmt:
		c.checkTransactionStmt(s, declReturn, env)
	}
}

// checkUnhandledResult is bug class F1: a typed error (Result's Err case)
// left unhandled — a bare expression statement whose value is silently
// dropped, with no `?` to propagate it and no `match` to handle it.
// Only called from checkStmt's non-producing position: the last statement
// of a producing block goes through checkProducingStmt instead, which is
// correct per ExprStmt's own doc comment — that value is the declaration's
// implicit return, not a dropped result.
func (c *checker) checkUnhandledResult(s *syntax.ExprStmt, env map[string]types.Type) {
	c.checkUnhandledResultExpr(s.Value, s.Start, env)
}

// checkUnhandledResultExpr is F1's actual check, factored out so a bare
// expression-bodied match arm used in a non-producing position (e.g. a
// fire-and-forget notification inside one arm of an otherwise-producing
// match) gets the same scrutiny a top-level bare statement already does —
// see checkMatchStmt. Before this existed, the exact same dropped-Result
// expression was rejected as a top-level statement but silently accepted
// inside a match arm, which isn't a real distinction: both are "this
// value's result is silently dropped," just inside a different statement
// shape. inferExpr/inferTry already unwrap Result<S, E> to S for a
// `?`-suffixed expression, so this only ever fires when the expression's
// own inferred type is still a bare Result — one that was never
// propagated or matched.
func (c *checker) checkUnhandledResultExpr(e syntax.Expr, at lexer.Pos, env map[string]types.Type) {
	if isAppendCall(e) {
		c.report(at, "A13", "the result of append is discarded; append returns a new list and leaves its argument unchanged, so write `xs = append(xs, x)`")
	}
	t := c.inferExpr(e, env)
	if _, ok := e.(*syntax.SortedExpr); ok {
		c.report(at, "A13", "the result of sorted(...) is discarded; it returns a new list and changes nothing, so assign it")
	}
	if call, ok := e.(*syntax.CallExpr); ok {
		if fa, ok := call.Callee.(*syntax.FieldAccess); ok && c.methodCalls[fa] {
			c.report(at, "A13", "the result of %s() is discarded; methods return a new value and change nothing, so use it or assign it", fa.Field)
		}
	}
	if g, ok := t.(types.Generic); ok && g.Name == "Result" && len(g.Args) == 2 {
		c.report(at, "F1", "typed error is left unhandled — use `?` to propagate it or `match` to handle it")
	}
}

// checkTransactionStmt is bug class C2 (transaction blocks may not nest) plus the
// transaction-context tracking C1/C3/C4/C5 depend on: c.activeTransaction names
// the capability of the transaction block currently in scope ("" when there
// isn't one), checked and restored like a stack frame around the body.
func (c *checker) checkTransactionStmt(s *syntax.TransactionStmt, declReturn types.Type, env map[string]types.Type) {
	if c.activeTransaction != "" {
		c.report(s.Start, "C2", "transaction %s is nested inside transaction %s; transaction blocks may not nest", s.Capability, c.activeTransaction)
	}
	saved := c.activeTransaction
	c.activeTransaction = s.Capability
	savedLoops := c.loops
	c.loops = nil
	c.checkBlock(s.Body, declReturn, childEnv(env), false)
	c.loops = savedLoops
	c.activeTransaction = saved
}

// checkProducingStmt is the final statement of a producing block.
func (c *checker) checkProducingStmt(s syntax.Stmt, declReturn types.Type, env map[string]types.Type) {
	switch s := s.(type) {
	case *syntax.ReturnStmt:
		c.checkReturnValue(s, declReturn, env)
	case *syntax.ExprStmt:
		c.checkExpected(s.Value, declReturn, env)
	case *syntax.MatchStmt:
		c.checkMatchStmt(s, declReturn, declReturn, env)
	default:
		// Binding/IfStmt/TransactionStmt as the final statement with no explicit
		// return isn't attested as an implicit-return position anywhere in
		// the corpus — check normally rather than enforcing something
		// nothing exercises.
		c.checkStmt(s, declReturn, env)
	}
}

func (c *checker) checkReturnValue(s *syntax.ReturnStmt, declReturn types.Type, env map[string]types.Type) {
	if s.Value == nil {
		return
	}
	c.checkExpected(s.Value, declReturn, env)
}

// checkMatchStmt checks a match statement. expected is nil in a
// non-producing position (just walk everything for its own errors);
// non-nil (always equal to declReturn, by construction — see call sites)
// when this match is itself the thing producing the enclosing
// declaration's return value, in which case every arm's body is checked
// against it.
func (c *checker) checkMatchStmt(s *syntax.MatchStmt, declReturn, expected types.Type, env map[string]types.Type) {
	subjectType := c.inferExpr(s.Subject, env)
	c.checkExhaustive(s, subjectType)
	c.checkBytesCatchAll(s)
	for _, arm := range s.Arms {
		armEnv := childEnv(env)
		c.bindPattern(arm.Pattern, subjectType, armEnv)
		switch body := arm.Body.(type) {
		case syntax.ExprBody:
			if expected != nil {
				c.checkExpected(body.Expr, expected, armEnv)
			} else {
				start, _ := body.Expr.(syntax.Spanned).Bounds()
				c.checkUnhandledResultExpr(body.Expr, start, armEnv)
			}
		case *syntax.Block:
			c.checkBlock(body, declReturn, armEnv, expected != nil)
		}
	}
}

// noneTag and restTag are the two synthetic tags a `T | None` union
// decomposes into for exhaustiveness purposes — see requiredTags.
const (
	noneTag = "None"
	restTag = "<non-None>"
)

// requiredTags returns the tags a match over subject type st must cover to
// be exhaustive, and whether st is a type this check knows how to reason
// about at all. false covers everything this checker doesn't model enough
// to judge — Unknown (most commonly an opaque capability call, no
// capability-contract system yet, Milestone 5), a plain model, or a
// union with no None member (no tag exists to pick out one non-None member
// from another, since pattern syntax has nothing to write there but a
// catch-all).
func requiredTags(st types.Type) ([]string, bool) {
	switch st := st.(type) {
	case types.Generic:
		if st.Name == "Result" && len(st.Args) == 2 {
			return []string{"Ok", "Err"}, true
		}
		return nil, false
	case types.Named:
		enumDecl, ok := st.Decl.(*syntax.EnumDecl)
		if !ok {
			return nil, false
		}
		tags := make([]string, len(enumDecl.Variants))
		for i, v := range enumDecl.Variants {
			tags[i] = v.Name
		}
		return tags, true
	case types.Union:
		for _, m := range st.Members {
			if _, isNone := m.(types.NoneType); isNone {
				return []string{noneTag, restTag}, true
			}
		}
		return nil, false
	default:
		return nil, false
	}
}

// patternCoverage reports whether p is a catch-all covering every required
// tag at once, and if not, which single tag it covers instead (possibly one
// that isn't actually required, which checkExhaustive silently ignores).
//
// A wildcard (`_`) is always a catch-all. A bare binding name (`found`) is
// also a catch-all everywhere *except* against a `T | None` union, where
// grammar.md §5 documents it as how the corpus destructures `T | None`
// without an `Option`/`Some` wrapper: it covers only the non-None remainder,
// and `None` needs its own arm. `None` itself, and any qualified or
// constructor pattern (`Ok(x)`, `CustomerError.NotFound(_)`), names one
// specific tag — the pattern's own last path segment.
func patternCoverage(p syntax.Pattern, isNoneUnion bool) (coversAll bool, tag string) {
	switch p := p.(type) {
	case *syntax.WildcardPattern:
		return true, ""
	case *syntax.NamePattern:
		if len(p.Path) == 1 {
			if p.Path[0] == noneTag {
				return false, noneTag
			}
			if isNoneUnion {
				return false, restTag
			}
			return true, ""
		}
		return false, p.Path[len(p.Path)-1]
	case *syntax.ConstructorPattern:
		return false, p.Path[len(p.Path)-1]
	default:
		return false, ""
	}
}

// checkExhaustive is bug class A9: a match over a type this checker can
// fully enumerate — Result's Ok/Err, an enum type's variants, or a
// `T | None` union — must cover every case. Subjects this checker can't
// enumerate (requiredTags' ok=false) are silently skipped rather than
// guessed at, the same policy the rest of this package follows for
// capability calls and other deliberately-unmodeled types.
func (c *checker) checkExhaustive(s *syntax.MatchStmt, subjectType types.Type) {
	tags, ok := requiredTags(subjectType)
	if !ok {
		return
	}
	isNoneUnion := len(tags) == 2 && tags[0] == noneTag && tags[1] == restTag

	covered := map[string]bool{}
	for _, arm := range s.Arms {
		all, tag := patternCoverage(arm.Pattern, isNoneUnion)
		if all {
			return
		}
		covered[tag] = true
	}

	var missing []string
	for _, t := range tags {
		if !covered[t] {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		c.report(s.Start, "A9", "match is not exhaustive; missing case(s): %s", strings.Join(missing, ", "))
	}
}
