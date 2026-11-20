package lsp

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ADR 0089 over the protocol: a client talks to the server through pipes,
// against a workspace of two packages, one depending on the other.

var workspaceFiles = map[string]string{
	"kernel/fiig.pkg.json": `{"path": "t.kernel", "kind": "library", "version": "1.0.0"}`,
	"kernel/models/money.fiig": `// An amount in one currency.
model Money {
    amount: decimal
    currency: string
}
`,
	"app/fiig.pkg.json": `{"path": "t.app", "kind": "application", "version": "1.0.0", "dependencies": {"t.kernel": "1.0.0"}}`,
	"app/rules/total.fiig": `use t.kernel

// Adds two amounts.
rule total(a: kernel.Money, b: kernel.Money) -> decimal {
    sum = a.amount + b.amount
    return sum
}
`,
	"app/rules/greet.fiig": `rule greet(name: string) -> string {
    return "é${name}"
}
`,
	"app/conversions/welcome.fiig": `conversion welcome(name: string) -> string {
    return shout(greet(name))
}

private {
    shout(s: string) -> string {
        return "${s}!"
    }
}
`,
	"app/rules/broken.fiig": `rule broken(n: int) -> string {
    return n
}
`,
}

type client struct {
	t      *testing.T
	conn   *conn
	msgs   chan *message
	root   string
	nextID int
	notes  []*message
}

func startClient(t *testing.T, encodings []string) (*client, json.RawMessage) {
	t.Helper()
	debounce = 5 * time.Millisecond
	root := t.TempDir()
	for name, text := range workspaceFiles {
		p := filepath.Join(root, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go Main(inR, outW)
	c := &client{t: t, conn: newConn(outR, inW), msgs: make(chan *message, 256), root: root}
	go func() {
		for {
			m, err := c.conn.read()
			if err != nil {
				close(c.msgs)
				return
			}
			c.msgs <- m
		}
	}()
	t.Cleanup(func() {
		c.conn.notify("exit", nil)
		inW.Close()
	})
	caps := map[string]any{}
	if encodings != nil {
		caps["general"] = map[string]any{"positionEncodings": encodings}
	}
	result := c.request("initialize", map[string]any{"rootUri": pathToURI(root), "capabilities": caps})
	c.conn.notify("initialized", map[string]any{})
	return c, result
}

func (c *client) path(rel string) string { return filepath.Join(c.root, filepath.FromSlash(rel)) }
func (c *client) uri(rel string) string  { return pathToURI(c.path(rel)) }

func (c *client) request(method string, params any) json.RawMessage {
	c.t.Helper()
	c.nextID++
	id := json.RawMessage(`` + itoa(c.nextID))
	raw, _ := json.Marshal(params)
	c.conn.write(&message{ID: &id, Method: method, Params: raw})
	timeout := time.After(10 * time.Second)
	for {
		select {
		case m, ok := <-c.msgs:
			if !ok {
				c.t.Fatalf("%s: server closed", method)
			}
			if m.ID != nil && string(*m.ID) == string(id) {
				if m.Error != nil {
					c.t.Fatalf("%s: %s", method, m.Error.Message)
				}
				out, _ := json.Marshal(m.Result)
				return out
			}
			c.notes = append(c.notes, m)
		case <-timeout:
			c.t.Fatalf("%s: no response", method)
		}
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// diagnostics waits for the diagnostics published for uri that satisfy
// ok, and returns them.
func (c *client) diagnostics(uri string, ok func([]lspDiagnostic) bool) []lspDiagnostic {
	c.t.Helper()
	check := func(m *message) ([]lspDiagnostic, bool) {
		if m.Method != "textDocument/publishDiagnostics" {
			return nil, false
		}
		var p publishDiagnosticsParams
		json.Unmarshal(m.Params, &p)
		if p.URI != uri || !ok(p.Diagnostics) {
			return nil, false
		}
		return p.Diagnostics, true
	}
	for _, m := range c.notes {
		if d, found := check(m); found {
			return d
		}
	}
	timeout := time.After(10 * time.Second)
	for {
		select {
		case m := <-c.msgs:
			c.notes = append(c.notes, m)
			if d, found := check(m); found {
				return d
			}
		case <-timeout:
			c.t.Fatalf("no matching diagnostics for %s", uri)
		}
	}
}

func (c *client) open(rel, text string) {
	c.notes = nil
	c.conn.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": c.uri(rel), "languageId": "fiig", "version": 1, "text": text}})
}

func (c *client) at(rel string, line, char int) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": c.uri(rel)}, "position": map[string]any{"line": line, "character": char}}
}

func decode[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

func TestEncodingIsNegotiated(t *testing.T) {
	_, result := startClient(t, []string{"utf-16", "utf-8", "utf-32"})
	if !strings.Contains(string(result), `"positionEncoding":"utf-32"`) {
		t.Errorf("initialize = %s, want utf-32 chosen", result)
	}
	_, result = startClient(t, nil)
	if !strings.Contains(string(result), `"positionEncoding":"utf-16"`) {
		t.Errorf("initialize = %s, want utf-16 by default", result)
	}
}

func TestDiagnosticsAppearAndClear(t *testing.T) {
	c, _ := startClient(t, nil)
	diags := c.diagnostics(c.uri("app/rules/broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	d := diags[0]
	if d.Code != "A1" || d.Severity != 1 || d.Range.Start.Line != 1 || d.Range.End.Character <= d.Range.Start.Character {
		t.Fatalf("diagnostic = %+v, want A1 as an error with a range on line 2", d)
	}
	c.open("app/rules/broken.fiig", "rule broken(n: int) -> int {\n    return n\n}\n")
	c.diagnostics(c.uri("app/rules/broken.fiig"), func(d []lspDiagnostic) bool { return len(d) == 0 })
}

func TestDefinitionAcrossPackagesAndIntoFields(t *testing.T) {
	c, _ := startClient(t, nil)
	c.diagnostics(c.uri("app/rules/broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	// `kernel.Money` in total's signature: line 3, "rule total(a: kernel.Money" -> Money at character 22.
	locs := decode[[]location](t, c.request("textDocument/definition", c.at("app/rules/total.fiig", 3, 23)))
	if len(locs) != 1 || locs[0].URI != c.uri("kernel/models/money.fiig") || locs[0].Range.Start.Line != 1 || locs[0].Range.Start.Character != 6 {
		t.Fatalf("definition of Money = %+v, want money.fiig 2:7", locs)
	}
	// `a.amount`: line 4, "    sum = a.amount" -> amount at character 12.
	locs = decode[[]location](t, c.request("textDocument/definition", c.at("app/rules/total.fiig", 4, 13)))
	if len(locs) != 1 || locs[0].URI != c.uri("kernel/models/money.fiig") || locs[0].Range.Start.Line != 2 {
		t.Fatalf("definition of amount = %+v, want money.fiig line 3", locs)
	}
	// The local `sum` in `return sum`: line 5, character 11.
	locs = decode[[]location](t, c.request("textDocument/definition", c.at("app/rules/total.fiig", 5, 12)))
	if len(locs) != 1 || locs[0].Range.Start.Line != 4 || locs[0].Range.Start.Character != 4 {
		t.Fatalf("definition of sum = %+v, want its binding at 5:5", locs)
	}
	// A parameter: `a` in `a.amount`.
	locs = decode[[]location](t, c.request("textDocument/definition", c.at("app/rules/total.fiig", 4, 10)))
	if len(locs) != 1 || locs[0].Range.Start.Line != 3 || locs[0].Range.Start.Character != 11 {
		t.Fatalf("definition of a = %+v, want the parameter at 4:12", locs)
	}
}

func TestReferencesHoverSymbolsAndFormatting(t *testing.T) {
	c, _ := startClient(t, nil)
	c.diagnostics(c.uri("app/rules/broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	// From Money's own declaration: both uses in total.fiig.
	refs := decode[[]location](t, c.request("textDocument/references", c.at("kernel/models/money.fiig", 1, 8)))
	n := 0
	for _, r := range refs {
		if r.URI == c.uri("app/rules/total.fiig") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("references to Money = %+v, want two in total.fiig", refs)
	}
	h := decode[hover](t, c.request("textDocument/hover", c.at("app/rules/total.fiig", 3, 23)))
	if !strings.Contains(h.Contents.Value, "model Money {") || !strings.Contains(h.Contents.Value, "amount: decimal") {
		t.Errorf("hover on Money = %q", h.Contents.Value)
	}
	h = decode[hover](t, c.request("textDocument/hover", c.at("app/rules/total.fiig", 5, 12)))
	if !strings.Contains(h.Contents.Value, "decimal") {
		t.Errorf("hover on sum = %q, want its type", h.Contents.Value)
	}
	h = decode[hover](t, c.request("textDocument/hover", c.at("app/rules/greet.fiig", 0, 6)))
	syms := decode[[]documentSymbol](t, c.request("textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": c.uri("kernel/models/money.fiig")}}))
	if len(syms) != 1 || syms[0].Name != "Money" || len(syms[0].Children) != 2 || syms[0].Children[0].Name != "amount" {
		t.Errorf("symbols = %+v", syms)
	}
	ws := decode[[]symbolInformation](t, c.request("workspace/symbol", map[string]any{"query": "mon"}))
	if len(ws) != 1 || ws[0].Name != "Money" || ws[0].ContainerName != "t.kernel" {
		t.Errorf("workspace symbols = %+v", ws)
	}
	c.open("app/rules/greet.fiig", "rule greet(name: string) -> string {\n  return \"é${name}\"\n}\n")
	edits := decode[[]textEdit](t, c.request("textDocument/formatting", map[string]any{"textDocument": map[string]any{"uri": c.uri("app/rules/greet.fiig")}}))
	if len(edits) != 1 || !strings.Contains(edits[0].NewText, "    return") {
		t.Errorf("formatting = %+v", edits)
	}
}

func TestPositionsCountTheNegotiatedUnits(t *testing.T) {
	// `    return "é${name}"`: name starts at code point 15, byte 16.
	for _, tc := range []struct {
		enc  []string
		char int
	}{{nil, 15}, {[]string{"utf-8"}, 16}, {[]string{"utf-32"}, 15}} {
		c, _ := startClient(t, tc.enc)
		c.diagnostics(c.uri("app/rules/broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
		locs := decode[[]location](t, c.request("textDocument/definition", c.at("app/rules/greet.fiig", 1, tc.char)))
		if len(locs) != 1 || locs[0].Range.Start.Line != 0 || locs[0].Range.Start.Character != 11 {
			t.Errorf("%v: definition of name = %+v, want the parameter at 1:12", tc.enc, locs)
		}
	}
}

func TestAHalfTypedFileKeepsNavigation(t *testing.T) {
	c, _ := startClient(t, nil)
	c.diagnostics(c.uri("app/rules/broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	c.open("app/rules/total.fiig", strings.Replace(workspaceFiles["app/rules/total.fiig"], "    return sum\n", "    return sum +\n", 1))
	c.diagnostics(c.uri("app/rules/total.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	locs := decode[[]location](t, c.request("textDocument/definition", c.at("app/rules/total.fiig", 3, 23)))
	if len(locs) != 1 || locs[0].URI != c.uri("kernel/models/money.fiig") {
		t.Fatalf("definition while the file doesn't parse = %+v, want the last good answer", locs)
	}
}

// ADR 0106 §1: each call is a token of its callee's kind.
func TestSemanticTokensGiveEachCallItsKind(t *testing.T) {
	c, init := startClient(t, nil)
	if !strings.Contains(string(init), `"semanticTokensProvider"`) {
		t.Fatalf("initialize doesn't offer semantic tokens: %s", init)
	}
	c.diagnostics(c.uri("app/rules/broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	got := decode[struct {
		Data []int `json:"data"`
	}](t, c.request("textDocument/semanticTokens/full", map[string]any{"textDocument": map[string]any{"uri": c.uri("app/conversions/welcome.fiig")}}))
	// shout (helper, 6) at 1:11, then greet (rule, 0) at 1:17.
	want := []int{1, 11, 5, 6, 0, 0, 6, 5, 0, 0}
	if fmt.Sprint(got.Data) != fmt.Sprint(want) {
		t.Errorf("tokens = %v, want %v", got.Data, want)
	}
}
