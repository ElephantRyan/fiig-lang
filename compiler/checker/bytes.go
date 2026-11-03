package checker

import (
	"fiig-lang/compiler/constant"
	"fiig-lang/compiler/format"
	"strings"
	"unicode/utf8"

	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0058: bytes, binary patterns, and binary expressions. Every
// compile-time segment error is A14; a value of the wrong type for its
// encoding is the ordinary A1.

var (
	intType    = types.Primitive{Name: "int"}
	stringType = types.Primitive{Name: "string"}
	boolType   = types.Primitive{Name: "bool"}
	bytesType  = types.Primitive{Name: "bytes"}
)

// method is a builtin method's signature; the tables live in
// compiler/types (ADR 0049 §2.8), shared with compiler/runtime.
type method = types.Method

// inferMethodCall types `receiver.method(args)` once the receiver is known
// to be a value, not a declaration or package. A method that isn't in the
// receiver type's table is K1 (ADR 0049 §2.6), including on a type with no
// table at all; an Unknown receiver was already reported, or is a gap in
// inference, and gets no second diagnostic.
func (c *checker) inferMethodCall(recv types.Type, fa *syntax.FieldAccess, args []syntax.Arg, start lexer.Pos, env map[string]types.Type) types.Type {
	if iface, ok := types.Interface(recv); ok {
		return c.callProvider(iface, "a provider", fa.Field, args, start, env)
	}
	if _, unknown := recv.(types.Unknown); !unknown {
		kind, table, ok := types.MethodsOf(recv)
		if m, found := table[fa.Field]; ok && found {
			if c.methodCalls == nil {
				c.methodCalls = map[*syntax.FieldAccess]bool{}
			}
			c.methodCalls[fa] = true
			if kind == "List" {
				c.checkListElement(recv.(types.Generic).Args[0], fa.Field, start)
			}
			return c.checkMethodArgs(kind, fa.Field, m, args, start, env)
		}
		if ok {
			c.report(start, "K1", "%s has no method %q; its methods are %s", kind, fa.Field, methodList(table))
		} else {
			c.report(start, "K1", "%s has no methods, so it has no %q", recv, fa.Field)
		}
	}
	for _, a := range args {
		c.inferExpr(a.Value, env)
	}
	return types.Unknown{}
}

func methodList(table map[string]method) string {
	names := types.MethodNames(table)
	for i, n := range names {
		names[i] = n + "()"
	}
	return strings.Join(names, ", ")
}

func (c *checker) checkMethodArgs(recv, name string, m method, args []syntax.Arg, start lexer.Pos, env map[string]types.Type) types.Type {
	if len(args) != len(m.Params) {
		c.report(start, "", "%s.%s takes %d argument(s), got %d", recv, name, len(m.Params), len(args))
		for _, a := range args {
			c.inferExpr(a.Value, env)
		}
		return m.Result
	}
	for i, a := range args {
		if a.Name != "" {
			c.report(start, "", "%s.%s takes positional arguments", recv, name)
		}
		c.checkExpected(a.Value, m.Params[i], env)
	}
	return m.Result
}

func encodingType(enc syntax.Encoding) types.Type {
	switch enc.Kind {
	case syntax.SegBytes:
		return bytesType
	case syntax.SegUTF8:
		return stringType
	default:
		return intType
	}
}

// encodingOK reports A14 for an illegal integer encoding name.
func (c *checker) encodingOK(enc syntax.Encoding) bool {
	if enc.Kind != syntax.SegInt {
		return true
	}
	if msg := syntax.IntEncodingError(enc.Name); msg != "" {
		c.report(enc.Start, "A14", "%s", msg)
		return false
	}
	return true
}

// literalFits reports A14 when an integer literal cannot be represented in
// its segment's width.
func (c *checker) literalFits(e syntax.Expr, enc syntax.Encoding) {
	cv, ok, err := constant.Eval(e)
	if !ok || err != nil {
		return
	}
	v, problem := constant.ToInt(cv)
	if problem != "" {
		return
	}
	lo, hi := int64(0), int64(1)<<enc.Bits-1
	if enc.Signed {
		lo, hi = -(int64(1) << (enc.Bits - 1)), int64(1)<<(enc.Bits-1)-1
	}
	if enc.Bits >= 64 {
		return
	}
	if v < lo || v > hi {
		start, _ := e.(syntax.Spanned).Bounds()
		c.report(start, "A14", "%s does not fit in %s (%d to %d)", format.Expr(e), enc.Name, lo, hi)
	}
}

// alignment tracks a segment list's bit offset. It stops tracking after an
// illegal encoding, whose width is unknown, so one mistake gives one error.
type alignment struct {
	bits  int
	known bool
}

func (c *checker) alignSegment(a *alignment, seg syntax.Segment, ok bool) {
	if !ok {
		a.known = false
		return
	}
	if !a.known {
		return
	}
	if seg.Encoding.Kind == syntax.SegInt {
		a.bits += seg.Encoding.Bits
		return
	}
	if a.bits%8 != 0 {
		c.report(seg.Start, "A14", "%s segment starts at bit %d, which is not a byte boundary; the integer fields before it must add up to whole bytes", seg.Encoding.Name, a.bits)
		a.known = false
	}
}

func (c *checker) alignEnd(a alignment, at lexer.Pos) {
	if a.known && a.bits%8 != 0 {
		c.report(at, "A14", "binary segments end at bit %d, which is not a byte boundary; the fields must add up to whole bytes", a.bits)
	}
}

// inferBytesLit is §4: `<<value: encoding, ...>>` builds a new bytes.
func (c *checker) inferBytesLit(lit *syntax.BytesLit, env map[string]types.Type) types.Type {
	a := alignment{known: true}
	for _, seg := range lit.Segments {
		enc := seg.Encoding
		ok := c.encodingOK(enc)
		if enc.Size != nil {
			c.report(enc.Start, "A14", "in a binary expression %s takes no size; the value's own length is used", enc.Name)
		}
		c.checkExpected(seg.Value, encodingType(enc), env)
		if ok && enc.Kind == syntax.SegInt {
			c.literalFits(seg.Value, enc)
		}
		c.alignSegment(&a, seg, ok)
	}
	c.alignEnd(a, lit.Start)
	return bytesType
}

// bindBytesPattern is §3: checks a binary pattern against its subject and
// binds each named segment in env.
func (c *checker) bindBytesPattern(p *syntax.BytesPattern, subject types.Type, env map[string]types.Type) {
	switch subject.(type) {
	case types.Unknown:
	default:
		if !types.Equal(subject, bytesType) {
			c.report(p.Start, "A1", "a binary pattern matches bytes, got %s", subject)
		}
	}
	bound := map[string]bool{}
	a := alignment{known: true}
	for i, seg := range p.Segments {
		enc := seg.Encoding
		ok := c.encodingOK(enc)
		last := i == len(p.Segments)-1
		if enc.Size != nil {
			c.checkSize(enc.Size, bound)
		} else if enc.Kind == syntax.SegBytes && !last {
			c.report(enc.Start, "A14", "bytes with no size takes the rest of the input, so it must be the last segment; give it a size, bytes(n)")
		} else if enc.Kind == syntax.SegUTF8 && !isStringLiteral(seg.Literal) {
			c.report(enc.Start, "A14", "utf8 in a pattern needs a size, utf8(n)")
		}
		if seg.Literal != nil {
			c.checkSegmentLiteral(seg)
		}
		c.alignSegment(&a, seg, ok)
		switch {
		case seg.Name == "" || seg.Name == "_":
		case bound[seg.Name]:
			c.report(seg.Start, "A14", "%s is bound twice in one binary pattern", seg.Name)
		default:
			bound[seg.Name] = true
			env[seg.Name] = encodingType(enc)
		}
	}
	c.alignEnd(a, p.Start)
}

func isStringLiteral(e syntax.Expr) bool {
	_, ok := e.(*syntax.StringLit)
	return ok
}

// checkSegmentLiteral checks a literal segment: an int literal with an
// integer encoding, or a plain string literal with utf8, whose size, if
// written, must be the literal's own byte length.
func (c *checker) checkSegmentLiteral(seg syntax.Segment) {
	enc := seg.Encoding
	switch lit := seg.Literal.(type) {
	case *syntax.IntLit:
		if enc.Kind != syntax.SegInt {
			c.report(lit.Start, "A14", "an integer literal segment needs an integer encoding, not %s", enc.Name)
			return
		}
		c.landConstant(lit, intType)
		if syntax.IntEncodingError(enc.Name) == "" {
			c.literalFits(lit, enc)
		}
	case *syntax.StringLit:
		if enc.Kind != syntax.SegUTF8 {
			c.report(lit.Start, "A14", "a string literal segment needs utf8, not %s", enc.Name)
			return
		}
		text, plain := plainText(lit)
		if !plain {
			c.report(lit.Start, "A14", "a string literal in a pattern cannot interpolate")
			return
		}
		if size, ok := enc.Size.(*syntax.IntLit); ok {
			if n, err := syntax.IntValue(size.Text); err == nil && n != int64(len(text)) {
				c.report(size.Start, "A14", "utf8(%d) does not match the literal %q, which is %d bytes", n, text, len(text))
			}
		}
	}
}

func plainText(lit *syntax.StringLit) (string, bool) {
	s := ""
	for _, part := range lit.Parts {
		if part.Interp != nil {
			return "", false
		}
		s += part.Text
	}
	return s, utf8.ValidString(s)
}

// checkSize is §3's size rule: an int expression over literals and names
// bound by earlier segments of the same pattern, with arithmetic. No
// calls and no outside names, so a pattern's layout is readable from the
// pattern alone.
func (c *checker) checkSize(e syntax.Expr, bound map[string]bool) {
	if _, ok := c.landConstant(e, intType); ok {
		return
	}
	switch e := e.(type) {
	case *syntax.Ident:
		if !bound[e.Name] {
			c.report(e.Start, "A14", "a segment size may use only literals and names bound by earlier segments of the same pattern; %s is not one", e.Name)
		}
	case *syntax.ParenExpr:
		c.checkSize(e.Inner, bound)
	case *syntax.NegExpr:
		c.checkSize(e.Operand, bound)
	case *syntax.BinaryExpr:
		switch e.Op {
		case syntax.OpAdd, syntax.OpSub, syntax.OpMul, syntax.OpDiv, syntax.OpMod:
			c.checkSize(e.Left, bound)
			c.checkSize(e.Right, bound)
		default:
			c.report(e.Start, "A14", "a segment size is integer arithmetic (+ - * / %%), not %s", e.Op)
		}
	default:
		start, _ := e.(syntax.Spanned).Bounds()
		c.report(start, "A14", "a segment size may use only literals, earlier segment names, and + - * / %%")
	}
}

// checkBytesCatchAll is §3's exhaustiveness rule: no set of binary patterns
// covers every bytes value, so a match using one needs a catch-all arm.
func (c *checker) checkBytesCatchAll(s *syntax.MatchStmt) {
	has := false
	for _, arm := range s.Arms {
		if _, ok := arm.Pattern.(*syntax.BytesPattern); ok {
			has = true
		}
	}
	if !has {
		return
	}
	for _, arm := range s.Arms {
		if all, _ := patternCoverage(arm.Pattern, false); all {
			return
		}
	}
	c.report(s.Start, "A9", "match is not exhaustive: no set of binary patterns covers every bytes value, so it needs a catch-all arm (`_ -> ...`)")
}
