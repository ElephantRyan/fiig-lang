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
		// Slots (ADR 0115 §3): a name found at a different place on a
		// later run, and ints held unboxed then read as values.
		{"slots across callers", []string{`conversion f(a: int) -> int {
    total: int = 0
    for x in [1, 2, 3] {
        total = total + g(b: x, a: a) + g(a, x)
    }
    return total
}`, `rule g(a: int, b: int) -> int {
    c = a * 10
    return c + b
}`}, "f", map[string]Value{"a": Int(7)}},
		{"slots across match arms", []string{`rule f(rs: List<Result<int, string>>) -> string {
    out: string = ""
    n: int = 0
    for r in rs {
        match r {
            Ok(v) -> {
                n = n + v
                out = "${out}${v},"
            }
            Err(reason) -> {
                out = "${out}${reason}:${n},"
            }
        }
    }
    return out
}`}, "f", map[string]Value{"rs": List{Elems: []Value{
			Result{Ok: true, Value: Int(1)}, Result{Ok: false, Value: Str("a")},
			Result{Ok: true, Value: Int(70000)}, Result{Ok: false, Value: Str("b")}}}}},
		{"unboxed ints as values", []string{`rule f(data: bytes) -> List<int> {
    sum: int = 0
    seen: List<int> = []
    for b in data {
        sum = sum * 31 + b
        seen = append(seen, sum)
        seen = append(seen, b)
    }
    if [sum, 1] == seen {
        return seen
    }
    return [sum, sum % 7]
}`}, "f", map[string]Value{"data": Bytes{B: []byte{200, 3, 255, 0, 17}}}},
		{"arithmetic on locals and constants", []string{`rule f(a: int, x: float) -> List<string> {
    big = a * 1000000000
    return ["${(a + 1) % 7}", "${(a * 3) / 2}", "${(x + 1.5) * 2.0}", "${a % 5 + a / 3}"]
}`}, "f", map[string]Value{"a": Int(40), "x": Float(0.25)}},
		{"a left operand that faults", []string{`rule f(a: int) -> int {
    return (a * a) % 9
}`}, "f", map[string]Value{"a": Int(1 << 40)}},
		{"division by a zero local", []string{`rule f(a: int, z: int) -> int {
    return (a + 1) / z
}`}, "f", map[string]Value{"a": Int(4), "z": Int(0)}},
		{"overflow on locals", []string{`rule f(a: int) -> int {
    b = a
    return a * b
}`}, "f", map[string]Value{"a": Int(1 << 40)}},
		{"bytes literals", []string{`rule f(n: int, s: string, tail: bytes) -> List<bytes> {
    framed = <<68: u8, n + 4: i32be, s: utf8, tail: bytes>>
    grown = <<framed: bytes, n: u16le, n % 256: u8>>
    bits = <<1: u1, 5: u3, n % 16: u4, n: i64le>>
    return [framed, grown, bits, <<>>, <<grown: bytes>>]
}`}, "f", map[string]Value{"n": Int(300), "s": Str("héllo"), "tail": Bytes{B: []byte{1, 2, 3}}}},
		{"a value that does not fit", []string{`rule f(n: int) -> bytes {
    return <<1: u8, n: u8>>
}`}, "f", map[string]Value{"n": Int(256)}},
		{"growing bytes in a loop", []string{`rule f(n: int) -> bytes {
    data: bytes = <<>>
    i: int = 0
    while i < n {
        data = <<data: bytes, i: u16be, (i * 7) % 256: u8>>
        i = i + 1
    }
    return data
}`}, "f", map[string]Value{"n": Int(40)}},
		{"binary patterns", []string{`rule f(stream: bytes) -> List<int> {
    out: List<int> = []
    rest: bytes = stream
    while rest.length() > 0 {
        match rest {
            <<68: u8, length: i32be, body: bytes(length - 4), more: bytes>> -> {
                match body {
                    <<a: u16le, b: i8, "ok": utf8, c: u1, d: u7>> -> {
                        out = append(out, a + b + c + d)
                    }
                    _ -> {
                        out = append(out, body.length())
                    }
                }
                rest = more
            }
            _ -> {
                return [0 - 1]
            }
        }
    }
    return out
}`}, "f", map[string]Value{"stream": Bytes{B: []byte{
			'D', 0, 0, 0, 10, 0x34, 0x12, 0xff, 'o', 'k', 0x85,
			'D', 0, 0, 0, 6, 9, 9,
			'D', 0, 0, 0, 3}}}},
		{"methods on values", []string{`rule f(a: int, b: int, data: bytes) -> List<int> {
    x = 0
    match <<a: u32be>>.xor(<<b: u32be>>) {
        <<v: u32be>> -> {
            return [v, data.slice(1, 3).length(), [1, 2, 3].length(), (data).at(0), "four".length()]
        }
        _ -> {
            return [x]
        }
    }
}`}, "f", map[string]Value{"a": Int(0xF0F0F0F0), "b": Int(0x12345678), "data": Bytes{B: []byte{5, 6, 7, 8}}}},
		{"many locals", []string{`rule f(n: int) -> int {
    a1 = n + 1
    a2 = a1 + 1
    a3 = a2 + 1
    a4 = a3 + 1
    a5 = a4 + 1
    a6 = a5 + 1
    a7 = a6 + 1
    a8 = a7 + 1
    a9 = a8 + 1
    a10 = a9 + 1
    a11 = a10 + 1
    a12 = a11 + 1
    a13 = a12 + 1
    a14 = a13 + 1
    a15 = a14 + 1
    a16 = a15 + 1
    a17 = a16 + 1
    a18 = a17 + 1
    t: int = 0
    i: int = 0
    while i < 3 {
        t = t + a1 + a18 + i
        i = i + 1
    }
    return t
}`}, "f", map[string]Value{"n": Int(5)}},
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
				// Twice, so the second run reads the slots the first set.
				first, firstFault := in.Call(context.Background(), c.call, c.args)
				v, f := in.Call(context.Background(), c.call, c.args)
				if (firstFault == nil) != (f == nil) || (f == nil && !Equal(first, v)) {
					t.Fatalf("a second run gave %v / %v, the first %v / %v", v, f, first, firstFault)
				}
				return v, f
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
