package format

import "testing"

// An arm of a `return match` whose block only returns a value is written as
// that value (ADR 0142 §2: the `return` leaves with the arm's value anyway).
func TestFormatShortensReturningArms(t *testing.T) {
	src := `conversion pick(id: string) -> Result<string, string> {
    return match (lookup(id)) {
        None -> {
            return Err("unknown");
        },

        link -> {
            return Ok(link);
        }
    }
}
`
	want := `conversion pick(id: string) -> Result<string, string> {
    return match (lookup(id)) {
        None -> Err("unknown"),
        link -> Ok(link)
    }
}
`
	if out := mustFormat(t, src); out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	if out := mustFormat(t, want); out != want {
		t.Fatalf("not idempotent:\n%s", out)
	}
}

// What a shorter arm would change or lose keeps its block: a binding's
// match, where `return` leaves the function; an arm that does more than
// return; a commented arm; and a nested match, which can't be an arm value.
func TestFormatKeepsArmsThatArentJustAValue(t *testing.T) {
	src := `conversion pick(id: string, n: int) -> Result<string, string> {
    label = match (n) {
        0 -> {
            return Err("none");
        },
        _ -> "some"
    }
    return match (lookup(id)) {
        None -> {
            // nobody by that id
            return Err("unknown");
        },
        link -> {
            if (n > 1) {
                return Err(label);
            }
            return Ok(link);
        },
        _ -> {
            return match (n) {
                1 -> Ok("one"),
                _ -> Ok("many")
            };
        }
    }
}
`
	if out := mustFormat(t, src); out != src {
		t.Fatalf("changed:\n%s", out)
	}
}
