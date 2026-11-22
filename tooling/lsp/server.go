package lsp

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/format"
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
	"fiig-lang/compiler/version"
)

// debounce is how long the server waits for typing to pause (§3).
var debounce = 150 * time.Millisecond

// Main runs the server on in and out until the client exits.
func Main(in io.Reader, out io.Writer) error {
	return newServer(in, out).run()
}

type server struct {
	conn *conn
	enc  encoding

	mu        sync.Mutex
	workspace string
	pkgs      []pkgInfo
	open      map[string]string // key(path) -> the editor's text
	snaps     map[string]*snapshot
	lastGood  map[string]*checker.Checked // key(path) -> its last check that parsed
	goodText  map[string]string           // key(path) -> the text lastGood is of
	goodSnap  map[string]*snapshot        // key(path) -> the check lastGood came from
	pending   map[string]bool             // package roots waiting to be checked
	timer     *time.Timer
	published map[string]bool // files that have diagnostics showing
	shutdown  bool
}

func newServer(in io.Reader, out io.Writer) *server {
	return &server{conn: newConn(in, out), enc: utf16Enc, open: map[string]string{}, snaps: map[string]*snapshot{},
		lastGood: map[string]*checker.Checked{}, goodText: map[string]string{}, goodSnap: map[string]*snapshot{}, pending: map[string]bool{}, published: map[string]bool{}}
}

func (s *server) run() error {
	for {
		m, err := s.conn.read()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if m.Error != nil {
			s.conn.write(&message{Error: m.Error, ID: nullID()})
			continue
		}
		if m.Method == "exit" {
			return nil
		}
		s.dispatch(m)
	}
}

func nullID() *json.RawMessage {
	raw := json.RawMessage("null")
	return &raw
}

// dispatch answers one message; a panic in a handler is logged and the
// request answered with an error, so the server stays up (§8).
func (s *server) dispatch(m *message) {
	defer func() {
		if r := recover(); r != nil {
			s.log(fmt.Sprintf("fiig lsp: %s failed: %v\n%s", m.Method, r, debug.Stack()))
			if m.ID != nil {
				s.conn.write(&message{ID: m.ID, Error: &rpcError{Code: codeInvalidRequest, Message: fmt.Sprint(r)}})
			}
		}
	}()
	result, rerr := s.handle(m)
	if m.ID == nil {
		return
	}
	if rerr != nil {
		s.conn.write(&message{ID: m.ID, Error: rerr})
		return
	}
	if result == nil {
		result = nullResult{}
	}
	s.conn.write(&message{ID: m.ID, Result: result})
}

func (s *server) log(text string) {
	s.conn.notify("window/logMessage", map[string]any{"type": 1, "message": text})
}

func params[T any](m *message) (T, *rpcError) {
	var p T
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return p, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	return p, nil
}

func (s *server) handle(m *message) (any, *rpcError) {
	switch m.Method {
	case "initialize":
		p, err := params[struct {
			RootURI          string `json:"rootUri"`
			RootPath         string `json:"rootPath"`
			WorkspaceFolders []struct {
				URI string `json:"uri"`
			} `json:"workspaceFolders"`
			Capabilities struct {
				General struct {
					PositionEncodings []string `json:"positionEncodings"`
				} `json:"general"`
			} `json:"capabilities"`
		}](m)
		if err != nil {
			return nil, err
		}
		root := ""
		if len(p.WorkspaceFolders) > 0 {
			root, _ = uriToPath(p.WorkspaceFolders[0].URI)
		} else if p.RootURI != "" {
			root, _ = uriToPath(p.RootURI)
		} else {
			root = p.RootPath
		}
		s.mu.Lock()
		s.enc = chooseEncoding(p.Capabilities.General.PositionEncodings)
		s.workspace = root
		s.mu.Unlock()
		return map[string]any{
			"capabilities": map[string]any{
				"positionEncoding":           string(s.enc),
				"textDocumentSync":           map[string]any{"openClose": true, "change": 1, "save": true},
				"hoverProvider":              true,
				"definitionProvider":         true,
				"referencesProvider":         true,
				"documentFormattingProvider": true,
				"documentSymbolProvider":     true,
				"workspaceSymbolProvider":    true,
				"semanticTokensProvider": map[string]any{
					"legend": map[string]any{"tokenTypes": semanticTokenTypes, "tokenModifiers": []string{}},
					"full":   true,
				},
			},
			"serverInfo": map[string]any{"name": "fiig", "version": version.Version},
		}, nil
	case "initialized":
		s.mu.Lock()
		if s.workspace != "" {
			s.pkgs = discover(s.workspace)
			for _, p := range s.pkgs {
				s.pending[p.id()] = true
			}
		}
		s.mu.Unlock()
		s.analyze()
		return nil, nil
	case "shutdown":
		s.mu.Lock()
		s.shutdown = true
		s.mu.Unlock()
		return nil, nil
	case "textDocument/didOpen":
		p, err := params[struct {
			TextDocument struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"textDocument"`
		}](m)
		if err != nil {
			return nil, err
		}
		s.changed(p.TextDocument.URI, p.TextDocument.Text, true, false)
		return nil, nil
	case "textDocument/didChange":
		p, err := params[struct {
			TextDocument   textDocumentIdentifier `json:"textDocument"`
			ContentChanges []struct {
				Text string `json:"text"`
			} `json:"contentChanges"`
		}](m)
		if err != nil {
			return nil, err
		}
		if n := len(p.ContentChanges); n > 0 {
			s.changed(p.TextDocument.URI, p.ContentChanges[n-1].Text, true, false)
		}
		return nil, nil
	case "textDocument/didSave":
		p, err := params[struct {
			TextDocument textDocumentIdentifier `json:"textDocument"`
		}](m)
		if err != nil {
			return nil, err
		}
		s.changed(p.TextDocument.URI, "", false, true)
		return nil, nil
	case "textDocument/didClose":
		p, err := params[struct {
			TextDocument textDocumentIdentifier `json:"textDocument"`
		}](m)
		if err != nil {
			return nil, err
		}
		if path, ok := uriToPath(p.TextDocument.URI); ok {
			s.mu.Lock()
			delete(s.open, key(path))
			s.mu.Unlock()
			s.changed(p.TextDocument.URI, "", false, true)
		}
		return nil, nil
	case "textDocument/hover":
		p, err := params[textDocumentPositionParams](m)
		if err != nil {
			return nil, err
		}
		return s.hover(p), nil
	case "textDocument/definition":
		p, err := params[textDocumentPositionParams](m)
		if err != nil {
			return nil, err
		}
		if loc, ok := s.definition(p); ok {
			return []location{loc}, nil
		}
		// On a declaration's own name there is nothing to jump to, so go to
		// its users: one is a jump, several are the client's chooser.
		if s.onDeclaration(p) {
			return s.references(p), nil
		}
		return []location{}, nil
	case "textDocument/references":
		p, err := params[textDocumentPositionParams](m)
		if err != nil {
			return nil, err
		}
		return s.references(p), nil
	case "textDocument/formatting":
		p, err := params[struct {
			TextDocument textDocumentIdentifier `json:"textDocument"`
		}](m)
		if err != nil {
			return nil, err
		}
		return s.formatting(p.TextDocument.URI), nil
	case "textDocument/semanticTokens/full":
		p, err := params[struct {
			TextDocument textDocumentIdentifier `json:"textDocument"`
		}](m)
		if err != nil {
			return nil, err
		}
		return s.semanticTokens(p.TextDocument.URI), nil
	case "textDocument/documentSymbol":
		p, err := params[struct {
			TextDocument textDocumentIdentifier `json:"textDocument"`
		}](m)
		if err != nil {
			return nil, err
		}
		return s.documentSymbols(p.TextDocument.URI), nil
	case "workspace/symbol":
		p, err := params[struct {
			Query string `json:"query"`
		}](m)
		if err != nil {
			return nil, err
		}
		return s.workspaceSymbols(p.Query), nil
	}
	if m.ID == nil || strings.HasPrefix(m.Method, "$/") {
		return nil, nil // notifications the server doesn't use
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "fiig lsp has no method " + m.Method}
}

// changed records a document's text (or forgets it) and schedules its
// package for a check after the debounce (§3).
func (s *server) changed(uri, text string, opened, fromDisk bool) {
	path, ok := uriToPath(uri)
	if !ok {
		return
	}
	s.mu.Lock()
	if opened {
		s.open[key(path)] = text
	}
	p, found := owner(s.pkgs, path)
	if !found {
		p = pkgInfo{root: filepath.Dir(path), loose: true}
		s.pkgs = append(s.pkgs, p)
	}
	s.pending[p.id()] = true
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(debounce, s.analyze)
	s.mu.Unlock()
}

// analyze checks every pending package, then the packages that depend on
// one that changed, and publishes their diagnostics.
func (s *server) analyze() {
	defer func() {
		if r := recover(); r != nil {
			s.log(fmt.Sprintf("fiig lsp: a check failed: %v\n%s", r, debug.Stack()))
		}
	}()
	s.mu.Lock()
	var todo []pkgInfo
	for _, p := range s.pkgs {
		if s.pending[p.id()] {
			todo = append(todo, p)
		}
	}
	s.pending = map[string]bool{}
	open := map[string]string{}
	for k, v := range s.open {
		open[k] = v
	}
	workspace := s.workspace
	s.mu.Unlock()
	lookup := func(path string) (string, bool) {
		text, ok := open[key(path)]
		return text, ok
	}
	done := map[string]bool{}
	var changedPaths []string
	for len(todo) > 0 {
		p := todo[0]
		todo = todo[1:]
		if done[p.id()] {
			continue
		}
		done[p.id()] = true
		snap := check(p, workspace, lookup)
		s.mu.Lock()
		s.snaps[key(p.id())] = snap
		for path, c := range snap.checked {
			if c.ParseErrors == 0 {
				s.lastGood[key(path)] = c
				s.goodText[key(path)] = snap.sources[path]
				s.goodSnap[key(path)] = snap
			}
		}
		// Dependents of a package that changed check again (§3).
		if snap.path != "" {
			changedPaths = append(changedPaths, snap.path)
			for _, other := range s.snaps {
				if done[other.pkg.id()] {
					continue
				}
				for _, dep := range other.depPaths {
					if dep == snap.path {
						todo = append(todo, other.pkg)
					}
				}
			}
		}
		s.mu.Unlock()
		s.publish(snap)
	}
}

func (s *server) publish(snap *snapshot) {
	paths := make([]string, 0, len(snap.diags))
	for p := range snap.diags {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		diags := snap.diags[path]
		src := snap.sources[path]
		out := make([]lspDiagnostic, 0, len(diags))
		for _, d := range diags {
			ld := lspDiagnostic{Severity: diagnosticKind, Source: "fiig", Message: d.Msg, Code: d.Rule}
			if d.At.Line > 0 {
				ld.Range = rangeOf(src, d.At, d.End, s.enc)
			}
			if d.Rule != "" {
				ld.CodeDescription = &codeDescription{Href: "https://github.com/ElephantRyan/fiig-lang/blob/main/docs/specification/bug-classes.md"}
			}
			out = append(out, ld)
		}
		s.mu.Lock()
		was := s.published[key(path)]
		s.published[key(path)] = len(out) > 0
		s.mu.Unlock()
		if len(out) == 0 && !was {
			continue
		}
		s.conn.notify("textDocument/publishDiagnostics", publishDiagnosticsParams{URI: pathToURI(path), Diagnostics: out})
	}
}

// at is what the server knows about a position: the file's text and the
// check its answers come from, the last one in which the file parsed.
type at struct {
	path    string
	src     string
	checked *checker.Checked
	snap    *snapshot
	offset  int
}

func (s *server) at(p textDocumentPositionParams) (at, bool) {
	path, ok := uriToPath(p.TextDocument.URI)
	if !ok {
		return at{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.lastGood[key(path)]
	if !ok {
		return at{}, false
	}
	src := s.goodText[key(path)]
	// The names in c resolve in the check that produced it, not
	// necessarily the latest one.
	snap := s.goodSnap[key(path)]
	// The position is in the editor's text; when the last good check is of
	// older text, it still answers, as near as the lines allow.
	text := src
	if live, ok := s.open[key(path)]; ok {
		text = live
	}
	offset := fromPosition(text, p.Position, s.enc)
	if text != src {
		offset = fromPosition(src, toPosition(text, offset, s.enc), s.enc)
	}
	return at{path: path, src: src, checked: c, snap: snap, offset: offset}, snap != nil
}

// refAt is the resolved name under the position.
func refAt(c *checker.Checked, offset int) (checker.Ref, bool) {
	var best checker.Ref
	found := false
	for _, r := range c.Index.Refs {
		if r.Start.Offset <= offset && offset <= r.End.Offset {
			if !found || r.End.Offset-r.Start.Offset < best.End.Offset-best.Start.Offset {
				best, found = r, true
			}
		}
	}
	return best, found
}

// target is a declaration's identity, or with member, one of its fields,
// variants, or gateways.
type target struct {
	id     declID
	member string
}

func (s *server) targetOf(snap *snapshot, r checker.Ref) (target, bool) {
	id, ok := snap.ids[r.Decl]
	return target{id, r.Member}, ok
}

// locate is where t is declared.
func (s *server) locate(t target) (location, bool) {
	s.mu.Lock()
	var st site
	var src string
	found := false
	for _, snap := range s.snaps {
		if own, ok := snap.own[t.id]; ok {
			st, src, found = own, snap.sources[own.path], true
			break
		}
	}
	var depDir string
	if !found {
		for _, snap := range s.snaps {
			if dir, ok := snap.depDirs[t.id.Pkg]; ok {
				depDir = dir
				break
			}
		}
	}
	s.mu.Unlock()
	if !found {
		if depDir == "" {
			return location{}, false
		}
		path, decl, text, ok := declIn(depDir, t.id.Name)
		if !ok {
			return location{}, false
		}
		st, src = site{path, decl}, text
	}
	start, _ := st.decl.(syntax.Spanned).Bounds()
	ns, ne := nameAt(src, start, syntax.DeclName(st.decl))
	if t.member != "" {
		if ms, me, ok := memberSpan(src, st.decl, t.member); ok {
			ns, ne = ms, me
		}
	}
	return location{URI: pathToURI(st.path), Range: rangeOf(src, ns, ne, s.enc)}, true
}

func (s *server) definition(p textDocumentPositionParams) (location, bool) {
	a, ok := s.at(p)
	if !ok {
		return location{}, false
	}
	if r, ok := refAt(a.checked, a.offset); ok {
		if t, ok := s.targetOf(a.snap, r); ok {
			return s.locate(t)
		}
		return location{}, false
	}
	if start, end, ok := localDefinition(a.checked.File, a.src, a.offset); ok {
		return location{URI: pathToURI(a.path), Range: rangeOf(a.src, start, end, s.enc)}, true
	}
	return location{}, false
}

func (s *server) onDeclaration(p textDocumentPositionParams) bool {
	a, ok := s.at(p)
	if !ok {
		return false
	}
	if _, ok := refAt(a.checked, a.offset); ok {
		return false
	}
	_, _, ok = declUnder(a.checked.File, a.src, a.offset)
	return ok
}

func (s *server) references(p textDocumentPositionParams) []location {
	a, ok := s.at(p)
	if !ok {
		return []location{}
	}
	var t target
	if r, ok := refAt(a.checked, a.offset); ok {
		if t, ok = s.targetOf(a.snap, r); !ok {
			return []location{}
		}
	} else if d, member, ok := declUnder(a.checked.File, a.src, a.offset); ok {
		t = target{a.snap.ids[d], member}
	} else {
		return []location{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []location{}
	seen := map[location]bool{}
	for _, snap := range s.snaps {
		for path, c := range snap.checked {
			if c.Index == nil {
				continue
			}
			for _, r := range c.Index.Refs {
				if id, ok := snap.ids[r.Decl]; ok && id == t.id && r.Member == t.member {
					loc := location{URI: pathToURI(path), Range: rangeOf(snap.sources[path], r.Start, r.End, s.enc)}
					if !seen[loc] {
						seen[loc] = true
						out = append(out, loc)
					}
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].URI != out[j].URI {
			return out[i].URI < out[j].URI
		}
		return out[i].Range.Start.Line < out[j].Range.Start.Line || out[i].Range.Start.Line == out[j].Range.Start.Line && out[i].Range.Start.Character < out[j].Range.Start.Character
	})
	return out
}

func (s *server) hover(p textDocumentPositionParams) any {
	a, ok := s.at(p)
	if !ok {
		return nil
	}
	if r, ok := refAt(a.checked, a.offset); ok {
		text := describe(r.Decl, r.Member)
		if t, ok := s.targetOf(a.snap, r); ok && r.Member == "" {
			if doc := s.docComment(t); doc != "" {
				text = doc + "\n\n" + text
			}
		}
		rg := rangeOf(a.src, r.Start, r.End, s.enc)
		return hover{Contents: markupContent{Kind: "markdown", Value: "```fiig\n" + text + "\n```"}, Range: &rg}
	}
	var best checker.TypeAt
	found := false
	for _, t := range a.checked.Index.Types {
		if t.Start.Offset <= a.offset && a.offset <= t.End.Offset {
			if !found || t.End.Offset-t.Start.Offset < best.End.Offset-best.Start.Offset {
				best, found = t, true
			}
		}
	}
	if !found {
		return nil
	}
	if _, unknown := best.Type.(types.Unknown); unknown {
		return nil
	}
	rg := rangeOf(a.src, best.Start, best.End, s.enc)
	expr := strings.TrimSpace(a.src[best.Start.Offset:min(best.End.Offset, len(a.src))])
	if len(expr) > 60 || strings.Contains(expr, "\n") {
		expr = ""
	} else {
		expr += ": "
	}
	return hover{Contents: markupContent{Kind: "markdown", Value: "```fiig\n" + expr + best.Type.String() + "\n```"}, Range: &rg}
}

// docComment is the `//` comment directly above a declaration.
func (s *server) docComment(t target) string {
	s.mu.Lock()
	var st site
	var c *checker.Checked
	var src string
	for _, snap := range s.snaps {
		if own, ok := snap.own[t.id]; ok {
			st, c, src = own, snap.checked[own.path], snap.sources[own.path]
			break
		}
	}
	s.mu.Unlock()
	if c == nil {
		return ""
	}
	start, _ := st.decl.(syntax.Spanned).Bounds()
	var lines []string
	want := start.Line - 1
	for i := len(c.Comments) - 1; i >= 0; i-- {
		cm := c.Comments[i]
		if cm.Start.Line > want {
			continue
		}
		if cm.Start.Line != want {
			break
		}
		lines = append([]string{cm.Text}, lines...)
		want--
	}
	_ = src
	return strings.Join(lines, "\n")
}

func (s *server) formatting(uri string) []textEdit {
	path, ok := uriToPath(uri)
	if !ok {
		return []textEdit{}
	}
	s.mu.Lock()
	src, isOpen := s.open[key(path)]
	enc := s.enc
	s.mu.Unlock()
	if !isOpen {
		data, err := os.ReadFile(path)
		if err != nil {
			return []textEdit{}
		}
		src = string(data)
	}
	formatted, errs := format.Format(src)
	if len(errs) > 0 || formatted == src {
		return []textEdit{}
	}
	end := toPosition(src, len(src), enc)
	return []textEdit{{Range: lspRange{End: end}, NewText: formatted}}
}

func (s *server) documentSymbols(uri string) []documentSymbol {
	path, ok := uriToPath(uri)
	if !ok {
		return []documentSymbol{}
	}
	s.mu.Lock()
	c, ok := s.lastGood[key(path)]
	src := s.goodText[key(path)]
	enc := s.enc
	s.mu.Unlock()
	if !ok {
		return []documentSymbol{}
	}
	out := []documentSymbol{}
	add := func(d syntax.Decl) {
		out = append(out, symbolOf(src, d, enc))
	}
	for _, d := range c.File.Decls {
		add(d)
	}
	if c.File.Private != nil {
		for _, d := range c.File.Private.Members {
			add(d)
		}
	}
	return out
}

func (s *server) workspaceSymbols(query string) []symbolInformation {
	q := strings.ToLower(query)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []symbolInformation{}
	for _, snap := range s.snaps {
		for path, c := range snap.checked {
			for _, d := range c.File.Decls {
				name := syntax.DeclName(d)
				if q != "" && !strings.Contains(strings.ToLower(name), q) {
					continue
				}
				start, _ := d.(syntax.Spanned).Bounds()
				ns, ne := nameAt(snap.sources[path], start, name)
				out = append(out, symbolInformation{Name: name, Kind: symbolKind(d),
					Location:      location{URI: pathToURI(path), Range: rangeOf(snap.sources[path], ns, ne, s.enc)},
					ContainerName: snap.path})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func symbolKind(d syntax.Decl) int {
	switch d.(type) {
	case *syntax.ModelDecl:
		return symStruct
	case *syntax.EnumDecl:
		return symEnum
	case *syntax.InterfaceDecl:
		return symInterface
	case *syntax.CapabilityDecl, *syntax.FormulasDecl, *syntax.AdapterDecl:
		return symModule
	case *syntax.ConfigDecl:
		return symConstant
	case *syntax.GatewayDecl:
		return symMethod
	}
	return symFunction
}

func symbolOf(src string, d syntax.Decl, enc encoding) documentSymbol {
	start, end := d.(syntax.Spanned).Bounds()
	name := syntax.DeclName(d)
	ns, ne := nameAt(src, start, name)
	sym := documentSymbol{Name: name, Detail: syntax.DeclKindName(d), Kind: symbolKind(d),
		Range: rangeOf(src, start, end, enc), SelectionRange: rangeOf(src, ns, ne, enc)}
	child := func(name string, kind int, at lexer.Pos, endAt lexer.Pos) {
		cs, ce := nameAt(src, at, name)
		sym.Children = append(sym.Children, documentSymbol{Name: name, Kind: kind,
			Range: rangeOf(src, at, endAt, enc), SelectionRange: rangeOf(src, cs, ce, enc)})
	}
	switch d := d.(type) {
	case *syntax.ModelDecl:
		for _, f := range d.Fields {
			child(f.Name, symField, f.Start, f.End)
		}
	case *syntax.EnumDecl:
		for _, v := range d.Variants {
			child(v.Name, symEnumMember, v.Start, v.End)
		}
	case *syntax.CapabilityDecl:
		for _, g := range d.Gateways {
			child(g.Name, symMethod, g.Start, g.End)
		}
	case *syntax.FormulasDecl:
		for _, f := range d.Members {
			child(f.Name, symFunction, f.Start, f.End)
		}
	}
	return sym
}

// describe is a declaration, or one member of it, as hover shows it,
// written from its syntax so a declaration of any package renders the
// same way.
func describe(d syntax.Decl, member string) string {
	ty := func(t syntax.Type) string {
		if t == nil {
			return ""
		}
		return types.SyntaxString(t)
	}
	sig := func(kind, name string, ps []syntax.Param, ret syntax.Type) string {
		parts := make([]string, len(ps))
		for i, p := range ps {
			parts[i] = p.Name + ": " + ty(p.Type)
		}
		out := kind + " " + name + "(" + strings.Join(parts, ", ") + ")"
		if ret != nil {
			out += " -> " + ty(ret)
		}
		return out
	}
	fields := func(fs []syntax.Field) string {
		parts := make([]string, len(fs))
		for i, f := range fs {
			if f.Embedded {
				parts[i] = ty(f.Type)
			} else {
				parts[i] = f.Name + ": " + ty(f.Type)
			}
		}
		return strings.Join(parts, ", ")
	}
	switch d := d.(type) {
	case *syntax.ModelDecl:
		if member != "" {
			for _, f := range d.AllFields() {
				if f.Name == member {
					return d.Name + "." + f.Name + ": " + ty(f.Type)
				}
			}
		}
		lines := []string{"model " + d.Name + " {"}
		for _, f := range d.Fields {
			if f.Embedded {
				lines = append(lines, "    "+ty(f.Type))
			} else {
				lines = append(lines, "    "+f.Name+": "+ty(f.Type))
			}
		}
		return strings.Join(append(lines, "}"), "\n")
	case *syntax.EnumDecl:
		variant := func(v syntax.EnumVariant) string {
			if len(v.Fields) == 0 {
				return v.Name
			}
			return v.Name + "(" + fields(v.Fields) + ")"
		}
		if member != "" {
			if v, ok := d.Variant(member); ok {
				return d.Name + "." + variant(v)
			}
		}
		lines := []string{"enum " + d.Name + " {"}
		for _, v := range d.Variants {
			lines = append(lines, "    "+variant(v))
		}
		return strings.Join(append(lines, "}"), "\n")
	case *syntax.RuleDecl:
		return sig("rule", d.Name, d.Params, d.ReturnType)
	case *syntax.ConversionDecl:
		return sig("conversion", d.Name, d.Params, d.ReturnType)
	case *syntax.ActivityDecl:
		return sig("activity", d.Name, d.Params, d.ReturnType)
	case *syntax.WorkflowDecl:
		return sig("workflow", d.Name, d.Params, d.ReturnType)
	case *syntax.HelperDecl:
		return sig("private", d.Name, d.Params, d.ReturnType)
	case *syntax.GatewayDecl:
		out := sig("gateway", d.Name, d.Params, d.ReturnType)
		if d.Requires != "" {
			out += "\n    requires " + d.Requires
		}
		if d.Idempotent {
			out += "\n    idempotent"
		}
		return out
	case *syntax.CapabilityDecl:
		if member != "" {
			if g := d.Gateway(member); g != nil {
				return d.Name + "." + describe(g, "")
			}
		}
		lines := []string{"capability " + d.Name + " {"}
		for _, g := range d.Gateways {
			lines = append(lines, "    "+strings.ReplaceAll(describe(g, ""), "\n", "\n    "))
		}
		return strings.Join(append(lines, "}"), "\n")
	case *syntax.FormulasDecl:
		if member != "" {
			if f := d.Member(member); f != nil {
				return d.Name + "." + sig("formula", f.Name, f.Params, f.ReturnType)
			}
		}
		return "formulas " + d.Name
	case *syntax.AdapterDecl:
		if pr := d.Principal(member); pr != nil {
			from := pr.Source + " " + strconv.Quote(pr.SourceName)
			if pr.Scheme != "" {
				from += " scheme " + strconv.Quote(pr.Scheme)
			}
			if pr.From != nil {
				from = pr.From.Name
			}
			out := "wire (who is calling) " + pr.Name + ": " + ty(pr.Type) + " from " + from
			if pr.By != nil && pr.Else != nil {
				out += "\n    by " + pr.By.Name() + " else " + pr.Else.Name()
			}
			return out
		}
	}
	return syntax.DeclKindName(d) + " " + syntax.DeclName(d)
}
