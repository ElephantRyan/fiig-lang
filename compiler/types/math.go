package types

import (
	"sort"
	"strings"
)

// MathGroup is the prelude formulas group's name: the one group visible in
// every file without a `use`, and so the one name no import alias,
// parameter, or binding may take (ADR 0049 §2.5).
const MathGroup = "math"

// MathParam is one parameter of a math formula. Named marks one whose
// argument must be passed by name: closeTo's two tolerances, which would
// otherwise be easy to swap (ADR 0051).
type MathParam struct {
	Name  string
	Type  Type
	Named bool
}

// MathForm is one signature of a math formula. A member has one form, or
// one per numeric type it works on (ADR 0049 §2.4, ADR 0064): usually with
// the same parameters, in which the arguments and the result share one
// type, but decimal's round takes the places and rounding mode float's
// doesn't, so a call's arguments are bound to each form in turn
// (BindMathArgs).
type MathForm struct {
	Params []MathParam
	Result Type
}

var (
	floatType   = Primitive{Name: "float"}
	decimalType = Primitive{Name: "decimal"}
)

func orNone(t Type) Type { return Union{Members: []Type{t, NoneType{}}} }

func params(t Type, names ...string) []MathParam {
	ps := make([]MathParam, len(names))
	for i, n := range names {
		ps[i] = MathParam{Name: n, Type: t}
	}
	return ps
}

// named is a parameter that must be passed by name.
func named(name string, t Type) MathParam { return MathParam{Name: name, Type: t, Named: true} }

var (
	floatToFloat = []MathForm{{params(floatType, "x"), floatType}}
	toInt        = []MathForm{{params(floatType, "x"), intType}, {params(decimalType, "x"), intType}}
	anyOne       = []MathForm{{params(intType, "x"), intType}, {params(floatType, "x"), floatType}, {params(decimalType, "x"), decimalType}}
	anyPair      = []MathForm{{params(intType, "a", "b"), intType}, {params(floatType, "a", "b"), floatType}, {params(decimalType, "a", "b"), decimalType}}
	anyOrNone    = []MathForm{{params(intType, "a", "b"), orNone(intType)}, {params(floatType, "a", "b"), orNone(floatType)}, {params(decimalType, "a", "b"), orNone(decimalType)}}
	// ADR 0064 §4: places and rounding are required and named, since a
	// default would hide a choice a domain's conventions usually make.
	placesAndRounding = []MathParam{named("places", intType), named("rounding", RoundingType)}
)

// mathFormulas is ADR 0049 §2.4's table: the prelude math group's
// formulas, whose bodies compiler/runtime supplies. The checker types a
// `math.member(...)` call against it and the runtime dispatches through it
// (§2.8); TestEveryMathFormulaIsImplemented keeps the two in step. The
// constants (§2.7) are untyped, so they live with the constant evaluator,
// in compiler/constant.
var mathFormulas = map[string][]MathForm{
	// ADR 0048 §2.7 and ADR 0064 §6: crossing between numeric types is
	// always named.
	"floorToInt":         toInt,
	"ceilToInt":          toInt,
	"truncToInt":         toInt,
	"roundToInt":         toInt,
	"roundHalfEvenToInt": toInt,
	"toFloat":            {{params(intType, "n"), floatType}, {params(decimalType, "d"), floatType}},
	"toDecimal":          {{params(intType, "n"), decimalType}, {params(floatType, "x"), decimalType}},

	"floor": floatToFloat,
	"ceil":  floatToFloat,
	"round": {{params(floatType, "x"), floatType}, {append(params(decimalType, "x"), placesAndRounding...), decimalType}},
	"trunc": floatToFloat,
	"sqrt":  floatToFloat,
	"exp":   floatToFloat,
	"log":   floatToFloat,
	"log10": floatToFloat,
	"sin":   floatToFloat,
	"cos":   floatToFloat,
	"tan":   floatToFloat,
	"asin":  floatToFloat,
	"acos":  floatToFloat,
	"atan":  floatToFloat,
	"atan2": {{params(floatType, "y", "x"), floatType}},
	"pow":   {{params(floatType, "x", "y"), floatType}},

	"abs":      anyOne,
	"min":      anyPair,
	"max":      anyPair,
	"floorMod": anyPair,

	// ADR 0063 §2, and ADR 0064 §2's decimal forms. Decimal has no
	// divOrNone, since it has no `/`.
	"addOrNone": anyOrNone,
	"subOrNone": anyOrNone,
	"mulOrNone": anyOrNone,
	"divOrNone": {{params(floatType, "a", "b"), orNone(floatType)}},

	// ADR 0064 §3 and §3a.
	"divide":   {{append(params(decimalType, "a", "b"), placesAndRounding...), decimalType}},
	"allocate": {{[]MathParam{{Name: "amount", Type: decimalType}, named("parts", intType), named("places", intType)}, Generic{Name: "List", Args: []Type{decimalType}}}},

	// ADR 0051.
	"closeTo": {{append(params(floatType, "a", "b"),
		MathParam{Name: "relative", Type: floatType, Named: true},
		MathParam{Name: "absolute", Type: floatType, Named: true}), boolType}},
	"isZero": {{params(floatType, "x"), boolType}},

	// ADR 0065 §1: where == is refused for floats, closeTo's rule over a
	// list or a map's values.
	"containsClose": {{[]MathParam{{Name: "xs", Type: floatList}, {Name: "x", Type: floatType}, named("relative", floatType), named("absolute", floatType)}, boolType}},
	"allClose": {
		{[]MathParam{{Name: "a", Type: floatList}, {Name: "b", Type: floatList}, named("relative", floatType), named("absolute", floatType)}, boolType},
		{[]MathParam{{Name: "a", Type: floatMap}, {Name: "b", Type: floatMap}, named("relative", floatType), named("absolute", floatType)}, boolType},
	},
}

// floatMap is a map of floats with any key: Unknown matches every key type.
var floatMap = Generic{Name: "Map", Args: []Type{Unknown{}, floatType}}

var floatList = Generic{Name: "List", Args: []Type{floatType}}

// BindMathArgs binds a call's arguments to ps: argNames holds each
// argument's name, "" for a positional one. It returns, for each parameter,
// the index of the argument that supplies it, and false when the arguments
// don't fit ps: too many, a name ps doesn't have, a name given twice, or a
// parameter left without one. An argument passed by position to a
// parameter that must be named still binds; the checker reports it.
func BindMathArgs(ps []MathParam, argNames []string) ([]int, bool) {
	at := make([]int, len(ps))
	for i := range at {
		at[i] = -1
	}
	next := 0
	for ai, name := range argNames {
		if name == "" {
			for next < len(ps) && at[next] >= 0 {
				next++
			}
			if next == len(ps) {
				return nil, false
			}
			at[next] = ai
			continue
		}
		pi := -1
		for i, p := range ps {
			if p.Name == name {
				pi = i
			}
		}
		if pi < 0 || at[pi] >= 0 {
			return nil, false
		}
		at[pi] = ai
	}
	for _, ai := range at {
		if ai < 0 {
			return nil, false
		}
	}
	return at, true
}

// MathFormula is the forms of the math formula named member, or false when
// math has no such formula.
func MathFormula(member string) ([]MathForm, bool) {
	forms, ok := mathFormulas[member]
	return forms, ok
}

// MathFormulaNames is every math formula's name, sorted.
func MathFormulaNames() []string {
	names := make([]string, 0, len(mathFormulas))
	for n := range mathFormulas {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// String is the form as a signature: `(a: int, b: int) -> int`.
func (f MathForm) String() string {
	ps := make([]string, len(f.Params))
	for i, p := range f.Params {
		ps[i] = p.Name + ": " + p.Type.String()
	}
	return "(" + strings.Join(ps, ", ") + ") -> " + f.Result.String()
}
