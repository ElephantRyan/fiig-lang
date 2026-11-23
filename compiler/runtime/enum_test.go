package runtime

import (
	"context"
	"testing"
)

// ADR 0046: header constants and key lookups at runtime.

func TestHeadedEnumLookupAndFieldAccessRun(t *testing.T) {
	u := universeFromSource(t, `enum DeductionSource(key code: string, key aliases: List<string>, layout: FieldLayout) {
    Acme(code: "ACME", aliases: ["ACMEINC", "ACME-INC"], layout: FieldLayout.FixedWidth),
    Globex(code: "GLBX", aliases: [], layout: FieldLayout.Csv)
}
`, `enum FieldLayout {
    FixedWidth,
    Csv
}
`, `rule resolve(raw: string) -> DeductionSource | None {
    return DeductionSource(code: raw);
}
`, `rule byAlias(raw: string) -> DeductionSource | None {
    return DeductionSource(aliases: raw);
}
`, `rule layoutFor(raw: string) -> string {
    return match (DeductionSource(code: raw)) {
        None -> "unknown",
        found -> describe(found)
    }
}
`, `rule describe(source: DeductionSource) -> string {
    return match (source.layout) {
        FieldLayout.FixedWidth -> "fixed width ${source.code}",
        FieldLayout.Csv -> "csv ${source.code}"
    }
}
`)
	in := newTestInterp(u, nil)
	ctx := context.Background()
	call := func(name, raw string) Value {
		t.Helper()
		v, fault := in.Call(ctx, name, map[string]Value{"raw": Str(raw)})
		if fault != nil {
			t.Fatalf("%s(%q): %v", name, raw, fault)
		}
		return v
	}
	if v := call("resolve", "GLBX"); v.(EnumValue).Variant != "Globex" {
		t.Fatalf("resolve(GLBX) = %s", v)
	}
	if v := call("resolve", "NOPE"); v != (None{}) {
		t.Fatalf("resolve(NOPE) = %s, want None", v)
	}
	if v := call("byAlias", "ACME-INC"); v.(EnumValue).Variant != "Acme" {
		t.Fatalf("byAlias(ACME-INC) = %s", v)
	}
	if v := call("byAlias", "ACME"); v != (None{}) {
		t.Fatalf("byAlias(ACME) = %s; the code is not an alias", v)
	}
	if v := call("layoutFor", "ACME"); v != Str("fixed width ACME") {
		t.Fatalf("layoutFor(ACME) = %s", v)
	}
	if v := call("layoutFor", "X"); v != Str("unknown") {
		t.Fatalf("layoutFor(X) = %s", v)
	}
}

// TestCorpusDeductions runs examples/deductions: sentinels in the rule,
// codes and aliases found through the enum, header fields read directly.
func TestCorpusDeductions(t *testing.T) {
	u := loadUniverse(t, "deductions/models", "deductions/rules")
	in := newTestInterp(u, nil)
	ctx := context.Background()
	var tenant Record
	resolve := func(raw string) Result {
		t.Helper()
		v, fault := in.Call(ctx, "resolveDeductionSource", map[string]Value{"raw": Str(raw), "tenant": tenant})
		if fault != nil {
			t.Fatalf("resolveDeductionSource(%q): %v", raw, fault)
		}
		return v.(Result)
	}
	variant := func(v Value) string { return v.(EnumValue).Variant }
	// The tenant's default comes from the interpreter, so it carries its
	// header constants, as every enum value built in fiig does.
	tenant = Record{TypeName: "Tenant", Fields: map[string]Value{"id": Str("t1"), "defaultSource": None{}}}
	tenant.Fields["defaultSource"] = resolve("GLBX").Value

	if r := resolve("INIT"); !r.Ok || variant(r.Value) != "Initech" {
		t.Errorf("INIT -> %v", r)
	}
	if r := resolve("ACME-INC"); !r.Ok || variant(r.Value) != "Acme" {
		t.Errorf("alias ACME-INC -> %v", r)
	}
	if r := resolve("DEFAULT"); !r.Ok || variant(r.Value) != "Globex" {
		t.Errorf("DEFAULT -> %v", r)
	}
	if r := resolve("OTHER"); r.Ok || variant(r.Value) != "UnsupportedSource" {
		t.Errorf("OTHER -> %v", r)
	}
	if r := resolve("NOPE"); r.Ok || variant(r.Value) != "UnknownSource" {
		t.Errorf("NOPE -> %v", r)
	}

	for _, c := range []struct{ raw, want string }{{"ACME", "ACME-7.dat"}, {"DEFAULT", "GLBX-7.csv"}} {
		got, fault := in.Call(ctx, "deductionFileName", map[string]Value{"source": resolve(c.raw).Value, "batch": Int(7)})
		if fault != nil || got != Str(c.want) {
			t.Errorf("deductionFileName(%s) = %v, %v; want %s", c.raw, got, fault, c.want)
		}
	}
}
