package runtime

import (
	"context"
	"strings"

	"fiig-lang/compiler/syntax"
)

// ADR 0115 §4: a declaration's body is turned into Go closures once, the
// first time it is called, rather than walked node by node on every run.
// A closure does what evalStmt or evalExpr would for its node, in the same
// Env, so the two agree by construction; a node this file doesn't compile
// gets a closure that hands it to them. Arithmetic and comparisons work on
// int64 while both operands are ints, so an expression boxes only the
// value it finally produces, and constants are boxed once, here.

// A cstmt runs one statement, with evalStmt's results.
type cstmt func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault)

// A cexpr evaluates one expression, with evalExpr's results.
type cexpr func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault)

// A cint evaluates an expression that is usually an int: v is nil when it
// produced the int n, and otherwise holds what it produced, the value of
// an early return, or nothing beside a fault.
type cint func(in *Interp, ctx context.Context, env *Env) (n int64, v Value, ret bool, f *Fault)

// cblock is a compiled block. scoped is whether it needs a scope of its
// own: one that binds no names runs in its parent's.
type cblock struct {
	stmts  []cstmt
	scoped bool
}

// run is evalBlock.
func (b *cblock) run(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
	done := ctx.Done()
	for _, s := range b.stmts {
		if done != nil {
			select {
			case <-done:
				return nil, false, Faultf("cancelled: %v", ctx.Err())
			default:
			}
		}
		val, returned, fault := s(in, ctx, env)
		if fault != nil {
			return nil, false, fault
		}
		if returned {
			return val, true, nil
		}
	}
	return nil, false, nil
}

// in runs b as a nested block of env's: in a child scope when it binds
// names, as evalBlock(ctx, b, env.Child(), ...) does, but reused.
func (b *cblock) in(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
	if b.scoped {
		env = env.scratch()
	}
	return b.run(in, ctx, env)
}

// compiledBody is body compiled, once per Interp.
func (in *Interp) compiledBody(body *syntax.Block) *cblock {
	if c, ok := in.compiled.Load(body); ok {
		return c.(*cblock)
	}
	c, _ := in.compiled.LoadOrStore(body, compileBlock(body, true))
	return c.(*cblock)
}

func compileBlock(b *syntax.Block, producing bool) *cblock {
	out := &cblock{stmts: make([]cstmt, len(b.Stmts))}
	for i, s := range b.Stmts {
		out.stmts[i] = compileStmt(s, producing && i == len(b.Stmts)-1)
		switch s.(type) {
		case *syntax.ExprStmt, *syntax.ReturnStmt, *syntax.BreakStmt, *syntax.ContinueStmt,
			*syntax.IfStmt, *syntax.WhileStmt, *syntax.ForStmt, *syntax.MatchStmt:
			// Each binds nothing in the block's own scope.
		default:
			out.scoped = true
		}
	}
	return out
}

func compileStmt(s syntax.Stmt, producingHere bool) cstmt {
	switch s := s.(type) {
	case *syntax.Binding:
		return compileBinding(s)
	case *syntax.ExprStmt:
		value := compileExpr(s.Value)
		return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
			val, earlyReturn, fault := value(in, ctx, env)
			if fault != nil {
				return nil, false, fault
			}
			if earlyReturn || producingHere {
				return val, true, nil
			}
			return nil, false, nil
		}
	case *syntax.ReturnStmt:
		if s.Value == nil {
			return func(*Interp, context.Context, *Env) (Value, bool, *Fault) { return None{}, true, nil }
		}
		value := compileExpr(s.Value)
		return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
			val, _, fault := value(in, ctx, env)
			if fault != nil {
				return nil, false, fault
			}
			return val, true, nil
		}
	case *syntax.BreakStmt:
		jump := Value(loopJump{label: s.Label, brk: true})
		return func(*Interp, context.Context, *Env) (Value, bool, *Fault) { return jump, true, nil }
	case *syntax.ContinueStmt:
		jump := Value(loopJump{label: s.Label})
		return func(*Interp, context.Context, *Env) (Value, bool, *Fault) { return jump, true, nil }
	case *syntax.IfStmt:
		return compileIf(s, producingHere)
	case *syntax.WhileStmt:
		return compileWhile(s)
	case *syntax.ForStmt:
		return compileFor(s)
	case *syntax.MatchStmt:
		return compileMatch(s, producingHere)
	}
	return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
		return in.evalStmt(ctx, s, env, producingHere)
	}
}

// compileBinding is a binding statement: a declaration binds in env's own
// scope, and any other binding reassigns the reassignable local it names
// or else binds, as evalStmt does. An int from arithmetic is stored
// unboxed.
func compileBinding(s *syntax.Binding) cstmt {
	name, typed := s.Name, s.Type != nil
	at := new(slot)
	store := func(env *Env, b binding) {
		if !typed {
			if old := env.lookup(name, at); old != nil && old.reassignable {
				old.v, old.n, old.isInt = b.v, b.n, b.isInt
				return
			}
		}
		b.name, b.reassignable = name, typed
		env.put(b)
	}
	if isArithmetic(s.Value) {
		value := compileInt(s.Value)
		return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
			n, v, earlyReturn, fault := value(in, ctx, env)
			if fault != nil {
				return nil, false, fault
			}
			if earlyReturn {
				return v, true, nil
			}
			if v != nil {
				store(env, binding{v: v})
			} else {
				store(env, binding{n: n, isInt: true})
			}
			return nil, false, nil
		}
	}
	value := compileExpr(s.Value)
	return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
		val, earlyReturn, fault := value(in, ctx, env)
		if fault != nil {
			return nil, false, fault
		}
		if earlyReturn {
			return val, true, nil
		}
		store(env, binding{v: val})
		return nil, false, nil
	}
}

// isArithmetic is whether e is arithmetic, so usually an int.
func isArithmetic(e syntax.Expr) bool {
	for {
		paren, ok := e.(*syntax.ParenExpr)
		if !ok {
			break
		}
		e = paren.Inner
	}
	b, ok := e.(*syntax.BinaryExpr)
	if !ok {
		return false
	}
	switch b.Op {
	case syntax.OpAdd, syntax.OpSub, syntax.OpMul, syntax.OpDiv, syntax.OpMod:
		return true
	}
	return false
}

// compileCond is evalCond: whether the condition holds, or the value of
// an early return from it.
func compileCond(e syntax.Expr) func(in *Interp, ctx context.Context, env *Env) (bool, Value, bool, *Fault) {
	cond := compileExpr(e)
	return func(in *Interp, ctx context.Context, env *Env) (bool, Value, bool, *Fault) {
		val, earlyReturn, fault := cond(in, ctx, env)
		if fault != nil || earlyReturn {
			return false, val, earlyReturn, fault
		}
		b, ok := val.(Bool)
		if !ok {
			return false, nil, false, Faultf("condition did not evaluate to a boolean (got %s)", val)
		}
		return bool(b), nil, false, nil
	}
}

func compileIf(s *syntax.IfStmt, producingHere bool) cstmt {
	conds := []func(*Interp, context.Context, *Env) (bool, Value, bool, *Fault){compileCond(s.Cond)}
	blocks := []*cblock{compileBlock(s.Then, producingHere)}
	for _, ei := range s.ElseIfs {
		conds = append(conds, compileCond(ei.Cond))
		blocks = append(blocks, compileBlock(ei.Then, producingHere))
	}
	var otherwise *cblock
	if s.Else != nil {
		otherwise = compileBlock(s.Else, producingHere)
	}
	return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
		for i, cond := range conds {
			ok, early, earlyReturn, fault := cond(in, ctx, env)
			if fault != nil {
				return nil, false, fault
			}
			if earlyReturn {
				return early, true, nil
			}
			if ok {
				return blocks[i].in(in, ctx, env)
			}
		}
		if otherwise != nil {
			return otherwise.in(in, ctx, env)
		}
		return nil, false, nil
	}
}

func compileWhile(s *syntax.WhileStmt) cstmt {
	cond := compileCond(s.Cond)
	body := compileBlock(s.Body, false)
	label := s.Label
	return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
		// One scope for the body, emptied each iteration (ADR 0115 §3).
		bodyEnv := env
		if body.scoped {
			bodyEnv = env.scratch()
		}
		for {
			if f := checkCancelled(ctx); f != nil {
				return nil, false, f
			}
			ok, early, earlyReturn, fault := cond(in, ctx, env)
			if fault != nil || earlyReturn {
				return early, earlyReturn, fault
			}
			if !ok {
				return nil, false, nil
			}
			if body.scoped {
				bodyEnv.reset()
			}
			val, jumped, fault := body.run(in, ctx, bodyEnv)
			if exit, out, outJumped, outFault := loopExit(val, jumped, fault, label); exit {
				return out, outJumped, outFault
			}
		}
	}
}

func compileFor(s *syntax.ForStmt) cstmt {
	iterable := compileExpr(s.Iterable)
	body := compileBlock(s.Body, false)
	name, label := s.Var, s.Label
	return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
		iterVal, earlyReturn, fault := iterable(in, ctx, env)
		if fault != nil || earlyReturn {
			return iterVal, earlyReturn, fault
		}
		var count int
		var list []Value
		var data []byte
		switch it := iterVal.(type) {
		case Map:
			return in.evalMapFor(ctx, s, it, env)
		case List:
			list, count = it.Elems, len(it.Elems)
		case Bytes:
			// ADR 0058 §1: each byte as an int, read in place.
			data, count = it.B, len(it.B)
		default:
			return nil, false, Faultf("for ... in iterable is not a List or bytes (got %s)", iterVal)
		}
		// The loop variable needs a scope even when the body binds nothing.
		// It stays first in that scope, and each iteration sets its value.
		bodyEnv := env.scratch()
		bodyEnv.bindFresh(name, nil)
		for i := range count {
			if f := checkCancelled(ctx); f != nil {
				return nil, false, f
			}
			bodyEnv.resetTo(1)
			if v := &bodyEnv.vars[0]; list != nil {
				v.v = list[i]
			} else {
				v.n, v.isInt = int64(data[i]), true
			}
			val, jumped, fault := body.run(in, ctx, bodyEnv)
			if exit, out, outJumped, outFault := loopExit(val, jumped, fault, label); exit {
				return out, outJumped, outFault
			}
		}
		return nil, false, nil
	}
}

func compileMatch(s *syntax.MatchStmt, producingHere bool) cstmt {
	subject := compileExpr(s.Subject)
	type arm struct {
		pattern syntax.Pattern
		expr    cexpr
		block   *cblock
	}
	arms := make([]arm, len(s.Arms))
	for i, a := range s.Arms {
		arms[i].pattern = a.Pattern
		switch body := a.Body.(type) {
		case syntax.ExprBody:
			arms[i].expr = compileExpr(body.Expr)
		case *syntax.Block:
			arms[i].block = compileBlock(body, producingHere)
		default:
			return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
				return in.evalStmt(ctx, s, env, producingHere)
			}
		}
	}
	return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
		v, earlyReturn, fault := subject(in, ctx, env)
		if fault != nil {
			return nil, false, fault
		}
		if earlyReturn {
			return v, true, nil
		}
		// One scope for the arms, emptied before each is tried (ADR 0115 §3).
		armEnv := env.scratch()
		for _, a := range arms {
			armEnv.reset()
			if !MatchPattern(a.pattern, v, armEnv) {
				continue
			}
			if a.block != nil {
				return a.block.run(in, ctx, armEnv)
			}
			val, earlyReturn, fault := a.expr(in, ctx, armEnv)
			if fault != nil {
				return nil, false, fault
			}
			if earlyReturn || producingHere {
				return val, true, nil
			}
			return nil, false, nil
		}
		return nil, false, Faultf("no match arm matched %s", v)
	}
}

// constExpr is a closure producing v, boxed once.
func constExpr(v Value) cexpr {
	return func(*Interp, context.Context, *Env) (Value, bool, *Fault) { return v, false, nil }
}

func compileExpr(e syntax.Expr) cexpr {
	if v, ok := constValue(e); ok {
		return constExpr(v)
	}
	switch e := e.(type) {
	case *syntax.Ident:
		switch e.Name {
		case "None":
			return constExpr(None{})
		case "true", "false":
			return constExpr(Bool(e.Name == "true"))
		}
		name, at := e.Name, new(slot)
		return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
			if b := env.lookup(name, at); b != nil {
				return b.value(), false, nil
			}
			// A group used as a value, or an undefined name.
			return in.evalExpr(ctx, e, env)
		}
	case *syntax.StringLit:
		return compileString(e)
	case *syntax.ParenExpr:
		return compileExpr(e.Inner)
	case *syntax.NotExpr:
		operand := compileExpr(e.Operand)
		return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
			v, earlyReturn, fault := operand(in, ctx, env)
			if fault != nil || earlyReturn {
				return v, earlyReturn, fault
			}
			b, ok := v.(Bool)
			if !ok {
				return nil, false, Faultf("`not` operand is not a boolean (got %s)", v)
			}
			return Bool(!b), false, nil
		}
	case *syntax.TryExpr:
		operand := compileExpr(e.Operand)
		return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
			v, earlyReturn, fault := operand(in, ctx, env)
			if fault != nil || earlyReturn {
				return v, earlyReturn, fault
			}
			rv, ok := v.(Result)
			if !ok {
				return nil, false, Faultf("`?` operand is not a Result (got %s)", v)
			}
			if rv.Ok {
				return rv.Value, false, nil
			}
			return rv, true, nil
		}
	case *syntax.ListLit:
		elems := make([]cexpr, len(e.Elems))
		for i, el := range e.Elems {
			elems[i] = compileExpr(el)
		}
		return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
			out := make([]Value, len(elems))
			for i, el := range elems {
				v, earlyReturn, fault := el(in, ctx, env)
				if fault != nil || earlyReturn {
					return v, earlyReturn, fault
				}
				out[i] = v
			}
			return List{Elems: out}, false, nil
		}
	case *syntax.BinaryExpr:
		return compileBinary(e)
	case *syntax.BytesLit:
		return compileBytesLit(e)
	case *syntax.FieldAccess:
		return compileFieldAccess(e)
	case *syntax.CallExpr:
		if c := compileCall(e); c != nil {
			return c
		}
	}
	return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
		return in.evalExpr(ctx, e, env)
	}
}

// compileBytesLit is evalBytesLit, with each int segment's value computed
// as an int64 and the buffer sized for the fixed-width segments up front.
func compileBytesLit(e *syntax.BytesLit) cexpr {
	segs := e.Segments
	var head cexpr
	if len(segs) > 0 && segs[0].Encoding.Kind == syntax.SegBytes {
		head, segs = compileExpr(segs[0].Value), segs[1:]
	}
	type segment struct {
		enc   syntax.Encoding
		n     cint
		value cexpr
	}
	compiled := make([]segment, len(segs))
	fixed := 0
	for i, seg := range segs {
		compiled[i].enc = seg.Encoding
		if seg.Encoding.Kind == syntax.SegInt {
			compiled[i].n = compileInt(seg.Value)
			fixed += seg.Encoding.Bits
		} else {
			compiled[i].value = compileExpr(seg.Value)
		}
	}
	fixed = (fixed + 7) / 8
	return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
		var start Bytes
		if head != nil {
			v, earlyReturn, fault := head(in, ctx, env)
			if fault != nil || earlyReturn {
				return v, earlyReturn, fault
			}
			b, ok := v.(Bytes)
			if !ok {
				return nil, false, Faultf("bytes segment value is not bytes (got %s)", v)
			}
			start = b
		}
		w := bitWriter{buf: make([]byte, 0, fixed)}
		for _, seg := range compiled {
			switch seg.enc.Kind {
			case syntax.SegInt:
				n, v, earlyReturn, fault := seg.n(in, ctx, env)
				if fault != nil || earlyReturn {
					return v, earlyReturn, fault
				}
				if v != nil {
					return nil, false, Faultf("%s segment value is not an int (got %s)", seg.enc.Name, v)
				}
				if !intFits(n, seg.enc) {
					return nil, false, Faultf("%d does not fit in %s", n, seg.enc.Name)
				}
				w.writeInt(n, seg.enc)
			case syntax.SegBytes:
				v, earlyReturn, fault := seg.value(in, ctx, env)
				if fault != nil || earlyReturn {
					return v, earlyReturn, fault
				}
				b, ok := v.(Bytes)
				if !ok {
					return nil, false, Faultf("bytes segment value is not bytes (got %s)", v)
				}
				w.writeBytes(b.B)
			case syntax.SegUTF8:
				v, earlyReturn, fault := seg.value(in, ctx, env)
				if fault != nil || earlyReturn {
					return v, earlyReturn, fault
				}
				str, ok := v.(Str)
				if !ok {
					return nil, false, Faultf("utf8 segment value is not a string (got %s)", v)
				}
				w.writeBytes([]byte(str))
			}
		}
		if w.nbits%8 != 0 {
			return nil, false, Faultf("binary expression is %d bits, not whole bytes", w.nbits)
		}
		if head == nil {
			return Bytes{B: w.buf}, false, nil
		}
		return growBytes(start, w.buf), false, nil
	}
}

func compileString(e *syntax.StringLit) cexpr {
	parts := make([]cexpr, len(e.Parts))
	interpolated := false
	for i, part := range e.Parts {
		if part.Interp != nil {
			parts[i] = compileExpr(part.Interp)
			interpolated = true
		}
	}
	if !interpolated {
		var sb strings.Builder
		for _, part := range e.Parts {
			sb.WriteString(part.Text)
		}
		return constExpr(Str(sb.String()))
	}
	return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
		var sb strings.Builder
		for i, part := range e.Parts {
			if parts[i] == nil {
				sb.WriteString(part.Text)
				continue
			}
			v, earlyReturn, fault := parts[i](in, ctx, env)
			if fault != nil || earlyReturn {
				return v, earlyReturn, fault
			}
			sb.WriteString(v.String())
		}
		return Str(sb.String()), false, nil
	}
}

// compileFieldAccess reads a local record's field directly; any other
// target (a config, an enum, an imported package's enum, an expression)
// goes to evalFieldAccess.
func compileFieldAccess(e *syntax.FieldAccess) cexpr {
	target, ok := e.Target.(*syntax.Ident)
	if !ok {
		return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
			return in.evalFieldAccess(ctx, e, env)
		}
	}
	name, field, at := target.Name, e.Field, new(slot)
	return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
		if b := env.lookup(name, at); b != nil {
			if rec, ok := b.v.(Record); ok {
				if f, ok := rec.Fields[field]; ok {
					return f, false, nil
				}
			}
		}
		return in.evalFieldAccess(ctx, e, env)
	}
}

func compileBinary(e *syntax.BinaryExpr) cexpr {
	switch e.Op {
	case syntax.OpAnd, syntax.OpOr:
		left, right := compileExpr(e.Left), compileExpr(e.Right)
		return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
			l, earlyReturn, fault := left(in, ctx, env)
			if fault != nil || earlyReturn {
				return l, earlyReturn, fault
			}
			if e.OrDefault {
				// ADR 0099: the right side runs only when the left is None.
				if _, isNone := l.(None); !isNone {
					return l, false, nil
				}
				return right(in, ctx, env)
			}
			lb, ok := l.(Bool)
			if !ok {
				return nil, false, Faultf("%s operand is not a boolean (got %s)", e.Op, l)
			}
			if e.Op == syntax.OpAnd && !bool(lb) {
				return Bool(false), false, nil
			}
			if e.Op == syntax.OpOr && bool(lb) {
				return Bool(true), false, nil
			}
			r, earlyReturn, fault := right(in, ctx, env)
			if fault != nil || earlyReturn {
				return r, earlyReturn, fault
			}
			rb, ok := r.(Bool)
			if !ok {
				return nil, false, Faultf("%s operand is not a boolean (got %s)", e.Op, r)
			}
			return rb, false, nil
		}
	case syntax.OpAdd, syntax.OpSub, syntax.OpMul, syntax.OpDiv, syntax.OpMod:
		arith := compileInt(e)
		return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
			n, v, earlyReturn, fault := arith(in, ctx, env)
			if fault != nil || earlyReturn || v != nil {
				return v, earlyReturn, fault
			}
			return intValue(n), false, nil
		}
	case syntax.OpEq, syntax.OpNe, syntax.OpLt, syntax.OpLe, syntax.OpGt, syntax.OpGe:
		left, right := compileInt(e.Left), compileInt(e.Right)
		op := e.Op
		return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
			ln, lv, earlyReturn, fault := left(in, ctx, env)
			if fault != nil || earlyReturn {
				return lv, earlyReturn, fault
			}
			rn, rv, earlyReturn, fault := right(in, ctx, env)
			if fault != nil || earlyReturn {
				return rv, earlyReturn, fault
			}
			if lv == nil && rv == nil {
				switch op {
				case syntax.OpEq:
					return Bool(ln == rn), false, nil
				case syntax.OpNe:
					return Bool(ln != rn), false, nil
				case syntax.OpLt:
					return Bool(ln < rn), false, nil
				case syntax.OpLe:
					return Bool(ln <= rn), false, nil
				case syntax.OpGt:
					return Bool(ln > rn), false, nil
				default:
					return Bool(ln >= rn), false, nil
				}
			}
			return in.applyBinary(e, boxed(ln, lv), boxed(rn, rv))
		}
	}
	return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
		return in.evalBinary(ctx, e, env)
	}
}

// intBinary is e's arithmetic on operands already evaluated, in int64
// when both are ints (v nil), with applyBinary's overflow and division
// faults, and by applyBinary otherwise.
func (in *Interp) intBinary(e *syntax.BinaryExpr, ln int64, lv Value, rn int64, rv Value) (int64, Value, bool, *Fault) {
	if lv == nil && rv == nil {
		if (e.Op == syntax.OpDiv || e.Op == syntax.OpMod) && rn == 0 {
			return 0, nil, false, Faultf("division by zero")
		}
		n, ok := intArith(e.Op, ln, rn)
		if !ok {
			return 0, nil, false, Faultf("integer overflow at %s: %d %s %d does not fit in int", e.Start, ln, e.Op, rn)
		}
		return n, nil, false, nil
	}
	v, _, fault := in.applyBinary(e, boxed(ln, lv), boxed(rn, rv))
	if fault != nil {
		return 0, nil, false, fault
	}
	if n, ok := v.(Int); ok {
		return int64(n), nil, false, nil
	}
	return 0, v, false, nil
}

// An intLeaf is an operand that is a local or an int constant.
type intLeaf struct {
	name    string
	at      *slot
	c       int64
	isConst bool
}

func intLeafOf(e syntax.Expr) (intLeaf, bool) {
	if v, ok := constValue(e); ok {
		n, isInt := v.(Int)
		return intLeaf{c: int64(n), isConst: true}, isInt
	}
	if id, ok := e.(*syntax.Ident); ok && id.Name != "None" && id.Name != "true" && id.Name != "false" {
		return intLeaf{name: id.Name, at: new(slot)}, true
	}
	return intLeaf{}, false
}

// read is the leaf's int, or false when it isn't a bound int.
func (l *intLeaf) read(env *Env) (int64, bool) {
	if l.isConst {
		return l.c, true
	}
	b := env.lookup(l.name, l.at)
	if b == nil {
		return 0, false
	}
	if b.isInt {
		return b.n, true
	}
	n, ok := b.v.(Int)
	return int64(n), ok
}

// boxed is a cint's result as a Value.
func boxed(n int64, v Value) Value {
	if v != nil {
		return v
	}
	return intValue(n)
}

// Boxing an int above 255 into a Value allocates, so the ints programs
// count, index, and measure with most are boxed once, at start-up, as
// CPython and the JVM cache theirs (ADR 0115 §4).
const (
	smallIntMin = -1024
	smallIntEnd = 1 << 16
)

var smallInts = func() []Value {
	out := make([]Value, smallIntEnd-smallIntMin)
	for i := range out {
		out[i] = Int(i + smallIntMin)
	}
	return out
}()

// intValue is Int(n) as a Value, without allocating when n is small.
func intValue(n int64) Value {
	if n >= smallIntMin && n < smallIntEnd {
		return smallInts[n-smallIntMin]
	}
	return Int(n)
}

// compileInt compiles e for a context that wants an int: arithmetic stays
// in int64 while both operands are ints, with applyBinary's overflow and
// division faults, and goes to applyBinary for anything else.
func compileInt(e syntax.Expr) cint {
	if v, ok := constValue(e); ok {
		if n, isInt := v.(Int); isInt {
			c := int64(n)
			return func(*Interp, context.Context, *Env) (int64, Value, bool, *Fault) { return c, nil, false, nil }
		}
		return func(*Interp, context.Context, *Env) (int64, Value, bool, *Fault) { return 0, v, false, nil }
	}
	switch e := e.(type) {
	case *syntax.Ident:
		if e.Name == "None" || e.Name == "true" || e.Name == "false" {
			break
		}
		name, at := e.Name, new(slot)
		return func(in *Interp, ctx context.Context, env *Env) (int64, Value, bool, *Fault) {
			b := env.lookup(name, at)
			if b == nil {
				v, earlyReturn, fault := in.evalExpr(ctx, e, env)
				return 0, v, earlyReturn, fault
			}
			if b.isInt {
				return b.n, nil, false, nil
			}
			if n, ok := b.v.(Int); ok {
				return int64(n), nil, false, nil
			}
			return 0, b.v, false, nil
		}
	case *syntax.ParenExpr:
		return compileInt(e.Inner)
	case *syntax.BinaryExpr:
		switch e.Op {
		case syntax.OpAdd, syntax.OpSub, syntax.OpMul, syntax.OpDiv, syntax.OpMod:
			left, right := compileInt(e.Left), compileInt(e.Right)
			general := func(in *Interp, ctx context.Context, env *Env) (int64, Value, bool, *Fault) {
				ln, lv, earlyReturn, fault := left(in, ctx, env)
				if fault != nil || earlyReturn {
					return 0, lv, earlyReturn, fault
				}
				rn, rv, earlyReturn, fault := right(in, ctx, env)
				if fault != nil || earlyReturn {
					return 0, rv, earlyReturn, fault
				}
				return in.intBinary(e, ln, lv, rn, rv)
			}
			divides := e.Op == syntax.OpDiv || e.Op == syntax.OpMod
			l, lok := intLeafOf(e.Left)
			r, rok := intLeafOf(e.Right)
			if !rok {
				return general
			}
			if !lok {
				// `(a + b) % m`: the right operand read here, the left
				// through its closure, which runs once whatever happens.
				return func(in *Interp, ctx context.Context, env *Env) (int64, Value, bool, *Fault) {
					ln, lv, earlyReturn, fault := left(in, ctx, env)
					if fault != nil || earlyReturn {
						return 0, lv, earlyReturn, fault
					}
					if rn, rok := r.read(env); lv == nil && rok {
						if rn != 0 || !divides {
							if n, ok := intArith(e.Op, ln, rn); ok {
								return n, nil, false, nil
							}
						}
						return in.intBinary(e, ln, nil, rn, nil)
					}
					rn, rv, earlyReturn, fault := right(in, ctx, env)
					if fault != nil || earlyReturn {
						return 0, rv, earlyReturn, fault
					}
					return in.intBinary(e, ln, lv, rn, rv)
				}
			}
			// Both operands are a local or a constant, read here rather
			// than through a closure each; general handles anything that
			// isn't an int.
			return func(in *Interp, ctx context.Context, env *Env) (int64, Value, bool, *Fault) {
				ln, lok := l.read(env)
				rn, rok := r.read(env)
				if lok && rok {
					if rn != 0 || !divides {
						if n, ok := intArith(e.Op, ln, rn); ok {
							return n, nil, false, nil
						}
					}
					return in.intBinary(e, ln, nil, rn, nil)
				}
				return general(in, ctx, env)
			}
		}
	}
	value := compileExpr(e)
	return func(in *Interp, ctx context.Context, env *Env) (int64, Value, bool, *Fault) {
		v, earlyReturn, fault := value(in, ctx, env)
		if fault != nil || earlyReturn {
			return 0, v, earlyReturn, fault
		}
		if n, ok := v.(Int); ok {
			return int64(n), nil, false, nil
		}
		return 0, v, false, nil
	}
}
