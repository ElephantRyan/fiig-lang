package checker

import (
	"strings"
	"testing"

	"fiig-lang/compiler/syntax"
)

func parseAll(t *testing.T, srcs ...string) []*syntax.File {
	t.Helper()
	files := make([]*syntax.File, len(srcs))
	for i, s := range srcs {
		files[i] = mustParse(t, s)
	}
	return files
}

const embeddingModels = `model Address {
    line1: string;
    city: string;
}`

// TestEmbeddingFlattensThroughSeveralLevels is ADR 0045: promoted fields
// are read as the outer model's own, at any depth, and construction names
// the embedded type.
func TestEmbeddingFlattensThroughSeveralLevels(t *testing.T) {
	files := parseAll(t,
		embeddingModels,
		"model ContactInfo {\n    Address;\n    email: string;\n}",
		"model Customer {\n    ContactInfo;\n    id: string;\n}",
		"rule cityOf(c: Customer) -> string {\n    return c.city;\n}",
		"rule emailOf(c: Customer) -> string {\n    return c.email;\n}",
		"rule build(a: Address, id: string) -> Customer {\n    return Customer(ContactInfo: ContactInfo(Address: a, email: \"x\"), id: id);\n}",
		"rule renamed(c: Customer) -> Customer {\n    return copy(c, city: \"Oslo\");\n}",
	)
	if diags := Check(files, nil); len(diags) != 0 {
		t.Fatalf("want no diagnostics, got %v", diags)
	}
}

func TestEmbeddingRejectsUnknownPromotedFieldAndMissingEmbed(t *testing.T) {
	files := parseAll(t,
		embeddingModels,
		"model Customer {\n    Address;\n    id: string;\n}",
		"rule bad(c: Customer) -> string {\n    return c.zip;\n}",
		"rule short(id: string) -> Customer {\n    return Customer(id: id);\n}",
	)
	diags := Check(files, nil)
	if len(diags) != 2 {
		t.Fatalf("want 2 diagnostics (unknown field, missing Address), got %v", diags)
	}
}

// TestEmbeddingDoesNotImplySubtyping: a Customer is not an Address. This is
// the regression ADR 0045 asks for — the tempting bug is to let embedding
// imply assignability.
func TestEmbeddingDoesNotImplySubtyping(t *testing.T) {
	files := parseAll(t,
		embeddingModels,
		"model Customer {\n    Address;\n    id: string;\n}",
		"rule takesAddress(a: Address) -> string {\n    return a.city;\n}",
		"activity bad(c: Customer) -> string {\n    return takesAddress(c);\n}",
	)
	diags := Check(files, nil)
	if len(diags) != 1 || !strings.Contains(diags[0].Msg, "Address") {
		t.Fatalf("want one type mismatch mentioning Address, got %v", diags)
	}
}

// TestEmbeddingShadowingAndSameDepthCollision: an explicit field shadows a
// promoted one, but two embeds promoting the same name at the same depth is
// A10 at the declaration, whether or not the name is ever read.
func TestEmbeddingShadowingAndSameDepthCollision(t *testing.T) {
	shadow := parseAll(t,
		embeddingModels,
		"model Customer {\n    Address;\n    city: int;\n}",
		"rule cityOf(c: Customer) -> int {\n    return c.city;\n}",
	)
	if diags := Check(shadow, nil); len(diags) != 0 {
		t.Fatalf("explicit field must shadow the promoted one, got %v", diags)
	}

	collide := parseAll(t,
		embeddingModels,
		"model Billing {\n    city: string;\n}",
		"model Customer {\n    Address;\n    Billing;\n}",
	)
	got := rules(Check(collide, nil))
	if got["A10"] != 1 || len(got) != 1 {
		t.Fatalf("want exactly one A10, got %v", got)
	}

	// Shallower wins: a field at depth 1 beats one at depth 2, no error.
	deep := parseAll(t,
		embeddingModels,
		"model Deep {\n    city: string;\n}",
		"model Wrapper {\n    Deep;\n}",
		"model Customer {\n    Address;\n    Wrapper;\n}",
	)
	if got := rules(Check(deep, nil)); got["A10"] != 0 {
		t.Fatalf("a shallower promoted field must win without error, got %v", got)
	}
}

func TestEmbeddingCycleIsA11AtEveryMember(t *testing.T) {
	files := parseAll(t,
		"model A {\n    B;\n}",
		"model B {\n    A;\n}",
		"model Self {\n    Self;\n}",
	)
	got := rules(Check(files, nil))
	if got["A11"] != 3 {
		t.Fatalf("want 3 A11 (A, B, Self), got %v", got)
	}
}

func TestEmbeddingMustNameAModel(t *testing.T) {
	files := parseAll(t,
		"enum CustomerKind {\n    Retail,\n}",
		"model Customer {\n    CustomerKind;\n    Missing;\n}",
	)
	diags := Check(files, nil)
	var msgs []string
	for _, d := range diags {
		msgs = append(msgs, d.Msg)
	}
	joined := strings.Join(msgs, "\n")
	if !strings.Contains(joined, `"CustomerKind" is a enum, not a model`) {
		t.Fatalf("want a not-a-model error for CustomerKind, got:\n%s", joined)
	}
}
