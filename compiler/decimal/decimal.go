// Package decimal is the arithmetic of fiig's `decimal` type (ADR 0064): an
// exact coefficient and a scale, `19.99` being coefficient 1999 at scale 2.
// A decimal is at most 34 digits written in full (§1): at most 34
// significant digits and at most 34 after the point. Every operation is
// exact, and one whose exact result doesn't fit reports false rather than
// rounding (§2); the only rounding is Round and Quo, which name a mode (§4).
//
// The package has no dependency on the rest of the compiler, so the
// constant evaluator, the runtime, and the boundaries share one definition.
package decimal

import (
	"math/big"
	"strconv"
	"strings"
)

// Digits is the cap: 34, IEEE 754 decimal128's precision.
const Digits = 34

// Decimal is an exact decimal value. Its coefficient is never mutated once
// built, so a Decimal is safe to copy and share. The zero Decimal is 0.
type Decimal struct {
	coef  *big.Int // nil is zero
	scale int
}

var (
	ten     = big.NewInt(10)
	zeroInt = new(big.Int)
)

// pow10 is 10^n for n >= 0.
func pow10(n int) *big.Int {
	return new(big.Int).Exp(ten, big.NewInt(int64(n)), nil)
}

func (d Decimal) c() *big.Int {
	if d.coef == nil {
		return zeroInt
	}
	return d.coef
}

// Scale is the number of digits kept after the point.
func (d Decimal) Scale() int { return d.scale }

// Sign is -1, 0, or +1.
func (d Decimal) Sign() int { return d.c().Sign() }

// New is coef × 10^-scale. A negative scale multiplies out, since a decimal
// never has one. When the result is over the cap only because of trailing
// zeros, they are dropped (lowering the scale, never below zero) until it
// fits (§5); false means its significant digits don't fit.
func New(coef *big.Int, scale int) (Decimal, bool) {
	coef = new(big.Int).Set(coef)
	if scale < 0 {
		coef.Mul(coef, pow10(-scale))
		scale = 0
	}
	digits := numDigits(coef)
	if digits > Digits || scale > Digits {
		q, r := new(big.Int), new(big.Int)
		for (digits > Digits || scale > Digits) && scale > 0 {
			q.QuoRem(coef, ten, r)
			if r.Sign() != 0 {
				break
			}
			coef, q = q, coef
			scale--
			if coef.Sign() != 0 {
				digits--
			}
		}
		if digits > Digits || scale > Digits {
			return Decimal{}, false
		}
	}
	return Decimal{coef: coef, scale: scale}, true
}

func numDigits(n *big.Int) int {
	if n.Sign() == 0 {
		return 1
	}
	s := n.String()
	if s[0] == '-' {
		return len(s) - 1
	}
	return len(s)
}

// FromInt is n at scale 0. Every int fits: int64 has 19 digits.
func FromInt(n int64) Decimal {
	return Decimal{coef: big.NewInt(n)}
}

// FromRat is r as a decimal at scale, or, when scale is negative, at the
// smallest scale that holds r exactly (§7). problem completes "constant
// <expr> ..." when r has no decimal that fits.
func FromRat(r *big.Rat, scale int) (Decimal, string) {
	if scale < 0 {
		var ok bool
		if scale, ok = exactScale(r.Denom()); !ok {
			return Decimal{}, "has no exact decimal value, so it is not a decimal"
		}
	}
	n := new(big.Int).Mul(r.Num(), pow10(scale))
	if q, m := new(big.Int).QuoRem(n, r.Denom(), new(big.Int)); m.Sign() == 0 {
		if d, ok := New(q, scale); ok {
			return d, ""
		}
	}
	return Decimal{}, "does not fit in decimal (34 digits written in full)"
}

// exactScale is the fewest digits after the point that a fraction with
// denominator den needs: a finite decimal exists exactly when den has no
// prime factors but 2 and 5.
func exactScale(den *big.Int) (int, bool) {
	d := new(big.Int).Set(den)
	twos := int(d.TrailingZeroBits())
	d.Rsh(d, uint(twos))
	five, m := big.NewInt(5), new(big.Int)
	fives := 0
	for {
		q, r := new(big.Int).QuoRem(d, five, m)
		if r.Sign() != 0 {
			break
		}
		d = q
		fives++
	}
	if d.Cmp(big.NewInt(1)) != 0 {
		return 0, false
	}
	return max(twos, fives), true
}

// LiteralScale is a number literal's scale (§7): its digits after the point
// minus its exponent, floored at zero, so `1.50` is 2, `1e-2` is 2, and
// `1.5e3` is 0.
func LiteralScale(text string) int {
	mantissa, exp := text, 0
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		mantissa = text[:i]
		e, err := strconv.Atoi(strings.TrimPrefix(text[i+1:], "+"))
		if err != nil {
			return 0
		}
		exp = e
	}
	frac := 0
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		frac = len(mantissa) - i - 1
	}
	return max(frac-exp, 0)
}

// Parse reads text in JSON's number grammar, exponents included, keeping
// the scale §7 gives a literal (ADR 0064 §8). false means text isn't a
// number or its value doesn't fit. The work is bounded by the text's
// length, so a hostile `1e1000000000` costs nothing.
func Parse(text string) (Decimal, bool) {
	i, neg := 0, false
	if i < len(text) && text[i] == '-' {
		neg, i = true, 1
	}
	start := i
	for i < len(text) && isDigit(text[i]) {
		i++
	}
	intPart := text[start:i]
	if intPart == "" || (len(intPart) > 1 && intPart[0] == '0') {
		return Decimal{}, false
	}
	frac := ""
	if i < len(text) && text[i] == '.' {
		i++
		fs := i
		for i < len(text) && isDigit(text[i]) {
			i++
		}
		if frac = text[fs:i]; frac == "" {
			return Decimal{}, false
		}
	}
	exp := 0
	if i < len(text) && (text[i] == 'e' || text[i] == 'E') {
		i++
		expNeg := false
		if i < len(text) && (text[i] == '+' || text[i] == '-') {
			expNeg = text[i] == '-'
			i++
		}
		es := i
		for i < len(text) && isDigit(text[i]) {
			if exp < 1_000_000 {
				exp = exp*10 + int(text[i]-'0')
			}
			i++
		}
		if es == i {
			return Decimal{}, false
		}
		if expNeg {
			exp = -exp
		}
	}
	if i != len(text) {
		return Decimal{}, false
	}
	shift := len(frac) - exp // value = digits × 10^-shift
	digits := strings.TrimLeft(intPart+frac, "0")
	if digits == "" {
		return Decimal{coef: new(big.Int), scale: min(max(shift, 0), Digits)}, true
	}
	switch {
	case shift <= -Digits:
		return Decimal{}, false // at least 10^34
	case shift > len(digits)+Digits:
		return Decimal{}, false // below 10^-34
	}
	coef, _ := new(big.Int).SetString(digits, 10)
	if neg {
		coef.Neg(coef)
	}
	return New(coef, shift)
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// NormalizeText rewrites a number in the text grammar (ADR 0067 §7) into
// JSON's: an optional `-` or `+`, digits with an optional `.` where either
// side may be empty but not both, leading zeros allowed, and an optional
// exponent. false means text isn't in the grammar. "+.5" becomes "0.5",
// "007" becomes "7", and "5." becomes "5".
func NormalizeText(text string) (string, bool) {
	i, sign := 0, ""
	if i < len(text) && (text[i] == '-' || text[i] == '+') {
		if text[i] == '-' {
			sign = "-"
		}
		i++
	}
	start := i
	for i < len(text) && isDigit(text[i]) {
		i++
	}
	intPart := strings.TrimLeft(text[start:i], "0")
	if intPart == "" {
		intPart = "0"
	}
	digits := i - start
	frac := ""
	if i < len(text) && text[i] == '.' {
		i++
		fs := i
		for i < len(text) && isDigit(text[i]) {
			i++
		}
		frac = text[fs:i]
		digits += i - fs
	}
	if digits == 0 {
		return "", false
	}
	exp := text[i:]
	if exp != "" {
		e := exp[1:]
		if exp[0] != 'e' && exp[0] != 'E' {
			return "", false
		}
		if e != "" && (e[0] == '+' || e[0] == '-') {
			e = e[1:]
		}
		if e == "" || strings.TrimLeft(e, "0123456789") != "" {
			return "", false
		}
	}
	out := sign + intPart
	if frac != "" {
		out += "." + frac
	}
	return out + exp, true
}

// ParseText is Parse over the text grammar (ADR 0067 §7), for a string's
// toDecimalOrNone and other text that isn't JSON.
func ParseText(text string) (Decimal, bool) {
	norm, ok := NormalizeText(text)
	if !ok {
		return Decimal{}, false
	}
	return Parse(norm)
}

// FromFloat is the shortest decimal that converts back to the same float
// (ADR 0064 §6), so FromFloat(0.1) is 0.1, not the binary value's 55-digit
// expansion. false means f is outside the decimal range.
func FromFloat(f float64) (Decimal, bool) {
	return Parse(strconv.FormatFloat(f, 'e', -1, 64))
}

// Rat is d's exact value.
func (d Decimal) Rat() *big.Rat {
	return new(big.Rat).SetFrac(d.c(), pow10(d.scale))
}

// Float64 is the nearest float, ties to even. It never overflows or
// underflows, since every decimal is inside float's range.
func (d Decimal) Float64() float64 {
	f, _ := d.Rat().Float64()
	return f
}

// String is plain notation at d's scale: `1.50`, `-0.07`, `12`. Zero has no
// sign.
func (d Decimal) String() string {
	c := d.c()
	s := new(big.Int).Abs(c).String()
	if d.scale > 0 {
		if len(s) <= d.scale {
			s = strings.Repeat("0", d.scale-len(s)+1) + s
		}
		s = s[:len(s)-d.scale] + "." + s[len(s)-d.scale:]
	}
	if c.Sign() < 0 {
		s = "-" + s
	}
	return s
}

// align returns a's and b's coefficients at the larger of their scales.
func align(a, b Decimal) (*big.Int, *big.Int, int) {
	ac, bc := a.c(), b.c()
	switch {
	case a.scale < b.scale:
		return new(big.Int).Mul(ac, pow10(b.scale-a.scale)), bc, b.scale
	case b.scale < a.scale:
		return ac, new(big.Int).Mul(bc, pow10(a.scale-b.scale)), a.scale
	}
	return ac, bc, a.scale
}

// Cmp compares values, so 1.50 and 1.5 are equal (§5).
func (d Decimal) Cmp(e Decimal) int {
	a, b, _ := align(d, e)
	return a.Cmp(b)
}

// Neg is -d. It always fits, and zero stays unsigned.
func (d Decimal) Neg() Decimal {
	return Decimal{coef: new(big.Int).Neg(d.c()), scale: d.scale}
}

// Add is d + e at the larger scale.
func (d Decimal) Add(e Decimal) (Decimal, bool) {
	a, b, s := align(d, e)
	return New(new(big.Int).Add(a, b), s)
}

// Sub is d - e at the larger scale.
func (d Decimal) Sub(e Decimal) (Decimal, bool) {
	a, b, s := align(d, e)
	return New(new(big.Int).Sub(a, b), s)
}

// Mul is d × e at the sum of the scales.
func (d Decimal) Mul(e Decimal) (Decimal, bool) {
	return New(new(big.Int).Mul(d.c(), e.c()), d.scale+e.scale)
}

// Rem is d % e, truncated so it takes d's sign (ADR 0048 §2.8), at the
// larger scale. e must not be zero.
func (d Decimal) Rem(e Decimal) Decimal {
	a, b, s := align(d, e)
	r, _ := New(new(big.Int).Rem(a, b), s)
	return r
}
