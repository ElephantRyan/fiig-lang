package checker

import (
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0046: headed enums, header constants, and key lookups.

const deductionSource = `enum DeductionSource(key code: string, key aliases: List<string>, layout: FieldLayout) {
    Acme(code: "ACME", aliases: ["ACMEINC"], layout: FieldLayout.FixedWidth),
    Globex(code: "GLBX", aliases: [], layout: FieldLayout.Csv),
}
`

const fieldLayout = `enum FieldLayout {
    FixedWidth,
    Csv,
}
`

func checkWithSource(t *testing.T, srcs ...string) map[string]int {
	t.Helper()
	files := []*syntax.File{mustParse(t, deductionSource), mustParse(t, fieldLayout)}
	for _, s := range srcs {
		files = append(files, mustParse(t, s))
	}
	return rules(Check(files, nil))
}

// TestHeadedEnumLookupAndFieldAccess is §4b and §5: a lookup by a key field
// is DeductionSource | None, and header fields read off any value.
func TestHeadedEnumLookupAndFieldAccess(t *testing.T) {
	got := checkWithSource(t, `rule resolve(raw: string) -> DeductionSource | None {
    return DeductionSource(code: raw);
}
`, `rule byAlias(raw: string) -> DeductionSource | None {
    return DeductionSource(aliases: raw);
}
`, `rule codeOf(source: DeductionSource) -> string {
    return source.code;
}
`, `rule layoutOf() -> FieldLayout {
    source = DeductionSource.Acme;
    return source.layout;
}
`)
	if len(got) != 0 {
		t.Fatalf("want a clean build, got %v", got)
	}
}

// TestLookupResultMayBeNone is §5: the lookup can miss, so it is not a
// DeductionSource.
func TestLookupResultMayBeNone(t *testing.T) {
	got := checkWithSource(t, `rule resolve(raw: string) -> DeductionSource {
    return DeductionSource(code: raw);
}
`)
	if len(got) == 0 {
		t.Fatalf("a lookup returned as DeductionSource should not type-check")
	}
}

// TestHeaderConstantErrorsAreA15 is §4, §4a and §6, one source per rule.
func TestHeaderConstantErrorsAreA15(t *testing.T) {
	cases := map[string]string{
		"missing field": `enum E(key code: string, label: string) {
    A(code: "A"),
}
`,
		"unknown field": `enum E(key code: string) {
    A(code: "A", colour: "red"),
}
`,
		"positional": `enum E(key code: string) {
    A("A"),
}
`,
		"supplied twice": `enum E(key code: string) {
    A(code: "A", code: "B"),
}
`,
		"call is not a constant": `enum E(key code: string) {
    A(code: makeCode()),
}
`,
		"arithmetic is not a constant": `enum E(seconds: int) {
    A(seconds: 5 * 60),
}
`,
		"interpolation is not a constant": `enum E(label: string) {
    A(label: "x${y}"),
}
`,
		"payload on a headed enum": `enum E(key code: string) {
    A(id: string),
}
`,
		"key must be string or int": `enum E(key active: bool) {
    A(active: true),
}
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			got := rules(Check([]*syntax.File{mustParse(t, src)}, nil))
			if got["A15"] == 0 {
				t.Fatalf("want A15, got %v", got)
			}
		})
	}
}

// TestHeaderConstantsAreTypeChecked is §4: a constant still has to be of
// its field's type.
func TestHeaderConstantsAreTypeChecked(t *testing.T) {
	f := mustParse(t, `enum E(key code: string) {
    A(code: 1),
}
`)
	if diags := Check([]*syntax.File{f}, nil); len(diags) == 0 {
		t.Fatalf("an int for a string header field should not type-check")
	}
}

// TestConstantFormsAreAccepted is §4a's list: literals, None, negative
// literals, enum variants, and lists and models of constants.
func TestConstantFormsAreAccepted(t *testing.T) {
	model := mustParse(t, `model Limits {
    min: int;
    max: int;
}
`)
	f := mustParse(t, `enum E(key code: string, flag: bool, floor: int, note: string | None, limits: Limits, tags: List<string>) {
    A(code: "A", flag: true, floor: -1, note: None, limits: Limits(min: 1, max: 5), tags: ["x", "y"]),
}
`)
	if diags := Check([]*syntax.File{model, f}, nil); len(diags) != 0 {
		t.Fatalf("want a clean build, got %v", diags)
	}
}

// TestDuplicateKeyValuesAreA16 is §5 and §7: a key value, or any element of
// a list key, may name only one variant. Non-key fields may repeat.
func TestDuplicateKeyValuesAreA16(t *testing.T) {
	dupCode := mustParse(t, `enum E(key code: string, layout: string) {
    A(code: "X", layout: "csv"),
    B(code: "X", layout: "csv"),
}
`)
	if got := rules(Check([]*syntax.File{dupCode}, nil)); got["A16"] != 1 || len(got) != 1 {
		t.Fatalf("want exactly one A16 for the shared code (not for the shared layout), got %v", got)
	}
	dupAlias := mustParse(t, `enum E(key aliases: List<string>) {
    A(aliases: ["X", "Y"]),
    B(aliases: ["Z", "Y"]),
}
`)
	if got := rules(Check([]*syntax.File{dupAlias}, nil)); got["A16"] != 1 || len(got) != 1 {
		t.Fatalf("want exactly one A16 for the shared alias, got %v", got)
	}
}

// TestKeyLookupMisuseIsA17 is §5: one named argument, naming a key field,
// on an enum that has a header.
func TestKeyLookupMisuseIsA17(t *testing.T) {
	cases := map[string]string{
		"not a key": `rule r(x: FieldLayout) -> DeductionSource | None {
    return DeductionSource(layout: x);
}
`,
		"positional": `rule r(raw: string) -> DeductionSource | None {
    return DeductionSource(raw);
}
`,
		"two keys": `rule r(raw: string) -> DeductionSource | None {
    return DeductionSource(code: raw, aliases: raw);
}
`,
		"no header": `rule r(raw: string) -> FieldLayout | None {
    return FieldLayout(code: raw);
}
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if got := checkWithSource(t, src); got["A17"] == 0 {
				t.Fatalf("want A17, got %v", got)
			}
		})
	}
}
