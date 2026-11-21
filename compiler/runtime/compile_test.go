package runtime

import (
	"context"
	"testing"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0115 §1, §4: compiled bodies give exactly what the tree walk gives,
// values and fault messages both. Each program runs both ways.
func TestCompiledBodiesMatchTheTreeWalk(t *testing.T) {
	cases := []struct {
		name string
		src  []string
		call string
		args map[string]Value
	}{
		{"arithmetic", []string{`rule f(a: int, b: int) -> int {
    return (a + b) * (a - b) / 3 % 7
}`}, "f", map[string]Value{"a": Int(100), "b": Int(41)}},
		{"overflow", []string{`rule f(a: int) -> int {
    return a * a
}`}, "f", map[string]Value{"a": Int(1 << 40)}},
		{"division by zero", []string{`rule f(a: int) -> int {
    return 10 % a
}`}, "f", map[string]Value{"a": Int(0)}},
		{"floats", []string{`rule f(a: float, b: float) -> float {
    return a * b + 0.5
}`}, "f", map[string]Value{"a": Float(1.5), "b": Float(4)}},
		{"comparisons", []string{`rule f(a: int, s: string) -> List<bool> {
    return [a < 3, a <= 3, a > 3, a >= 3, a == 3, a != 3, s == "x", s < "y", not (a == 3) or false]
}`}, "f", map[string]Value{"a": Int(3), "s": Str("x")}},
		{"strings", []string{`rule f(n: int) -> string {
    return "n is ${n + 1}, plain"
}`}, "f", map[string]Value{"n": Int(41)}},
		{"if chain", []string{`rule f(n: int) -> string {
    if n < 0 {
        return "negative"
    } else if n == 0 {
        label = "zero"
        return label
    } else {
        return "positive"
    }
}`}, "f", map[string]Value{"n": Int(0)}},
		{"while, break, continue", []string{`rule f(n: int) -> int {
    total: int = 0
    i: int = 0
    outer: while i < n {
        i = i + 1
        if i % 2 == 0 {
            continue
        }
        j: int = 0
        while j < 10 {
            j = j + 1
            if j == 3 {
                continue outer
            }
            total = total + j
        }
        if i > 7 {
            break
        }
    }
    return total * 100 + i
}`}, "f", map[string]Value{"n": Int(20)}},
		{"for over a list, bytes and a map", []string{`rule f(xs: List<int>, data: bytes) -> int {
    total: int = 0
    for x in xs {
        total = total + x
    }
    for b in data {
        total = total * 2 + b
    }
    m: Map<string, int> = ["a": 1, "b": 2]
    for k, v in m {
        total = total + v
    }
    return total
}`}, "f", map[string]Value{"xs": intList(1, 2, 3), "data": Bytes{B: []byte{1, 2}}}},
		{"match", []string{`rule f(r: Result<int, string>) -> string {
    match r {
        Ok(n) -> {
            if n > 1 {
                return "big"
            }
            return "small"
        }
        Err(reason) -> reason
    }
}`}, "f", map[string]Value{"r": Result{Ok: true, Value: Int(5)}}},
		{"try", []string{`rule f(r: Result<int, string>) -> Result<int, string> {
    n = r?
    return Ok(n + 1)
}`}, "f", map[string]Value{"r": Result{Ok: false, Value: Str("no")}}},
		{"calls with named arguments", []string{`conversion f(a: int) -> int {
    return g(b: a, a: 1) + g(a, 2)
}`, `rule g(a: int, b: int) -> int {
    return a * 10 + b
}`}, "f", map[string]Value{"a": Int(7)}},
		{"recursion", []string{`conversion fib(n: int) -> int {
    if n < 2 {
        return n
    }
    return fib(n - 1) + fib(n - 2)
}`}, "fib", map[string]Value{"n": Int(15)}},
		{"methods", []string{`rule f(data: bytes, s: string) -> int {
    return data.slice(1, 3).length() + data.at(0) + s.length()
}`}, "f", map[string]Value{"data": Bytes{B: []byte{9, 8, 7, 6}}, "s": Str("four")}},
		{"a method fault", []string{`rule f(data: bytes) -> int {
    return data.at(10)
}`}, "f", map[string]Value{"data": Bytes{B: []byte{1}}}},
		{"records", []string{`model P {
    x: int
    y: int
}`, `rule f(n: int) -> int {
    p = P(x: n, y: n + 1)
    return p.x * p.y
}`}, "f", map[string]Value{"n": Int(6)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var files []*syntax.File
			for _, s := range c.src {
				f, errs := parser.Parse(s)
				if len(errs) > 0 {
					t.Fatalf("parse: %v", errs)
				}
				files = append(files, f)
			}
			universe, _ := types.BuildUniverse(files)
			if diags := checker.Check(files, nil); len(diags) > 0 {
				t.Fatalf("check: %v", diags)
			}
			run := func(walk bool) (Value, *Fault) {
				in := newTestInterp(universe, nil)
				in.walkOnly = walk
				return in.Call(context.Background(), c.call, c.args)
			}
			walked, walkFault := run(true)
			compiled, compiledFault := run(false)
			if (walkFault == nil) != (compiledFault == nil) {
				t.Fatalf("tree walk %v / %v, compiled %v / %v", walked, walkFault, compiled, compiledFault)
			}
			if walkFault != nil {
				if walkFault.Message != compiledFault.Message {
					t.Fatalf("fault: tree walk %q, compiled %q", walkFault.Message, compiledFault.Message)
				}
				return
			}
			if !Equal(walked, compiled) || walked.String() != compiled.String() {
				t.Fatalf("tree walk %s, compiled %s", walked, compiled)
			}
		})
	}
}
