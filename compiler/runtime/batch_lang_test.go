package runtime

import (
	"context"
	"strings"
	"testing"
)

// ADR 0148: a batch read in a fiig program, in input order, with each
// element's outcome and the batch's length.
func TestBatchPrograms(t *testing.T) {
	src := `conversion half(n: int) -> Result<int, string> {
    if (n % 2 == 1) {
        return Err("odd");
    }
    return Ok(n / 2);
}

conversion describe(ns: List<int>) -> string {
    halves = concurrent(n in ns, limit: 3): half(n);
    out: string = "";
    for (n, outcome in halves) {
        match (outcome) {
            Ok(h) -> {
                out = "${out}${n}=${h};";
            },
            Err(why) -> {
                out = "${out}${n} ${why};";
            }
        }
    }
    return "${out} ${halves.length()}";
}

conversion outcomesOnly(ns: List<int>) -> int {
    halves = concurrent(n in ns): half(n);
    failed: int = 0;
    for (_, outcome in halves) {
        match (outcome) {
            Ok(_) -> {
            },
            Err(_) -> {
                failed = failed + 1;
            }
        }
    }
    return failed;
}`
	parts := strings.Split(src, "\n\nconversion ")
	for i := 1; i < len(parts); i++ {
		parts[i] = "conversion " + parts[i]
	}
	checkClean(t, parts...)
	in := newTestInterp(universeFromSource(t, parts...), nil)
	run := func(name string, args map[string]Value) (Value, *Fault) {
		return in.Call(context.Background(), name, args)
	}
	ns := make([]Value, 40)
	want := ""
	for i := range ns {
		ns[i] = Int(i)
		if i%2 == 1 {
			want += Int(i).String() + " odd;"
		} else {
			want += Int(i).String() + "=" + Int(i/2).String() + ";"
		}
	}
	want += " 40"
	got, fault := run("describe", map[string]Value{"ns": List{Elems: ns}})
	if fault != nil || got.String() != want {
		t.Fatalf("describe = %v, %v; want %s", got, fault, want)
	}
	got, fault = run("outcomesOnly", map[string]Value{"ns": List{Elems: ns}})
	if fault != nil || got != Int(20) {
		t.Fatalf("outcomesOnly = %v, %v", got, fault)
	}
	got, fault = run("describe", map[string]Value{"ns": List{}})
	if fault != nil || got.String() != " 0" {
		t.Fatalf("an empty batch = %v, %v", got, fault)
	}
}
