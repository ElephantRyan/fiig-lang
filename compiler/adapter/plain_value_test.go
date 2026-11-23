package adapter

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// abs-recommendations GAPS G27: a route whose workflow returns a plain
// value maps it with a name, not an Ok arm.
func TestAPlainValueWorkflowMapsWithAName(t *testing.T) {
	h := handlerFor(t, `model Health {
    ok: bool;
}`, `workflow healthCheck() -> Health {
    return Health(ok: true);
}`, `adapter http healthApi {
    GET "/health" -> healthCheck() {
        status -> 200 status
    }
}`)
	code, _, body := serve(h, httptest.NewRequest("GET", "/health", nil))
	if code != 200 || strings.TrimSpace(body) != `{"ok":true}` {
		t.Fatalf("got %d %s, want 200 {\"ok\":true}", code, body)
	}
}
