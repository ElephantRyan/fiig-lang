package checker

import "testing"

// ADR 0068 §5: every builtin takes named arguments.

func TestBuiltinNamedArgsCheck(t *testing.T) {
	diags := checkSrc(t, `rule r(s: string, xs: List<int>, m: Map<string, int>) -> string {
    a: string = s.replaceAll(old: "-", new: "/")
    b: string = s.slice(to: 3, from: 1)
    c: bool = s.startsWith(prefix: "Bearer ") and xs.contains(element: 2)
    d: List<int> = append(element: 3, list: xs)
    e: Map<string, int> = m.put(key: "a", value: 1)
    f: string = s.replaceAll("-", new: "/")
    return xs.slice(0, to: 1).length().toString()
}`)
	// toString isn't a method; the one K1 shows everything else bound.
	wantDiag(t, diags, "K1", `int has no methods, so it has no "toString"`)
}

func TestBuiltinNamedArgMisuse(t *testing.T) {
	cases := []struct{ src, fragment string }{
		{`rule r(s: string) -> string {
    return s.replaceAll(from: "-", new: "/")
}`, `string.replaceAll has no parameter "from"; its parameters are old and new`},
		{`rule r(s: string) -> string {
    return s.replaceAll(old: "-", old: "/")
}`, `string.replaceAll's "old" is given twice`},
		{`rule r(s: string) -> string {
    return s.replaceAll("-", old: "/")
}`, `string.replaceAll's "old" is given twice`},
		{`rule r(s: string) -> string {
    return s.replaceAll(old: "-", "/")
}`, "a positional argument to string.replaceAll follows a named one"},
		{`rule r(s: string) -> string {
    return s.replaceAll(new: "/")
}`, `string.replaceAll is missing argument "old"`},
		{`rule r(s: string) -> string {
    return s.slice(1, 2, to: 3)
}`, "string.slice's \"to\" is given twice"},
		{`rule r(s: string) -> string {
    return s.slice(1, 2, 3)
}`, "string.slice takes 2 argument(s), got 3"},
		{`rule r(xs: List<int>) -> List<int> {
    return append(xs, item: 1)
}`, `append has no parameter "item"; its parameters are list and element`},
		{`gateway g(host: string) -> Result<Connection, SocketError>
    requires Socket
{
    return Socket.connect(hostname: host, port: 1)
}`, `Socket.connect has no parameter "hostname"; its parameters are host and port`},
	}
	for _, c := range cases {
		wantDiag(t, checkSrc(t, c.src), "", c.fragment)
	}
	// Arguments are typed against the parameter their name binds to.
	wantDiag(t, checkSrc(t, `rule r(s: string) -> string {
    return s.slice(to: "3", from: 1)
}`), "A1", "")
}

// A declaration call follows the same two rules.
func TestDeclarationNamedArgOrder(t *testing.T) {
	decl := "rule add(a: int, b: int) -> int {\n    return a + b\n}"
	wantDiag(t, checkSrc(t, decl, "activity r() -> int {\n    return add(a: 1, a: 2)\n}"), "", `add's "a" is given twice`)
	wantDiag(t, checkSrc(t, decl, "activity r() -> int {\n    return add(a: 1, 2)\n}"), "", "a positional argument to add follows a named one")
}
