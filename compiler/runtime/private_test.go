package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0054: private helpers and models run in their file's scope, and
// recursion is bounded by a call depth limit that faults instead of
// crashing.

func TestPrivateHelperAndModelRunInTheirFilesScope(t *testing.T) {
	u := universeFromSource(t, `rule fooBar(number: int) -> int {
    floor = Threshold(minimum: 0);
    if (interiorFunctionCall(number, floor)) {
        return 3;
    }
    return 4;
}

private {
    interiorFunctionCall(number: int, floor: Threshold) -> bool {
        return number > floor.minimum;
    }

    model Threshold {
        minimum: int
    }
}
`)
	in := newTestInterp(u, nil)
	for _, tc := range []struct {
		n    int64
		want Value
	}{{5, Int(3)}, {-5, Int(4)}} {
		v, fault := in.Call(context.Background(), "fooBar", map[string]Value{"number": Int(tc.n)})
		if fault != nil {
			t.Fatalf("Call: %v", fault)
		}
		if v != tc.want {
			t.Fatalf("fooBar(%d) = %v, want %v", tc.n, v, tc.want)
		}
	}
}

// TestSamePrivateNameInTwoFilesRunsEachFilesOwn: two files may each have
// a private `helper`, and each entry must reach its own.
func TestSamePrivateNameInTwoFilesRunsEachFilesOwn(t *testing.T) {
	u := universeFromSource(t,
		"rule a(n: int) -> int {\n    return helper(n);\n}\n\nprivate {\n    helper(n: int) -> int {\n        return n + 1;\n    }\n}\n",
		"rule b(n: int) -> int {\n    return helper(n);\n}\n\nprivate {\n    helper(n: int) -> int {\n        return n * 10;\n    }\n}\n",
	)
	in := newTestInterp(u, nil)
	if v, fault := in.Call(context.Background(), "a", map[string]Value{"n": Int(2)}); fault != nil || v != Int(3) {
		t.Fatalf("a(2) = %v, %v; want 3", v, fault)
	}
	if v, fault := in.Call(context.Background(), "b", map[string]Value{"n": Int(2)}); fault != nil || v != Int(20) {
		t.Fatalf("b(2) = %v, %v; want 20", v, fault)
	}
}

// sumTo is an activity: a rule may not call itself (ADR 0054 §6), an
// activity may.
const sumToSource = `activity sumTo(n: int) -> int {
    if (n <= 0) {
        return 0;
    }
    return n + sumTo(n - 1);
}
`

func TestRecursionWithinTheDepthLimitRuns(t *testing.T) {
	in := newTestInterp(universeFromSource(t, sumToSource), nil)
	// sumTo(999) is 1,000 frames deep: exactly the default limit.
	v, fault := in.Call(context.Background(), "sumTo", map[string]Value{"n": Int(999)})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	if v != Int(999*1000/2) {
		t.Fatalf("sumTo(999) = %v", v)
	}
}

// TestRecursionPastTheDepthLimitIsAnOrdinaryFault is §6: a Fault naming
// the limit and carrying the call chain, never a crash.
func TestRecursionPastTheDepthLimitIsAnOrdinaryFault(t *testing.T) {
	in := newTestInterp(universeFromSource(t, sumToSource), nil)
	in.MaxCallDepth = 10
	_, fault := in.Call(context.Background(), "sumTo", map[string]Value{"n": Int(50)})
	if fault == nil {
		t.Fatal("got nil fault, want a call depth fault")
	}
	if !strings.Contains(fault.Message, "call depth limit of 10") {
		t.Fatalf("fault = %q, want it to name the limit", fault.Message)
	}
	if len(fault.Stack) != 11 {
		t.Fatalf("got a %d-frame chain, want 11 (10 calls that ran, plus the one that overflowed): %v", len(fault.Stack), fault.Stack)
	}

	// The default is 1,000 frames.
	in.MaxCallDepth = 0
	if _, fault := in.Call(context.Background(), "sumTo", map[string]Value{"n": Int(1000)}); fault == nil || !strings.Contains(fault.Message, "limit of 1000") {
		t.Fatalf("sumTo(1000) is 1,001 frames; want the default limit's fault, got %v", fault)
	}
}

// G49: json.decode into a private model whose fields are other private
// models of the same file resolves those fields in the file's scope.
func TestJSONDecodesNestedPrivateModels(t *testing.T) {
	in := checkedInterp(t, `conversion titleOf(body: bytes) -> Result<string, JsonError> {
    item: Item = json.decode(body)?;
    return Ok(item.media.title);
}

private {
    model Item {
        media: Media
        tags: List<Tag>
    }

    model Media {
        title: string
    }

    model Tag {
        name: string
    }
}
`)
	body := Bytes{B: []byte(`{"media": {"title": "Dune"}, "tags": [{"name": "scifi"}]}`)}
	v, fault := in.Call(context.Background(), "titleOf", map[string]Value{"body": body})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	if want := (EnumValue{TypeName: "Result", Variant: "Ok", Fields: []Value{Str("Dune")}}); !strings.Contains(fmt.Sprint(v), "Dune") {
		t.Fatalf("titleOf = %v, want an Ok holding %v", v, want)
	}
}

// G49, second half: the private models belong to a dependency, not to the
// package the run starts in.
func TestJSONDecodesNestedPrivateModelsOfADependency(t *testing.T) {
	libFile := mustParseFile(t, `conversion titleOf(body: bytes) -> Result<string, JsonError> {
    item: Item = json.decode(body)?;
    return Ok(item.media.title);
}

private {
    model Item {
        media: Media
    }

    model Media {
        title: string
    }
}
`)
	if diags := checker.Check([]*syntax.File{libFile}, nil); len(diags) > 0 {
		t.Fatalf("checker: %+v", diags)
	}
	lib := buildTestPackage(t, []*syntax.File{libFile}, nil)
	rootFile := mustParseFile(t, `use lib.books as books

rule read(body: bytes) -> Result<string, JsonError> {
    return books.titleOf(body);
}
`)
	packages := types.Packages{"lib.books": lib}
	universe, conflicts := types.BuildUniverse([]*syntax.File{rootFile})
	if len(conflicts) > 0 {
		t.Fatalf("unexpected conflicts: %+v", conflicts)
	}
	in := New(universe, NewCapabilityRegistry())
	in.Imports = map[string]map[string]*types.Package{"read": checker.ResolveFileImports(rootFile, packages)}

	body := Bytes{B: []byte(`{"media": {"title": "Dune"}}`)}
	v, fault := in.Call(context.Background(), "read", map[string]Value{"body": body})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	if !strings.Contains(fmt.Sprint(v), "Dune") {
		t.Fatalf("read = %v, want an Ok holding Dune", v)
	}
}
