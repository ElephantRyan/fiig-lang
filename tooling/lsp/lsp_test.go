package lsp

import (
	"encoding/json"
	"fiig-lang/internal/fixture"
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
	"kernel/src/Money.fiig": `// An amount in one currency.
model Money {
    amount: decimal
    currency: string
}
`,
	"app/fiig.pkg.json": `{"path": "t.app", "kind": "library", "version": "1.0.0", "dependencies": {"t.kernel": "1.0.0"}, "capabilities": ["Database"], "nativeCapabilities": ["Database"]}`,
	"app/src/Total.fiig": `use t.kernel

// Adds two amounts.
rule total(a: kernel.Money, b: kernel.Money) -> decimal {
    sum = a.amount + b.amount
    return sum
}
`,
	"app/src/Greet.fiig": `rule greet(name: string) -> string {
    return "é${name}"
}
`,
	"app/src/Welcome.fiig": `conversion welcome(name: string) -> string {
    return shout(greet(name))
}

private {
    shout(s: string) -> string {
        return "${s}!"
    }
}
`,
	// Another package's enum in a pattern, and a config read.
	"kernel/src/Level.fiig": `enum Level {
    Low
    High
}
`,
	"app/src/Describe.fiig": `use t.kernel

conversion describe(level: kernel.Level) -> string {
    match level {
        kernel.Level.Low -> "low"
        kernel.Level.High -> "high"
    }
}
`,
	"app/src/Limits.fiig": `config Limits {
    max: int from env "LIMIT_MAX" = 10
}
`,
	"app/src/Cap.fiig": `activity cap() -> int {
    first = Limits.max
    return first
}
`,
	// A gateway requires a capability group.
	"app/src/Ledger.fiig": `capability Ledger {
    gateway record(entry: string) -> int
        requires Database
    {
        return Database.execute("INSERT INTO ledger (entry) VALUES (?)", entry)
    }
}
`,
	"app/src/Audit.fiig": `gateway audit(entry: string) -> int
    requires Ledger
{
    return Ledger.record(entry)
}
`,
	// An application names adapters, which Go to Declaration reaches (ADR 0132).
	"app/src/Runs.fiig": `use t.app.ratings

application Runs {
    serve web {
        Api
        ratings.clampApi
        ratings.whoApi
    }
}
`,
	"app/src/ratings/ClampApi.fiig": `adapter http clampApi {
    GET "/clamp/{score}" -> clampRoute(score: int)
        produces "application/json"
}
`,
	// A route names its adapter's principal, which Go to Declaration and hover
	// reach (ADR 0137).
	"app/src/ratings/WhoApi.fiig": `adapter http whoApi {
    principal me: Caller from header "Authorization" by signedIn else refused

    GET "/who" -> who(me)
        produces "application/json"
}
`,
	"app/src/ratings/Caller.fiig": `model Caller {
    id: string
}
`,
	"app/src/ratings/CallerError.fiig": `enum CallerError {
    Unknown
}
`,
	"app/src/ratings/SignedIn.fiig": `workflow signedIn(key: string | None) -> Result<Caller, CallerError> {
    match key {
        None -> {
            return Err(CallerError.Unknown)
        }
        given -> {
            return Ok(Caller(id: given))
        }
    }
}
`,
	"app/src/ratings/Refused.fiig": `conversion refused(error: CallerError) -> HttpResponse<string> {
    return HttpResponse(status: 401, body: "who are you")
}
`,
	"app/src/ratings/Who.fiig": `workflow who(me: Caller) -> string {
    return me.id
}
`,
	"app/src/ratings/ClampRoute.fiig": `workflow clampRoute(score: int) -> int {
    return score
}
`,
	// A route names a workflow, which Go to Declaration reaches.
	"app/src/Hello.fiig": `workflow hello() -> string {
    return "hi"
}
`,
	"app/src/Api.fiig": `adapter http Api {
    GET "/hello" -> hello()
        produces "application/json"
}
`,
	"app/src/Rated.fiig": `use t.app.ratings

conversion rated(score: int) -> int {
    return ratings.clamp(score)
}
`,
	"app/src/ratings/Clamp.fiig": `conversion clamp(score: int) -> int {
    return math.min(score, 10)
}
`,
	"app/test/ratings/Clamp.fiig": `test clampsHigh {
    expect clamp(11) == 10
}
`,
	"app/src/Broken.fiig": `rule broken(n: int) -> string {
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
	for name, text := range fixture.Module(workspaceFiles) {
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
	diags := c.diagnostics(c.uri("app/src/Broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	d := diags[0]
	if d.Code != "A1" || d.Severity != 1 || d.Range.Start.Line != 1 || d.Range.End.Character <= d.Range.Start.Character {
		t.Fatalf("diagnostic = %+v, want A1 as an error with a range on line 2", d)
	}
	c.open("app/src/Broken.fiig", "rule broken(n: int) -> int {\n    return n\n}\n")
	c.diagnostics(c.uri("app/src/Broken.fiig"), func(d []lspDiagnostic) bool { return len(d) == 0 })
}

func TestDefinitionAcrossPackagesAndIntoFields(t *testing.T) {
	c, _ := startClient(t, nil)
	c.diagnostics(c.uri("app/src/Broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	// `kernel.Money` in total's signature: line 3, "rule total(a: kernel.Money" -> Money at character 22.
	locs := decode[[]location](t, c.request("textDocument/definition", c.at("app/src/Total.fiig", 3, 23)))
	if len(locs) != 1 || locs[0].URI != c.uri("kernel/src/Money.fiig") || locs[0].Range.Start.Line != 1 || locs[0].Range.Start.Character != 6 {
		t.Fatalf("definition of Money = %+v, want money.fiig 2:7", locs)
	}
	// `a.amount`: line 4, "    sum = a.amount" -> amount at character 12.
	locs = decode[[]location](t, c.request("textDocument/definition", c.at("app/src/Total.fiig", 4, 13)))
	if len(locs) != 1 || locs[0].URI != c.uri("kernel/src/Money.fiig") || locs[0].Range.Start.Line != 2 {
		t.Fatalf("definition of amount = %+v, want money.fiig line 3", locs)
	}
	// The local `sum` in `return sum`: line 5, character 11.
	locs = decode[[]location](t, c.request("textDocument/definition", c.at("app/src/Total.fiig", 5, 12)))
	if len(locs) != 1 || locs[0].Range.Start.Line != 4 || locs[0].Range.Start.Character != 4 {
		t.Fatalf("definition of sum = %+v, want its binding at 5:5", locs)
	}
	// A parameter: `a` in `a.amount`.
	locs = decode[[]location](t, c.request("textDocument/definition", c.at("app/src/Total.fiig", 4, 10)))
	if len(locs) != 1 || locs[0].Range.Start.Line != 3 || locs[0].Range.Start.Character != 11 {
		t.Fatalf("definition of a = %+v, want the parameter at 4:12", locs)
	}
}

func TestReferencesHoverSymbolsAndFormatting(t *testing.T) {
	c, _ := startClient(t, nil)
	c.diagnostics(c.uri("app/src/Broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	// From Money's own declaration: both uses in total.fiig.
	refs := decode[[]location](t, c.request("textDocument/references", c.at("kernel/src/Money.fiig", 1, 8)))
	n := 0
	for _, r := range refs {
		if r.URI == c.uri("app/src/Total.fiig") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("references to Money = %+v, want two in total.fiig", refs)
	}
	h := decode[hover](t, c.request("textDocument/hover", c.at("app/src/Total.fiig", 3, 23)))
	if !strings.Contains(h.Contents.Value, "model Money {") || !strings.Contains(h.Contents.Value, "amount: decimal") {
		t.Errorf("hover on Money = %q", h.Contents.Value)
	}
	h = decode[hover](t, c.request("textDocument/hover", c.at("app/src/Total.fiig", 5, 12)))
	if !strings.Contains(h.Contents.Value, "decimal") {
		t.Errorf("hover on sum = %q, want its type", h.Contents.Value)
	}
	h = decode[hover](t, c.request("textDocument/hover", c.at("app/src/Greet.fiig", 0, 6)))
	syms := decode[[]documentSymbol](t, c.request("textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": c.uri("kernel/src/Money.fiig")}}))
	if len(syms) != 1 || syms[0].Name != "Money" || len(syms[0].Children) != 2 || syms[0].Children[0].Name != "amount" {
		t.Errorf("symbols = %+v", syms)
	}
	ws := decode[[]symbolInformation](t, c.request("workspace/symbol", map[string]any{"query": "mon"}))
	if len(ws) != 1 || ws[0].Name != "Money" || ws[0].ContainerName != "t.kernel" {
		t.Errorf("workspace symbols = %+v", ws)
	}
	c.open("app/src/Greet.fiig", "rule greet(name: string) -> string {\n  return \"é${name}\"\n}\n")
	edits := decode[[]textEdit](t, c.request("textDocument/formatting", map[string]any{"textDocument": map[string]any{"uri": c.uri("app/src/Greet.fiig")}}))
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
		c.diagnostics(c.uri("app/src/Broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
		locs := decode[[]location](t, c.request("textDocument/definition", c.at("app/src/Greet.fiig", 1, tc.char)))
		if len(locs) != 1 || locs[0].Range.Start.Line != 0 || locs[0].Range.Start.Character != 11 {
			t.Errorf("%v: definition of name = %+v, want the parameter at 1:12", tc.enc, locs)
		}
	}
}

func TestAHalfTypedFileKeepsNavigation(t *testing.T) {
	c, _ := startClient(t, nil)
	c.diagnostics(c.uri("app/src/Broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	c.open("app/src/Total.fiig", strings.Replace(workspaceFiles["app/src/Total.fiig"], "    return sum\n", "    return sum +\n", 1))
	c.diagnostics(c.uri("app/src/Total.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	locs := decode[[]location](t, c.request("textDocument/definition", c.at("app/src/Total.fiig", 3, 23)))
	if len(locs) != 1 || locs[0].URI != c.uri("kernel/src/Money.fiig") {
		t.Fatalf("definition while the file doesn't parse = %+v, want the last good answer", locs)
	}
}

// ADR 0106 §1: each call is a token of its callee's kind.
func TestSemanticTokensGiveEachCallItsKind(t *testing.T) {
	c, init := startClient(t, nil)
	if !strings.Contains(string(init), `"semanticTokensProvider"`) {
		t.Fatalf("initialize doesn't offer semantic tokens: %s", init)
	}
	c.diagnostics(c.uri("app/src/Broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	got := decode[struct {
		Data []int `json:"data"`
	}](t, c.request("textDocument/semanticTokens/full", map[string]any{"textDocument": map[string]any{"uri": c.uri("app/src/Welcome.fiig")}}))
	// shout (helper, 6) at 1:11, then greet (rule, 0) at 1:17.
	want := []int{1, 11, 5, 6, 0, 0, 6, 5, 0, 0}
	if fmt.Sprint(got.Data) != fmt.Sprint(want) {
		t.Errorf("tokens = %v, want %v", got.Data, want)
	}
}

// ADR 0105: a folder under src/ is a package. Definition goes from the
// top-level package into it, and its test file is checked with it.
// abs-recommendations: Ctrl+click on the workflow a route calls went nowhere,
// since the route's name was never recorded as a reference.
func TestARoutesWorkflowNavigates(t *testing.T) {
	c, _ := startClient(t, nil)
	c.diagnostics(c.uri("app/src/Broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	// `    GET "/hello" -> hello()`: line 1, and hello starts at character 20.
	locs := decode[[]location](t, c.request("textDocument/definition", c.at("app/src/Api.fiig", 1, 22)))
	if len(locs) != 1 || locs[0].URI != c.uri("app/src/Hello.fiig") || locs[0].Range.Start.Line != 0 {
		t.Fatalf("definition of the route's workflow = %+v, want app/src/Hello.fiig", locs)
	}
	// And usages find the route from the workflow.
	refs := decode[[]location](t, c.request("textDocument/references", c.at("app/src/Hello.fiig", 0, 11)))
	found := false
	for _, r := range refs {
		found = found || r.URI == c.uri("app/src/Api.fiig")
	}
	if !found {
		t.Errorf("references of hello = %+v, want the route in Api.fiig", refs)
	}
	// Ctrl+click on the declaration's own name lists its users, the same set.
	locs = decode[[]location](t, c.request("textDocument/definition", c.at("app/src/Hello.fiig", 0, 11)))
	if len(locs) != len(refs) || len(locs) == 0 || locs[0].URI != c.uri("app/src/Api.fiig") {
		t.Errorf("definition on hello's own name = %+v, want its references %+v", locs, refs)
	}
}

// abs-recommendations: a variant of another package's enum in a pattern
// (`accounts.SignInError.WrongMethod ->`), and a config read (`Oidc.issuer`),
// had no definition.
func TestPatternVariantsAndConfigReadsNavigate(t *testing.T) {
	c, _ := startClient(t, nil)
	c.diagnostics(c.uri("app/src/Broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	// `        kernel.Level.Low -> "low"`: Level at character 15, Low at 21.
	for _, tc := range []struct {
		name      string
		line, col int
		file      string
		wantLine  int
	}{
		{"the enum in a pattern", 4, 16, "kernel/src/Level.fiig", 0},
		{"the variant in a pattern", 4, 22, "kernel/src/Level.fiig", 1},
	} {
		locs := decode[[]location](t, c.request("textDocument/definition", c.at("app/src/Describe.fiig", tc.line, tc.col)))
		if len(locs) != 1 || locs[0].URI != c.uri(tc.file) || locs[0].Range.Start.Line != tc.wantLine {
			t.Errorf("%s: definition = %+v, want %s line %d", tc.name, locs, tc.file, tc.wantLine)
		}
	}
	// `    first = Limits.max`, with an indented line after it: Limits at
	// character 12, max at 19. A field ending its line was recorded at the next
	// token's position, a few characters on.
	for _, tc := range []struct {
		name     string
		col      int
		wantLine int
	}{{"the config", 13, 0}, {"the config's field", 19, 1}, {"the config's field, last character", 21, 1}} {
		locs := decode[[]location](t, c.request("textDocument/definition", c.at("app/src/Cap.fiig", 1, tc.col)))
		if len(locs) != 1 || locs[0].URI != c.uri("app/src/Limits.fiig") || locs[0].Range.Start.Line != tc.wantLine {
			t.Errorf("%s: definition = %+v, want Limits.fiig line %d", tc.name, locs, tc.wantLine)
		}
	}
}

// A `requires` clause names a capability group, which Go to Declaration
// reaches (abs-recommendations: `requires AuthentikSignIn`).
func TestARequiresClauseNavigates(t *testing.T) {
	c, _ := startClient(t, nil)
	c.diagnostics(c.uri("app/src/Broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	// `    requires Ledger`: line 1, Ledger at character 13.
	locs := decode[[]location](t, c.request("textDocument/definition", c.at("app/src/Audit.fiig", 1, 14)))
	if len(locs) != 1 || locs[0].URI != c.uri("app/src/Ledger.fiig") || locs[0].Range.Start.Line != 0 {
		t.Fatalf("definition of the required group = %+v, want app/src/Ledger.fiig", locs)
	}
}

// ADR 0132: Ctrl+click on an adapter named in an application goes to it.
func TestAnApplicationsAdaptersNavigate(t *testing.T) {
	c, _ := startClient(t, nil)
	c.diagnostics(c.uri("app/src/Broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	// `        Api`: line 4, character 8: an adapter of the same package.
	locs := decode[[]location](t, c.request("textDocument/definition", c.at("app/src/Runs.fiig", 4, 9)))
	if len(locs) != 1 || locs[0].URI != c.uri("app/src/Api.fiig") || locs[0].Range.Start.Line != 0 {
		t.Errorf("definition of Api = %+v, want app/src/Api.fiig", locs)
	}
	// `        ratings.clampApi`: line 5, the adapter after the alias at character 16.
	locs = decode[[]location](t, c.request("textDocument/definition", c.at("app/src/Runs.fiig", 5, 17)))
	if len(locs) != 1 || locs[0].URI != c.uri("app/src/ratings/ClampApi.fiig") {
		t.Errorf("definition of ratings.clampApi = %+v, want app/src/ratings/ClampApi.fiig", locs)
	}
	// And Find Usages on the adapter finds the application.
	refs := decode[[]location](t, c.request("textDocument/references", c.at("app/src/ratings/ClampApi.fiig", 0, 14)))
	found := false
	for _, r := range refs {
		found = found || r.URI == c.uri("app/src/Runs.fiig")
	}
	if !found {
		t.Errorf("references of clampApi = %+v, want the application", refs)
	}
}

func TestFolderPackagesNavigate(t *testing.T) {
	c, _ := startClient(t, nil)
	c.diagnostics(c.uri("app/src/Broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	locs := decode[[]location](t, c.request("textDocument/definition", c.at("app/src/Rated.fiig", 3, 20)))
	if len(locs) != 1 || locs[0].URI != c.uri("app/src/ratings/Clamp.fiig") {
		t.Fatalf("definition of ratings.clamp = %+v, want app/src/ratings/Clamp.fiig", locs)
	}
	c.open("app/test/ratings/Clamp.fiig", "test clampsHigh {\n    expect clamp(11) == \"ten\"\n}\n")
	c.diagnostics(c.uri("app/test/ratings/Clamp.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
}

// ADR 0137: a principal named by a route reaches its declaration, hovers as
// one, and its binder navigates like a route's workflow.
func TestAPrincipalNavigates(t *testing.T) {
	c, _ := startClient(t, nil)
	c.diagnostics(c.uri("app/src/Broken.fiig"), func(d []lspDiagnostic) bool { return len(d) > 0 })
	// `    GET "/who" -> who(me)`: line 3, me at character 22.
	locs := decode[[]location](t, c.request("textDocument/definition", c.at("app/src/ratings/WhoApi.fiig", 3, 23)))
	if len(locs) != 1 || locs[0].URI != c.uri("app/src/ratings/WhoApi.fiig") || locs[0].Range.Start.Line != 1 || locs[0].Range.Start.Character != 14 {
		t.Fatalf("definition of me = %+v, want its principal at 2:15", locs)
	}
	h := decode[hover](t, c.request("textDocument/hover", c.at("app/src/ratings/WhoApi.fiig", 3, 23)))
	if !strings.Contains(h.Contents.Value, `principal (who is calling) me: Caller from header "Authorization"`) {
		t.Errorf("hover on me = %q", h.Contents.Value)
	}
	// `by signedIn`: line 1, character 56.
	locs = decode[[]location](t, c.request("textDocument/definition", c.at("app/src/ratings/WhoApi.fiig", 1, 57)))
	if len(locs) != 1 || locs[0].URI != c.uri("app/src/ratings/SignedIn.fiig") {
		t.Fatalf("definition of signedIn = %+v, want SignedIn.fiig", locs)
	}
}
