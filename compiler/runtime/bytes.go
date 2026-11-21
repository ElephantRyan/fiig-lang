package runtime

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"fiig-lang/compiler/syntax"
)

// Bytes is ADR 0058's `bytes`: an immutable byte sequence. Like List, B is
// a view of a buffer several values may share, and used counts the
// buffer's claimed bytes for claim-the-slot growth (ADR 0057 §5). used is
// non-nil only for a value grown by a binary expression, whose B always
// starts at its buffer's start; a slice or a matched segment has none, and
// its capacity is clipped to its length so growing it always copies.
type Bytes struct {
	B    []byte
	used *atomic.Int64
}

func (Bytes) isValue() {}
func (b Bytes) String() string {
	parts := make([]string, len(b.B))
	for i, x := range b.B {
		parts[i] = strconv.Itoa(int(x))
	}
	return "<<" + strings.Join(parts, ", ") + ">>"
}

// bytesCopies counts growths that had to copy, for the cost test.
var bytesCopies atomic.Int64

// bitWriter appends bits most significant first.
type bitWriter struct {
	buf   []byte
	nbits int
}

func (w *bitWriter) writeBits(v uint64, n int) {
	for i := n - 1; i >= 0; i-- {
		if w.nbits%8 == 0 {
			w.buf = append(w.buf, 0)
		}
		if v>>uint(i)&1 == 1 {
			w.buf[len(w.buf)-1] |= 1 << uint(7-w.nbits%8)
		}
		w.nbits++
	}
}

func (w *bitWriter) writeBytes(b []byte) {
	if w.nbits%8 == 0 {
		w.buf = append(w.buf, b...)
		w.nbits += 8 * len(b)
		return
	}
	for _, x := range b {
		w.writeBits(uint64(x), 8)
	}
}

// writeInt encodes v in enc's width and byte order. A little-endian field
// is whole bytes, written lowest byte first.
func (w *bitWriter) writeInt(v int64, enc syntax.Encoding) {
	u := uint64(v)
	if enc.Bits < 64 {
		u &= 1<<uint(enc.Bits) - 1
	}
	if w.nbits%8 == 0 && enc.Bits%8 == 0 {
		// Whole bytes on a byte boundary, as nearly every field is
		// (ADR 0115 §4).
		n := enc.Bits / 8
		for i := range n {
			shift := 8 * (n - 1 - i)
			if enc.Little {
				shift = 8 * i
			}
			w.buf = append(w.buf, byte(u>>uint(shift)))
		}
		w.nbits += enc.Bits
		return
	}
	if !enc.Little {
		w.writeBits(u, enc.Bits)
		return
	}
	for i := 0; i < enc.Bits/8; i++ {
		w.writeBits(u>>uint(8*i)&0xff, 8)
	}
}

// intFits reports whether v is representable in enc (§4: a value that
// does not fit faults rather than truncating).
func intFits(v int64, enc syntax.Encoding) bool {
	if enc.Signed {
		if enc.Bits >= 64 {
			return true
		}
		lim := int64(1) << uint(enc.Bits-1)
		return v >= -lim && v < lim
	}
	return v >= 0 && (enc.Bits >= 63 || v < int64(1)<<uint(enc.Bits))
}

// evalBytesLit is §4: encode each segment in order. When the first
// segment is an existing bytes value, the rest is appended to its buffer
// in place if that value is the newest one on it (claim-the-slot).
func (in *Interp) evalBytesLit(ctx context.Context, e *syntax.BytesLit, env *Env) (Value, bool, *Fault) {
	var head *Bytes
	segs := e.Segments
	if len(segs) > 0 && segs[0].Encoding.Kind == syntax.SegBytes {
		v, earlyReturn, fault := in.evalExpr(ctx, segs[0].Value, env)
		if fault != nil || earlyReturn {
			return v, earlyReturn, fault
		}
		b, ok := v.(Bytes)
		if !ok {
			return nil, false, Faultf("bytes segment value is not bytes (got %s)", v)
		}
		head, segs = &b, segs[1:]
	}
	var w bitWriter
	for _, seg := range segs {
		v, earlyReturn, fault := in.evalExpr(ctx, seg.Value, env)
		if fault != nil || earlyReturn {
			return v, earlyReturn, fault
		}
		switch seg.Encoding.Kind {
		case syntax.SegInt:
			n, ok := v.(Int)
			if !ok {
				return nil, false, Faultf("%s segment value is not an int (got %s)", seg.Encoding.Name, v)
			}
			if !intFits(int64(n), seg.Encoding) {
				return nil, false, Faultf("%d does not fit in %s", n, seg.Encoding.Name)
			}
			w.writeInt(int64(n), seg.Encoding)
		case syntax.SegBytes:
			b, ok := v.(Bytes)
			if !ok {
				return nil, false, Faultf("bytes segment value is not bytes (got %s)", v)
			}
			w.writeBytes(b.B)
		case syntax.SegUTF8:
			s, ok := v.(Str)
			if !ok {
				return nil, false, Faultf("utf8 segment value is not a string (got %s)", v)
			}
			w.writeBytes([]byte(s))
		}
	}
	if w.nbits%8 != 0 {
		return nil, false, Faultf("binary expression is %d bits, not whole bytes", w.nbits)
	}
	if head == nil {
		return Bytes{B: w.buf}, false, nil
	}
	return growBytes(*head, w.buf), false, nil
}

// growBytes is ADR 0057 §5 for bytes: claim len(tail) slots after b when b
// ends where its buffer's claimed bytes end and there is room, otherwise
// copy into a buffer of twice the size.
func growBytes(b Bytes, tail []byte) Bytes {
	n, m := len(b.B), len(tail)
	if m == 0 {
		return b
	}
	if b.used != nil && n+m <= cap(b.B) && b.used.CompareAndSwap(int64(n), int64(n+m)) {
		out := b.B[:n+m]
		copy(out[n:], tail)
		return Bytes{B: out, used: b.used}
	}
	bytesCopies.Add(1)
	size := 2 * (n + m)
	if size < 16 {
		size = 16
	}
	out := make([]byte, n+m, size)
	copy(out, b.B)
	copy(out[n:], tail)
	used := new(atomic.Int64)
	used.Store(int64(n + m))
	return Bytes{B: out, used: used}
}

// bitReader reads bits most significant first.
type bitReader struct {
	data []byte
	pos  int // in bits
}

func (r *bitReader) remaining() int { return 8*len(r.data) - r.pos }

func (r *bitReader) readBits(n int) uint64 {
	var v uint64
	for i := 0; i < n; i++ {
		bit := r.data[r.pos/8] >> uint(7-r.pos%8) & 1
		v = v<<1 | uint64(bit)
		r.pos++
	}
	return v
}

func (r *bitReader) readInt(enc syntax.Encoding) int64 {
	var u uint64
	if r.pos%8 == 0 && enc.Bits%8 == 0 {
		// Whole bytes on a byte boundary (ADR 0115 §4).
		n, at := enc.Bits/8, r.pos/8
		for i := range n {
			if enc.Little {
				u |= uint64(r.data[at+i]) << uint(8*i)
			} else {
				u = u<<8 | uint64(r.data[at+i])
			}
		}
		r.pos += enc.Bits
	} else if enc.Little {
		for i := 0; i < enc.Bits/8; i++ {
			u |= r.readBits(8) << uint(8*i)
		}
	} else {
		u = r.readBits(enc.Bits)
	}
	if enc.Signed && enc.Bits < 64 && u>>uint(enc.Bits-1)&1 == 1 {
		u |= ^uint64(0) << uint(enc.Bits)
	}
	return int64(u)
}

// matchBytesPattern is §3. A pattern matches only if every segment decodes
// and the segments consume the whole input; any failure, including short
// input, a bad size, a failed literal, or invalid UTF-8, is a non-match,
// never a fault. Matched bytes and utf8 segments are views of the input.
func matchBytesPattern(p *syntax.BytesPattern, v Value, env *Env) bool {
	b, ok := v.(Bytes)
	if !ok {
		return false
	}
	r := bitReader{data: b.B}
	// The names bound so far, in segment order, so a size can read them
	// and env binds them in the same order on every match (ADR 0115 §3).
	var inline [8]patternBinding
	bound := patternBindings(inline[:0])
	for _, seg := range p.Segments {
		enc := seg.Encoding
		var val Value
		if enc.Kind == syntax.SegInt {
			if r.remaining() < enc.Bits {
				return false
			}
			val = Int(r.readInt(enc))
		} else {
			var n int64
			switch {
			case enc.Size != nil:
				size, ok := evalSize(enc.Size, bound)
				if !ok {
					return false
				}
				n = size
			case seg.Literal != nil:
				n = int64(len(literalText(seg.Literal)))
			default:
				n = int64(r.remaining() / 8)
			}
			if n < 0 || n > int64(r.remaining()/8) || r.pos%8 != 0 {
				return false
			}
			start := r.pos / 8
			view := b.B[start : start+int(n) : start+int(n)]
			r.pos += 8 * int(n)
			if enc.Kind == syntax.SegUTF8 {
				if !utf8.Valid(view) {
					return false
				}
				val = Str(view)
			} else {
				val = Bytes{B: view}
			}
		}
		if seg.Literal != nil {
			if !literalMatches(seg.Literal, val) {
				return false
			}
			continue
		}
		if seg.Name != "_" {
			bound = append(bound, patternBinding{seg.Name, val})
		}
	}
	if r.remaining() != 0 {
		return false
	}
	for _, b := range bound {
		env.Bind(b.name, b.v)
	}
	return true
}

type patternBinding struct {
	name string
	v    Value
}

// patternBindings is a pattern's names bound so far.
type patternBindings []patternBinding

// get is name's value, the latest when a name is bound twice.
func (p patternBindings) get(name string) (Value, bool) {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i].name == name {
			return p[i].v, true
		}
	}
	return nil, false
}

func literalText(e syntax.Expr) string {
	lit, ok := e.(*syntax.StringLit)
	if !ok {
		return ""
	}
	var sb strings.Builder
	for _, part := range lit.Parts {
		sb.WriteString(part.Text)
	}
	return sb.String()
}

func literalMatches(e syntax.Expr, v Value) bool {
	switch lit := e.(type) {
	case *syntax.IntLit:
		if c, ok := constValue(lit); ok {
			return v == c
		}
		n, err := syntax.IntValue(lit.Text)
		return err == nil && v == Int(n)
	case *syntax.StringLit:
		return v == Str(literalText(lit))
	}
	return false
}

// evalSize evaluates a segment size: literals, earlier segment names, and
// integer arithmetic. Division by zero is a non-match, like any other bad
// size.
func evalSize(e syntax.Expr, bound patternBindings) (int64, bool) {
	if v, ok := constValue(e); ok {
		n, isInt := v.(Int)
		return int64(n), isInt
	}
	switch e := e.(type) {
	case *syntax.IntLit:
		n, err := syntax.IntValue(e.Text)
		return n, err == nil
	case *syntax.Ident:
		v, _ := bound.get(e.Name)
		n, ok := v.(Int)
		return int64(n), ok
	case *syntax.ParenExpr:
		return evalSize(e.Inner, bound)
	case *syntax.NegExpr:
		n, ok := evalSize(e.Operand, bound)
		if !ok {
			return 0, false
		}
		return intNeg(n)
	case *syntax.BinaryExpr:
		l, ok1 := evalSize(e.Left, bound)
		r, ok2 := evalSize(e.Right, bound)
		if !ok1 || !ok2 {
			return 0, false
		}
		if (e.Op == syntax.OpDiv || e.Op == syntax.OpMod) && r == 0 {
			return 0, false
		}
		// An overflowing size is a non-match, like a zero divisor (ADR 0063
		// §4): a wrapped size can land on a small, plausible length.
		return intArith(e.Op, l, r)
	}
	return 0, false
}

func bytesEqual(a, b Bytes) bool { return bytes.Equal(a.B, b.B) }
