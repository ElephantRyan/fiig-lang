package adapter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0144: a header wire with `scheme "Bearer"` hands its binder the
// credential alone, and OpenAPI describes it as http bearer.

var bearerProgram = []string{
	"model Peer {\n    name: string;\n}",
	"enum PeerRefusal {\n    NoKey,\n}",
	`workflow callingPeer(key: string | None) -> Result<Peer, PeerRefusal> {
    return match (key) {
        None -> {
            return Err(PeerRefusal.NoKey);
        },
        given -> {
            return Ok(Peer(name: "[${given}]"));
        },
    }
}`,
	`conversion peerRefused(error: PeerRefusal) -> HttpResponse<string> {
    return HttpResponse(status: 401, body: "no key");
}`,
	`workflow whoCalls(peer: Peer) -> string {
    return peer.name;
}`,
	`adapter http api {
    wire peer: Peer from header "Authorization" scheme "Bearer" by callingPeer else peerRefused;

    GET "/who" -> whoCalls(peer)
        produces "application/json";
}`,
}

// §2's table.
func TestABearerWireStripsTheScheme(t *testing.T) {
	h := handlerFor(t, bearerProgram...)
	cases := []struct {
		header string
		code   int
		want   string
	}{
		{"Bearer abc123", 200, "[abc123]"},
		{"bearer abc123", 200, "[abc123]"},
		{"Bearer   abc123 ", 200, "[abc123]"},
		{"Basic dXNlcjpwdw==", 401, "no key"},
		{"Bearer", 401, "no key"},
		{"Bearer ", 401, "no key"},
		{"", 401, "no key"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/who", nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		if code, _, out := serve(h, req); code != c.code || !strings.Contains(out, c.want) {
			t.Errorf("Authorization %q: %d %s, want %d %s", c.header, code, out, c.code, c.want)
		}
	}
}

// §3: the scheme keeps ADR 0141's name and becomes http bearer.
func TestOpenAPIDescribesABearerWire(t *testing.T) {
	var files []*syntax.File
	var ad *syntax.AdapterDecl
	for _, src := range bearerProgram {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		files = append(files, f)
		for _, d := range f.Decls {
			if a, ok := d.(*syntax.AdapterDecl); ok {
				ad = a
			}
		}
	}
	u, _ := types.BuildUniverse(files)
	doc, err := GenerateOpenAPI(ad, u)
	if err != nil {
		t.Fatal(err)
	}
	schemes := doc["components"].(map[string]any)["securitySchemes"].(map[string]any)
	s, _ := schemes["Authorization"].(map[string]any)
	if s["type"] != "http" || s["scheme"] != "bearer" || len(s) != 2 {
		t.Errorf("schemes = %v, want Authorization as http bearer", schemes)
	}
}
