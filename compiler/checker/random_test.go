package checker

import (
	"strings"
	"testing"
)

// ADR 0068 §4: Clock and Random are local effects. An activity or a gateway
// reads them directly; rules, conversions, formulas, and workflows take
// the value as an argument.

func TestLocalEffectsInActivitiesAndGateways(t *testing.T) {
	diags := checkSrc(t, `activity newSessionToken() -> string {
    return "${Random.uuid()} ${Random.int(min: 1, max: 6)} ${Random.float()} ${Clock.now()}"
}`, `gateway stamp(host: string) -> Result<Connection, SocketError>
    requires Socket
{
    id: string = Random.uuid()
    token: bytes = Random.bytes(count: 32)
    at = Clock.now()
    return Socket.connect(host, Random.int(1, 6))
}`)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
}

func TestLocalEffectsOutsideTheShellAreB10(t *testing.T) {
	cases := []struct{ src, fragment string }{
		{"rule r() -> int {\n    return Random.int(min: 1, max: 6)\n}", "a rule may not call Random.int; its result would change from call to call, so take the value as a parameter"},
		{"conversion c() -> int {\n    return Clock.now()\n}", "a conversion may not call Clock.now"},
		{"formulas f {\n    formula g() -> float {\n        return Random.float()\n    }\n}", "a formula may not call Random.float"},
		{"workflow w() -> Result<string, string> {\n    return Ok(Random.uuid())\n}", "a workflow may not call Random.uuid; call an activity that does"},
	}
	for _, c := range cases {
		wantDiag(t, checkSrc(t, c.src), "B10", c.fragment)
	}
}

func TestRequiringALocalEffectIsP8(t *testing.T) {
	for _, name := range []string{"Clock", "Random"} {
		src := "gateway g() -> string\n    requires " + name + "\n{\n    return \"x\"\n}"
		wantDiag(t, checkSrc(t, src), "P8", "requires "+name+", which is a local effect")
	}
}

func TestRandomOperationsAreTyped(t *testing.T) {
	wantDiag(t, checkSrc(t, "activity a(xs: List<int>) -> int {\n    return Random.choose(xs)\n}"),
		"K1", `Random has no operation "choose"; its operations are bytes(), float(), int(), uuid()`)
	wantDiag(t, checkSrc(t, "activity a() -> int {\n    return Random.int(\"a\", 2)\n}"), "A1", "")
	wantDiag(t, checkSrc(t, "activity a() -> string {\n    return Random.int(1, 6)\n}"), "A1", "")
	wantDiag(t, checkSrc(t, "activity a() -> int {\n    return Random.int(low: 1, max: 6)\n}"),
		"", `Random.int has no parameter "low"; its parameters are min and max`)
	diags := checkSrc(t, "activity a() -> bytes {\n    return Random.bytes()\n}")
	if len(diags) != 1 || !strings.Contains(diags[0].Msg, "Random.bytes takes 1 argument(s), got 0") {
		t.Fatalf("diags = %v", diags)
	}
}
