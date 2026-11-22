package format

import "testing"

// ADR 0137: an adapter's principals print before its routes, a route names
// a principal by its bare name, and `public` follows the path.
func TestFormatPrincipalsAreStable(t *testing.T) {
	src := `adapter http ratingsApi {
    wire me: accounts.Identity from cookie "sid" by sessions.signedIn else sessions.signInFirst;
    wire admin: Admin from me by signedInAdmin else notAnAdmin;

    PUT "/ratings/{itemId}" -> putRating(me, itemId: string, input: RatingInput)
            consumes "application/json" {
        Ok(rating) -> 200 rating,
        Err(error) -> ratingErrorResponse(error),
    }

    GET "/covers/{id}" public -> showCover(id: string) {
        _ -> 200,
    }
}
`
	if out := mustFormat(t, src); out != src {
		t.Fatalf("not stable:\n%s", out)
	}
}

func TestFormatWrapsALongPrincipal(t *testing.T) {
	src := "adapter http a {\n    wire me: accounts.Identity from cookie \"session-identifier-cookie\" by sessions.signedInPersonFromCookie else sessions.signInFirstPlease;\n}\n"
	want := "adapter http a {\n    wire me: accounts.Identity from cookie \"session-identifier-cookie\"\n        by sessions.signedInPersonFromCookie else sessions.signInFirstPlease;\n}\n"
	if out := mustFormat(t, src); out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	if out := mustFormat(t, want); out != want {
		t.Fatalf("wrapped form not stable:\n%s", out)
	}
}

// ADR 0141: a top-level principal, and an adapter naming principals.
func TestFormatNamedPrincipalsAreStable(t *testing.T) {
	for _, src := range []string{
		"wire me: accounts.Identity from cookie \"sid\" by signedIn else identityErrorResponse;\n",
		"wire admin: accounts.Admin from me by accounts.signedInAdmin else accounts.notAnAdmin;\n",
		`adapter http adminApi {
    wire peer: LinkedServer from header "Authorization" by callingServer else peerRefused;
    wire sessions.me;
    wire partners.me as partner;

    GET "/stats" -> stats(me) {
        _ -> 200,
    }
}
`,
	} {
		if out := mustFormat(t, src); out != src {
			t.Fatalf("not stable:\n%s", out)
		}
	}
}
