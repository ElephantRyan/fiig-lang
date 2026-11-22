package runtime

import (
	"context"
	"strings"
	"testing"
)

// ADR 0068 §5: a builtin's named arguments reach the parameters they name,
// whatever order they're written in.
func TestBuiltinNamedArgs(t *testing.T) {
	src := `rule replaced(s: string) -> string {
    return s.replaceAll(new: "/", old: "-");
}

rule sliced(s: string) -> string {
    return s.slice(to: 3, from: 1);
}

rule mixed(s: string) -> string {
    return s.replaceAll("-", new: "+");
}

rule appended(s: string) -> string {
    xs = append(element: s, list: ["a", "b"]);
    return xs.join(separator: ", ");
}

rule put(n: int) -> string {
    empty: Map<string, int> = [:];
    m = empty.put(value: n, key: "k");
    return "${m.at(key: "k")}";
}`
	cases := []struct {
		name string
		args map[string]Value
		want string
	}{
		{"replaced", map[string]Value{"s": Str("a-b")}, "a/b"},
		{"sliced", map[string]Value{"s": Str("abcd")}, "bc"},
		{"mixed", map[string]Value{"s": Str("a-b")}, "a+b"},
		{"put", map[string]Value{"n": Int(7)}, "7"},
		{"appended", map[string]Value{"s": Str("c")}, "a, b, c"},
	}
	for _, c := range cases {
		got, fault := runEach(t, src, c.name, c.args)
		if fault != nil || got == nil || got.String() != c.want {
			t.Errorf("%s(%v) = %v, %v; want %s", c.name, c.args, got, fault, c.want)
		}
	}
}

// Socket's operations bind by name too: naming startTls's arguments in the
// other order still upgrades the connection, so writing to the old one
// faults.
func TestSocketNamedArgs(t *testing.T) {
	src := `gateway upgraded(host: string) -> Result<None, SocketError>
    requires Socket
{
    connection = Socket.connect(port: 1, host: host)?;
    secure = Socket.startTls(serverName: host, connection: connection)?;
    return Socket.write(data: <<1: u8>>, connection: connection);
}
`
	caps := NewCapabilityRegistry()
	caps.Register("Socket", SocketProvider{Backend: &ScriptedSocket{Peers: []*SocketPeer{{Host: "h", Port: 1, Steps: []SocketStep{{StartTLS: true}}}}}})
	_, fault := newTestInterp(universeFromSource(t, src), caps).Call(context.Background(), "upgraded", map[string]Value{"host": Str("h")})
	if fault == nil || !strings.Contains(fault.Error(), "upgraded by startTls") {
		t.Errorf("got fault %v, want one containing %q", fault, "upgraded by startTls")
	}
}
