package checker

import (
	"strings"
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0054: a kind's file is a unit with one public entry and a private
// block of helpers and models only that file can see.

const fooBarFile = `rule fooBar(number: int) -> int {
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
`

func TestRuleMayCallItsOwnHelpersAndUseItsPrivateModels(t *testing.T) {
	if diags := Check([]*syntax.File{mustParse(t, fooBarFile)}, nil); len(diags) != 0 {
		t.Fatalf("a rule using its own private block must be clean, got %v", diags)
	}
}

// TestRuleRecursesOnlyThroughItsHelpers is §4 and §6: a rule's helpers
// may call each other and themselves, but nothing in the file may call
// the rule itself. A call to the public entry is judged by the matrix
// like a call to any other rule.
func TestRuleRecursesOnlyThroughItsHelpers(t *testing.T) {
	ok := mustParse(t, `rule countdown(n: int) -> int {
    return step(n);
}

private {
    step(n: int) -> int {
        if (n <= 0) {
            return 0;
        }
        return step(n - 1);
    }
}
`)
	if diags := Check([]*syntax.File{ok}, nil); len(diags) != 0 {
		t.Fatalf("a helper recursing on itself must be legal, got %v", diags)
	}

	direct := mustParse(t, `rule fact(n: int) -> int {
    if (n <= 1) {
        return 1;
    }
    return n * fact(n - 1);
}
`)
	viaHelper := mustParse(t, `rule bounce(n: int) -> int {
    return back(n);
}

private {
    back(n: int) -> int {
        return bounce(n);
    }
}
`)
	if got := rules(Check([]*syntax.File{direct, viaHelper}, nil)); got["B7"] != 2 || len(got) != 1 {
		t.Fatalf("want two B7 (a rule calling itself, directly and through a helper), got %v", got)
	}
}

// TestSelfCallFollowsTheMatrix is §6: a public entry calling itself is
// judged as a call to another declaration of its kind. Activities and
// conversions may call their own kind; gateways (B3) and workflows (B8)
// may not.
func TestSelfCallFollowsTheMatrix(t *testing.T) {
	act := mustParse(t, `activity walk(n: int) -> int {
    if (n <= 0) {
        return 0;
    }
    return walk(n - 1);
}
`)
	conv := mustParse(t, `conversion unwind(n: int) -> int {
    if (n <= 0) {
        return 0;
    }
    return unwind(n - 1);
}
`)
	if diags := Check([]*syntax.File{act, conv}, nil); len(diags) != 0 {
		t.Fatalf("an activity or conversion calling itself must be legal, got %v", diags)
	}

	gw := mustParse(t, `gateway poll(n: int) -> int
    requires Database
{
    return poll(n);
}
`)
	wf := mustParse(t, `workflow retryAll(n: int) -> int {
    return retryAll(n);
}
`)
	if got := rules(Check([]*syntax.File{gw, wf}, nil)); got["B3"] != 1 || got["B8"] != 1 || len(got) != 2 {
		t.Fatalf("want B3 (gateway calls itself) and B8 (workflow calls itself), got %v", got)
	}
}

// The refusal of a self-call says where the recursion goes (docs/improvement-
// ideas.md B2), for each kind that can't call itself.
func TestSelfCallMessageSaysWhereRecursionGoes(t *testing.T) {
	cases := []struct{ src, rule string }{
		{"rule fact(n: int) -> int {\n    return fact(n - 1);\n}", "B7"},
		{"gateway poll(n: int) -> int\n    requires Database\n{\n    return poll(n);\n}", "B3"},
		{"workflow again(n: int) -> int {\n    return again(n);\n}", "B8"},
	}
	for _, c := range cases {
		wantDiag(t, checkSrc(t, c.src), c.rule, "recursion belongs in a private helper that calls only itself")
	}
}

// TestHelperInheritsItsFilesCompositionRules is §4/§5: a rule's helper is
// held to the rule's matrix for calls that leave the file.
func TestHelperInheritsItsFilesCompositionRules(t *testing.T) {
	other := mustParse(t, "rule doubled(n: int) -> int {\n    return n * 2;\n}\n")
	gw := mustParse(t, "gateway fetch(n: int) -> int\n    requires Database\n{\n    return n;\n}\n")
	f := mustParse(t, `rule entry(n: int) -> int {
    return helper(n);
}

private {
    helper(n: int) -> int {
        a = doubled(n);
        return fetch(a);
    }
}
`)
	got := rules(Check([]*syntax.File{other, gw, f}, nil))
	if got["B7"] != 1 || got["B1"] != 1 {
		t.Fatalf("want one B7 (helper calls another rule) and one B1 (helper calls a gateway), got %v", got)
	}

	// An activity's helper may do what an activity may.
	act := mustParse(t, `activity coordinate(n: int) -> int {
    return helper(n);
}

private {
    helper(n: int) -> int {
        a = doubled(n);
        return fetch(a);
    }
}
`)
	if diags := Check([]*syntax.File{other, gw, act}, nil); len(diags) != 0 {
		t.Fatalf("an activity's helper may call a rule and a gateway, got %v", diags)
	}
}

// TestPrivateMemberReferencedFromAnotherFileIsH4 is §3's third escape
// rule. Scoping alone would make it "undefined"; H4 says why.
func TestPrivateMemberReferencedFromAnotherFileIsH4(t *testing.T) {
	outsider := mustParse(t, `rule outsider(n: int) -> bool {
    t = Threshold(minimum: 1);
    return interiorFunctionCall(n, t);
}
`)
	diags := Check([]*syntax.File{mustParse(t, fooBarFile), outsider}, nil)
	if got := rules(diags); got["H4"] != 2 {
		t.Fatalf("want two H4 (the helper and the model), got %v", diags)
	}
	for _, d := range diags {
		if d.Rule == "H4" && !strings.Contains(d.Msg, `rule "fooBar"`) {
			t.Errorf("H4 should name the owning file's entry: %s", d)
		}
	}
}

// TestPrivateTypeInPublicSignatureIsH4 is §3's first escape rule, at any
// depth of the signature's type.
func TestPrivateTypeInPublicSignatureIsH4(t *testing.T) {
	f := mustParse(t, `rule leaky(t: Threshold) -> List<Threshold> | None {
    return None;
}

private {
    model Threshold {
        minimum: int
    }
}
`)
	if got := rules(Check([]*syntax.File{f}, nil)); got["H4"] != 2 {
		t.Fatalf("want H4 on the parameter and on the return type, got %v", got)
	}

	// A private type may still flow between private members.
	ok := mustParse(t, `rule fine(n: int) -> int {
    return unwrap(build(n));
}

private {
    build(n: int) -> Threshold {
        return Threshold(minimum: n);
    }

    unwrap(t: Threshold) -> int {
        return t.minimum;
    }

    model Threshold {
        minimum: int
    }
}
`)
	if diags := Check([]*syntax.File{ok}, nil); len(diags) != 0 {
		t.Fatalf("private types between private members must be legal, got %v", diags)
	}
}

// TestPrivateNameCollidingWithPackageDeclIsAnError is §3a: reported at
// both sites, and only inside the private member's file does the name go
// ambiguous; every other file keeps using the package-level declaration.
func TestPrivateNameCollidingWithPackageDeclIsAnError(t *testing.T) {
	pkgRule := mustParse(t, "rule helper(n: int) -> int {\n    return n;\n}\n")
	user := mustParse(t, "conversion user(n: int) -> int {\n    return helper(n);\n}\n")
	f := mustParse(t, `rule entry(n: int) -> int {
    return helper(n);
}

private {
    helper(n: int) -> int {
        return n + 1;
    }
}
`)
	diags := Check([]*syntax.File{pkgRule, user, f}, nil)
	dup := 0
	for _, d := range diags {
		if strings.Contains(d.Msg, `duplicate declaration name "helper"`) {
			dup++
			continue
		}
		t.Errorf("only the collision should be reported (downstream suppressed), got %s", d)
	}
	if dup != 2 {
		t.Fatalf("want the collision reported at both sites, got %v", diags)
	}
}

// TestSamePrivateNameInTwoFilesDoesNotCollide: neither file can see the
// other's private members.
func TestSamePrivateNameInTwoFilesDoesNotCollide(t *testing.T) {
	a := mustParse(t, "rule a(n: int) -> int {\n    return helper(n);\n}\n\nprivate {\n    helper(n: int) -> int {\n        return n;\n    }\n}\n")
	b := mustParse(t, "rule b(s: string) -> string {\n    return helper(s);\n}\n\nprivate {\n    helper(s: string) -> string {\n        return s;\n    }\n}\n")
	if diags := Check([]*syntax.File{a, b}, nil); len(diags) != 0 {
		t.Fatalf("same-named private helpers in two files must not collide, got %v", diags)
	}
}

// TestActivityCycleThroughHelpersIsStillB9 is §6's limit: recursion that
// leaves the file, here through a helper, is still a B9 cycle.
func TestActivityCycleThroughHelpersIsStillB9(t *testing.T) {
	ping := mustParse(t, "activity ping(n: int) -> int {\n    return relay(n);\n}\n\nprivate {\n    relay(n: int) -> int {\n        return pong(n);\n    }\n}\n")
	pong := mustParse(t, "activity pong(n: int) -> int {\n    return ping(n);\n}\n")
	if got := rules(Check([]*syntax.File{ping, pong}, nil)); got["B9"] != 2 {
		t.Fatalf("want B9 at ping and pong, got %v", got)
	}
}
