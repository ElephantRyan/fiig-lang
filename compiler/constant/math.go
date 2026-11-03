package constant

import (
	"math"
	"math/big"
	"sort"
)

// The prelude math group's constant members (ADR 0049 §2.7). They are
// untyped constants like any literal, so `math.pi * 2` needs no conversion
// and `math.maxInt + 1` is a compile error rather than a fault. pi and e
// are irrational, so their values are 100-digit approximations, far past
// the 17 digits rounding to a float needs; tau is exactly twice pi's. All
// three are marked Irrational, so none lands as a decimal (ADR 0064 §7).
var mathConstants = map[string]Value{
	"pi":       irrational("3.141592653589793238462643383279502884197169399375105820974944592307816406286208998628034825342117068"),
	"e":        irrational("2.718281828459045235360287471352662497757247093699959574966967627724076630353547594571382178525166427"),
	"minInt":   {Kind: Int, Rat: new(big.Rat).SetInt64(math.MinInt64)},
	"maxInt":   {Kind: Int, Rat: new(big.Rat).SetInt64(math.MaxInt64)},
	"maxFloat": {Kind: Float, Rat: new(big.Rat).SetFloat64(math.MaxFloat64)},
}

func init() {
	pi := mathConstants["pi"]
	mathConstants["tau"] = Value{Kind: Float, Rat: new(big.Rat).Mul(pi.Rat, big.NewRat(2, 1)), Irrational: true}
}

func irrational(digits string) Value {
	r, _ := new(big.Rat).SetString(digits)
	return Value{Kind: Float, Rat: r, Irrational: true}
}

// MathConstant is the value of the math constant named member, or false
// when math has no such constant.
func MathConstant(member string) (Value, bool) {
	v, ok := mathConstants[member]
	return v, ok
}

// MathConstantNames is every math constant's name, sorted.
func MathConstantNames() []string {
	names := make([]string, 0, len(mathConstants))
	for n := range mathConstants {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
