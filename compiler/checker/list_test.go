package checker

import (
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0057: list literals and append.

func TestListLiteralsAndAppendTypeCheck(t *testing.T) {
	f := mustParse(t, `rule codes(extra: string) -> List<string> {
    base = ["ACME", "GLBX",];
    ids: List<string> = [];
    for (code in base) {
        ids = append(ids, code);
    }
    ids = append(ids, extra);
    return ids;
}
`)
	if diags := Check([]*syntax.File{f}, nil); len(diags) != 0 {
		t.Fatalf("want a clean build, got %v", diags)
	}
}

// TestEmptyListTakesItsTypeFromContext is §2: `[]` is legal wherever an
// expected type exists, and A12 where none does.
func TestEmptyListTakesItsTypeFromContext(t *testing.T) {
	ok := mustParse(t, `rule none() -> List<int> {
    return [];
}
`)
	viaAppend := mustParse(t, `rule one(n: int) -> List<int> {
    xs = append([], n);
    return xs;
}
`)
	if diags := Check([]*syntax.File{ok, viaAppend}, nil); len(diags) != 0 {
		t.Fatalf("a return type and an append operand both type []; got %v", diags)
	}

	bare := mustParse(t, `rule bad() -> int {
    names = [];
    return 0;
}
`)
	if got := rules(Check([]*syntax.File{bare}, nil)); got["A12"] != 1 || len(got) != 1 {
		t.Fatalf("want A12 for [] with no expected type, got %v", got)
	}
}

// TestListElementsMustShareOneType is §2: no implicit union, no widening;
// a declared type is how a list of a union is written.
func TestListElementsMustShareOneType(t *testing.T) {
	mixed := mustParse(t, `rule mixed() -> int {
    xs = [1, "a"];
    return 0;
}
`)
	if got := rules(Check([]*syntax.File{mixed}, nil)); got["A1"] != 1 {
		t.Fatalf("want A1 for mixed element types, got %v", got)
	}

	declared := mustParse(t, `rule declared(n: int) -> int {
    xs: List<int | None> = [n, None];
    return 0;
}
`)
	if diags := Check([]*syntax.File{declared}, nil); len(diags) != 0 {
		t.Fatalf("a declared union element type must accept both, got %v", diags)
	}

	wrongDeclared := mustParse(t, `rule wrong() -> List<string> {
    return [1];
}
`)
	if got := rules(Check([]*syntax.File{wrongDeclared}, nil)); got["A1"] != 1 {
		t.Fatalf("want A1 for an int element in a List<string>, got %v", got)
	}
}

func TestAppendChecksItsArguments(t *testing.T) {
	f := mustParse(t, `rule bad(n: int) -> int {
    names: List<string> = [];
    names = append(names, n);
    m = append(n, n);
    return 0;
}
`)
	if got := rules(Check([]*syntax.File{f}, nil)); got["A1"] != 2 {
		t.Fatalf("want A1 for an int appended to List<string> and for a non-list first argument, got %v", got)
	}
}

// TestDiscardedAppendIsA13 is §3: append returns a new list, so a bare
// append statement does nothing.
func TestDiscardedAppendIsA13(t *testing.T) {
	f := mustParse(t, `rule dropped(n: int) -> List<int> {
    xs: List<int> = [];
    append(xs, n);
    return xs;
}
`)
	if got := rules(Check([]*syntax.File{f}, nil)); got["A13"] != 1 || len(got) != 1 {
		t.Fatalf("want A13 for a discarded append, got %v", got)
	}
}
