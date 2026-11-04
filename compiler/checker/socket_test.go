package checker

import (
	"strings"
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0060: typed Socket operations, the prelude's Connection and
// SocketError, and H5.

func checkSources(t *testing.T, srcs ...string) []Diagnostic {
	t.Helper()
	files := make([]*syntax.File, len(srcs))
	for i, s := range srcs {
		files[i] = mustParse(t, s)
	}
	return Check(files, nil)
}

func TestSocketOperationsAreTyped(t *testing.T) {
	clean := "gateway echo(host: string, message: string) -> Result<bytes, SocketError>\n" +
		"\trequires Socket\n{\n" +
		"\tconnection = Socket.connectTls(host, 443)?\n" +
		"\tsecure = Socket.startTls(connection, host)?\n" +
		"\tSocket.write(secure, message.utf8())?\n" +
		"\treply = Socket.read(secure)?\n" +
		"\tSocket.close(secure)\n" +
		"\treturn Ok(reply)\n}"
	if diags := checkSources(t, clean); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics for valid Socket use: %+v", diags)
	}

	wrongType := "gateway bad(host: string) -> Result<None, SocketError>\n\trequires Socket\n{\n" +
		"\tconnection = Socket.connect(host, \"5432\")?\n" +
		"\treturn Socket.write(connection, \"text\")\n}"
	if got := rules(checkSources(t, wrongType))["A1"]; got != 2 {
		t.Errorf("got %d A1 for a string port and string data, want 2", got)
	}

	unknownOp := "gateway bad(host: string) -> int\n\trequires Socket\n{\n\tSocket.exchange(host)\n\treturn 0\n}"
	diags := checkSources(t, unknownOp)
	if len(diags) != 1 || !strings.Contains(diags[0].Msg, "Socket has no operation \"exchange\"") {
		t.Errorf("want one unknown-operation diagnostic, got %+v", diags)
	}
}

// TestSocketErrorIsAPreludeEnum: matched and constructed like a declared
// enum, with exhaustiveness (A9) applying.
func TestSocketErrorIsAPreludeEnum(t *testing.T) {
	describe := "conversion describe(failure: SocketError) -> string {\n\tmatch failure {\n" +
		"\t\tSocketError.Tls(reason) -> reason\n\t\tSocketError.Closed -> \"closed\"\n\t}\n}"
	if got := rules(checkSources(t, describe))["A9"]; got != 1 {
		t.Errorf("got %d A9 for a non-exhaustive SocketError match, want 1", got)
	}
	build := "conversion refused() -> SocketError {\n\treturn SocketError.Refused\n}"
	if diags := checkSources(t, build); len(diags) != 0 {
		t.Errorf("unexpected diagnostics constructing a SocketError: %+v", diags)
	}
}

func TestConnectionInWorkflowSignatureIsH5(t *testing.T) {
	session := "model Session {\n\tconnection: Connection\n\tuser: string\n}"
	wrapper := "model Wrapper {\n\tsession: Session\n}"
	cases := map[string]string{
		"param":  "workflow w(connection: Connection) -> int {\n\treturn 0\n}",
		"nested": "workflow w(sessions: List<Wrapper>) -> int {\n\treturn 0\n}",
		"return": "workflow w(id: string) -> Result<Session, string> {\n\treturn Err(id)\n}",
	}
	for name, src := range cases {
		if got := rules(checkSources(t, session, wrapper, src))["H5"]; got != 1 {
			t.Errorf("%s: got %d H5, want 1", name, got)
		}
	}
	// A model holding a Connection is fine anywhere but the boundary.
	inner := "activity a(s: Session) -> Session {\n\treturn s\n}"
	if got := rules(checkSources(t, session, inner))["H5"]; got != 0 {
		t.Errorf("activity with a Connection-holding model: got %d H5, want 0", got)
	}
}

func TestConnectionIsNotComparable(t *testing.T) {
	src := "gateway same(host: string) -> Result<bool, SocketError>\n\trequires Socket\n{\n" +
		"\ta = Socket.connect(host, 1)?\n\tb = Socket.connect(host, 1)?\n\treturn Ok(a == b)\n}"
	diags := checkSources(t, src)
	if len(diags) != 1 || !strings.Contains(diags[0].Msg, "Connection is not comparable") {
		t.Errorf("want one comparability diagnostic, got %+v", diags)
	}
}

// TestSocketOutsideItsGatewayIsB10OrP7: only a gateway that requires
// Socket may call it.
func TestSocketOutsideItsGatewayIsB10OrP7(t *testing.T) {
	for kind, src := range map[string]string{
		"rule":       "rule sneaky(host: string) -> Result<Connection, SocketError> {\n\treturn Socket.connect(host, 1)\n}",
		"conversion": "conversion sneaky(host: string) -> Result<Connection, SocketError> {\n\treturn Socket.connect(host, 1)\n}",
		"activity":   "activity sneaky(host: string) -> Result<Connection, SocketError> {\n\treturn Socket.connect(host, 1)\n}",
		"workflow":   "workflow sneaky(host: string) -> Result<None, SocketError> {\n\tc = Socket.connect(host, 1)?\n\treturn Ok(None)\n}",
	} {
		if got := rules(checkSources(t, src))["B10"]; got != 1 {
			t.Errorf("%s calling Socket: got %d B10, want 1", kind, got)
		}
	}
	other := "gateway now(host: string) -> Result<Connection, SocketError>\n\trequires FileSystem\n{\n\treturn Socket.connect(host, 1)\n}"
	if got := rules(checkSources(t, other))["P7"]; got != 1 {
		t.Errorf("gateway requiring FileSystem calling Socket: got %d P7, want 1", got)
	}
}
