// Package registry generates compact, machine-readable contract cards from
// parsed declarations (fiig-lang.md §3.11, Milestone 3's "Package catalog
// generation"). This works directly off the parser's AST, not the
// Milestone 2 checker: every field here is mechanically derivable from a
// parsed declaration alone (or, for Dependencies, a cheap name-level scan
// of its body — see graph.go), matching the "no additional mandatory
// export metadata" line ADR 0007 draws between auto-derived fields and
// authored ones (intent, examples) that this package doesn't attempt yet —
// there's no comment-preservation in the lexer to source them from. Using
// the checker's actual resolved types instead of this package's own
// lighter-weight AST walk is a possible future upgrade, not done here.
//
// Dependency and caller graph (Milestone 3): see graph.go for
// Dependencies (stored per-card, built from an AST walk of the
// declaration's own body) and Callers (computed on demand from the whole
// card set, the same treatment Siblings already gets).
package registry

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fiig-lang/compiler/format"
	"fiig-lang/compiler/manifest"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// Catalog is which of the two searchable catalogs a card belongs to
// (docs/decisions/0008-separate-composition-catalog.md): Default holds the
// composable building blocks (models, values, enums, rules, conversions,
// activities, gateways) that a workflow is assembled from; Composition holds workflows and
// adapters, searched deliberately rather than by default.
type Catalog string

const (
	Default     Catalog = "default"
	Composition Catalog = "composition"
)

// Param is a name/type pair shared by parameters, model fields, and enum
// variant fields — all three are the same shape in the AST.
type Param struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// PromotedFrom is, for a model field that came in through embedding
	// (ADR 0045), the chain of embedded models it was promoted through
	// ("Address" or "ContactInfo.Address"). Without it, two models embedding
	// the same block would look like a coincidence of field names.
	PromotedFrom string `json:"promotedFrom,omitempty"`
	// Key marks an enum header field with a derived lookup (ADR 0046 §5).
	Key bool `json:"key,omitempty"`
}

// Constant is one value a headed enum's variant supplies (ADR 0046 §4),
// rendered as the formatter prints it, so the catalog can answer "which
// source has code ACME" without reading source.
type Constant struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	// Elements is each element of a list constant, in the same spelling,
	// so `catalog find --constant aliases=ACMEINC` matches one alias the
	// way DeductionSource(aliases: raw) does (ADR 0046 §7).
	Elements []string `json:"elements,omitempty"`
}

// Variant is one case of an enum declaration.
type Variant struct {
	Name      string     `json:"name"`
	Fields    []Param    `json:"fields,omitempty"`
	Constants []Constant `json:"constants,omitempty"` // a headed enum's row (ADR 0046 §4)
}

// Card is a compact contract card for one exported declaration
// (fiig-lang.md §3.11). Every field is derived from the AST alone; see the
// package doc for what's deliberately left out until the checker exists.
type Card struct {
	Name    string  `json:"name"`
	Kind    string  `json:"kind"` // model | value | enum | rule | conversion | activity | gateway | workflow | adapter | interface | capability | formula | constant (a prelude math constant)
	Catalog Catalog `json:"catalog"`
	Source  string  `json:"source"` // file path relative to the registry root, forward-slashed

	// Package and PackageVersion are the canonical package path and
	// resolved version this card belongs to (docs/terminology.md's
	// "Canonical identity": "a declaration's true package/version/symbol
	// identity ... as opposed to any local alias a consuming file uses"),
	// set by Merge when combining cards across more than one package.
	// Both are "" for a single-package Generate call with no package
	// context — unchanged from before Milestone 6's loader existed.
	Package        string `json:"package,omitempty"`
	PackageVersion string `json:"packageVersion,omitempty"`

	// Group is, on a formula card, the formulas group it belongs to (ADR
	// 0049): a formula gets a card of its own, named `group.member` as its
	// callers write it, so `catalog find magnitude` finds it.
	Group string `json:"group,omitempty"`

	// Deprecated is set when root's own fiig.pkg.json (compiler/manifest)
	// declares this exact name deprecated — Milestone 6's "deprecation
	// metadata" deliverable. nil for every card otherwise, including
	// every package with no manifest at all (Generate degrades
	// gracefully, same as every other manifest-derived field added
	// across Milestone 6).
	Deprecated *manifest.Deprecation `json:"deprecated,omitempty"`

	Params  []Param `json:"params,omitempty"`
	Returns string  `json:"returns,omitempty"`
	Error   string  `json:"error,omitempty"` // the E in a Result<S, E> Returns, when present
	// Forms is every signature of a prelude math formula with more than
	// one, an int form and a float form (ADR 0049 §2.4); Params and Returns
	// then hold the first. Nothing written in fiig has more than one.
	Forms []Operation `json:"forms,omitempty"`

	Requires   string `json:"requires,omitempty"`   // gateway's capability (grammar.md §4)
	Idempotent bool   `json:"idempotent,omitempty"` // gateway only (ADR 0014)
	Pure       bool   `json:"pure,omitempty"`       // rule/conversion always; an activity when everything it reaches is pure (ADR 0044, derived in Generate) — §3.11 lists purity as its own contract-card field

	Fields   []Param   `json:"fields,omitempty"`   // model fields
	Header   []Param   `json:"header,omitempty"`   // enum header fields (ADR 0046 §4)
	Variants []Variant `json:"variants,omitempty"` // enum variants

	AdapterKind string   `json:"adapterKind,omitempty"` // "http" | "cli"
	Routes      []string `json:"routes,omitempty"`      // e.g. "GET /customers/{id}"

	// Operations is an interface's members, or a capability group's
	// gateways (ADR 0043) — the contract either one presents to callers.
	Operations []Operation `json:"operations,omitempty"`
	// Provides is, on a capability card, every interface its package's
	// manifest says it fulfills; Fulfillers is the inverse, on an interface
	// card, for the groups in the same build. Both are computed from the
	// manifest, never declared in source (ADR 0043 §2).
	Provides   []string `json:"provides,omitempty"`
	Fulfillers []string `json:"fulfillers,omitempty"`
	// Uses is every capability operation a declaration's body calls directly,
	// as "Name.op" — a capability group's, a primitive's, or a provider
	// binding's (ADR 0043 §6: exactly the `Upper.lower(...)` calls). An
	// activity that uses one is not pure.
	Uses []string `json:"uses,omitempty"`

	// Dependencies is every other rule/conversion/activity/gateway/workflow this
	// declaration calls — the Milestone 3 "dependency ... graph" deliverable.
	// Populated at BuildCard time from the declaration's own body (or, for
	// an adapter, its routes), then narrowed by Generate to names that
	// actually resolve to a callable card elsewhere in the same build — a
	// capability call (Database.queryOne(...)), a model/enum
	// construction, and the Ok/Err/copy prelude forms are never recorded in
	// the first place (see collectExprCallNames), but an ordinary call to a
	// declaration that simply doesn't exist in this build is also dropped
	// here rather than kept as a dangling, unverifiable edge. Callers (the
	// inverse) is computed on demand from this field — see Callers below —
	// the same relationship Siblings already models for signatures: a
	// cross-card fact, not something redundantly stored on every card.
	Dependencies []string `json:"dependencies,omitempty"`
}

// Operation is one operation on an interface or capability card.
type Operation struct {
	Name       string  `json:"name"`
	Params     []Param `json:"params,omitempty"`
	Returns    string  `json:"returns"`
	Error      string  `json:"error,omitempty"`
	Requires   string  `json:"requires,omitempty"`
	Idempotent bool    `json:"idempotent,omitempty"`
}

// BuildCards builds the cards one declaration contributes: one for every
// kind but a formulas group, which contributes one per member (ADR 0049),
// since a formula, not its group, is what a caller looks for and calls.
func BuildCards(decl syntax.Decl, source string) []*Card {
	if d, ok := decl.(*syntax.FormulasDecl); ok {
		cards := make([]*Card, len(d.Members))
		for i, f := range d.Members {
			cards[i] = &Card{
				Name: d.Name + "." + f.Name, Kind: "formula", Group: d.Name, Catalog: Default, Source: source,
				Params: paramParams(f.Params), Returns: typeString(f.ReturnType), Pure: true,
				Dependencies: siblingCalls(d, f),
			}
		}
		return cards
	}
	if c := BuildCard(decl, source); c != nil {
		return []*Card{c}
	}
	return nil
}

// siblingCalls is the members of f's own group that f calls, by card name.
// A formula calls only formulas, and a call into another group is qualified,
// which the name-level scan doesn't record for any kind.
func siblingCalls(group *syntax.FormulasDecl, f *syntax.FormulaDecl) []string {
	if f.Body == nil {
		return nil
	}
	var out []string
	for _, name := range syntax.BodyCallNames(f.Body) {
		if group.Member(name) != nil {
			out = append(out, group.Name+"."+name)
		}
	}
	return out
}

// BuildCard builds one Card from a declaration. source is the card's
// recorded Source (fiig-lang.md §3.11's "Source location"), typically a
// path relative to the registry root. Returns nil for a declaration kind
// the registry doesn't track as a single card: a formulas group, whose
// members BuildCards turns into cards of their own.
func BuildCard(decl syntax.Decl, source string) *Card {
	switch d := decl.(type) {
	case *syntax.ModelDecl:
		return &Card{Name: d.Name, Kind: "model", Catalog: Default, Source: source, Fields: modelFieldParams(d)}
	case *syntax.EnumDecl:
		return &Card{Name: d.Name, Kind: "enum", Catalog: Default, Source: source, Header: headerParams(d.Header), Variants: variantsOf(d)}
	case *syntax.RuleDecl:
		return &Card{Name: d.Name, Kind: "rule", Catalog: Default, Source: source, Params: paramParams(d.Params), Returns: typeString(d.ReturnType), Error: errorTypeOf(d.ReturnType), Pure: true, Dependencies: bodyCallNames(d.Body)}
	case *syntax.ConversionDecl:
		return &Card{Name: d.Name, Kind: "conversion", Catalog: Default, Source: source, Params: paramParams(d.Params), Returns: typeString(d.ReturnType), Error: errorTypeOf(d.ReturnType), Pure: true, Dependencies: bodyCallNames(d.Body)}
	case *syntax.ActivityDecl:
		// Pure is derived, not stipulated (ADR 0044): Generate computes it
		// from the activity's callees once the whole build's cards exist.
		return &Card{Name: d.Name, Kind: "activity", Catalog: Default, Source: source, Params: paramParams(d.Params), Returns: typeString(d.ReturnType), Error: errorTypeOf(d.ReturnType), Dependencies: bodyCallNames(d.Body), Uses: syntax.BodyCapabilityCalls(d.Body)}
	case *syntax.GatewayDecl:
		return &Card{
			Name: d.Name, Kind: "gateway", Catalog: Default, Source: source,
			Params: paramParams(d.Params), Returns: typeString(d.ReturnType), Error: errorTypeOf(d.ReturnType),
			Requires: d.Requires, Idempotent: d.Idempotent, Dependencies: bodyCallNames(d.Body), Uses: syntax.BodyCapabilityCalls(d.Body),
		}
	case *syntax.WorkflowDecl:
		return &Card{Name: d.Name, Kind: "workflow", Catalog: Composition, Source: source, Params: paramParams(d.Params), Returns: typeString(d.ReturnType), Error: errorTypeOf(d.ReturnType), Dependencies: bodyCallNames(d.Body), Uses: syntax.BodyCapabilityCalls(d.Body)}
	case *syntax.AdapterDecl:
		return &Card{Name: d.Name, Kind: "adapter", Catalog: Composition, Source: source, AdapterKind: string(d.Kind), Routes: routeSummaries(d.Routes), Dependencies: adapterCallNames(d)}
	case *syntax.InterfaceDecl:
		ops := make([]Operation, len(d.Members))
		for i, m := range d.Members {
			ops[i] = Operation{Name: m.Name, Params: paramParams(m.Params), Returns: typeString(m.ReturnType), Error: errorTypeOf(m.ReturnType)}
		}
		return &Card{Name: d.Name, Kind: "interface", Catalog: Default, Source: source, Operations: ops}
	case *syntax.CapabilityDecl:
		ops := make([]Operation, len(d.Gateways))
		uses := map[string]bool{}
		var deps []string
		for i, g := range d.Gateways {
			ops[i] = Operation{Name: g.Name, Params: paramParams(g.Params), Returns: typeString(g.ReturnType), Error: errorTypeOf(g.ReturnType), Requires: g.Requires, Idempotent: g.Idempotent}
			for _, u := range syntax.BodyCapabilityCalls(g.Body) {
				uses[u] = true
			}
			deps = append(deps, bodyCallNames(g.Body)...)
		}
		var usesList []string
		for u := range uses {
			usesList = append(usesList, u)
		}
		sort.Strings(usesList)
		return &Card{Name: d.Name, Kind: "capability", Catalog: Default, Source: source, Operations: ops, Uses: usesList, Dependencies: dedupe(deps)}
	default:
		return nil
	}
}

// Generate parses every .fiig file under root and returns one Card per
// declaration, sorted by name then source so output is stable across runs
// (Milestone 3 exit criteria: "Registry output remains stable across
// identical builds"). Parse errors are collected rather than aborting the
// walk, so one malformed file doesn't hide the rest of the registry.
func Generate(root string) ([]*Card, []FileError, error) {
	var cards []*Card
	var fileErrs []FileError
	type parsedFile struct {
		rel  string
		file *syntax.File
	}
	var parsed []parsedFile

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".fiig" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		file, errs := parser.Parse(string(src))
		if len(errs) > 0 {
			fileErrs = append(fileErrs, FileError{Source: rel, Errors: errs})
		}
		parsed = append(parsed, parsedFile{rel, file})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// Every file is parsed before any card is built: a model card lists its
	// flattened fields (ADR 0045), and flattening needs every model in the
	// build to be visible, whichever file declares it.
	files := make([]*syntax.File, len(parsed))
	for i, pf := range parsed {
		files[i] = pf.file
	}
	types.BuildUniverse(files)
	for _, pf := range parsed {
		for i, decl := range pf.file.Decls {
			for _, c := range BuildCards(decl, pf.rel) {
				// Private members get no card of their own (ADR 0054 §3), but
				// what their helpers call is what the public entry calls.
				if i == 0 && pf.file.Private != nil {
					c.Dependencies = syntax.FileCallNames(pf.file)
				}
				cards = append(cards, c)
			}
		}
	}

	sort.Slice(cards, func(i, j int) bool {
		if cards[i].Name != cards[j].Name {
			return cards[i].Name < cards[j].Name
		}
		return cards[i].Source < cards[j].Source
	})

	// A manifest is optional (most of the examples/ corpus has none at
	// all) — Load failing just means nothing here is deprecated, same
	// graceful degradation every other manifest-derived field in this
	// package already uses.
	if m, errs := manifest.Load(root); len(errs) == 0 {
		for _, c := range cards {
			if dep, ok := m.Deprecations[c.Name]; ok {
				c.Deprecated = &dep
			}
		}
		linkProvides(cards, m.Path, m.Provides)
	}

	callable := map[string]bool{}
	for _, c := range cards {
		switch c.Kind {
		case "rule", "conversion", "activity", "gateway", "workflow", "formula":
			callable[c.Name] = true
		}
	}
	for _, c := range cards {
		c.Dependencies = filterKnown(c.Dependencies, callable)
	}
	derivePurity(cards)

	return cards, fileErrs, nil
}

// linkProvides fills a capability card's Provides from its package's
// manifest, and, for an interface declared in the same package, the
// interface card's Fulfillers (ADR 0043 §2).
func linkProvides(cards []*Card, path string, provides map[string]string) {
	byKind := map[string]*Card{}
	for _, c := range cards {
		byKind[c.Kind+" "+c.Name] = c
	}
	keys := make([]string, 0, len(provides))
	for k := range provides {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, iface := range keys {
		group := provides[iface]
		if c := byKind["capability "+group]; c != nil {
			c.Provides = append(c.Provides, iface)
		}
		if strings.HasPrefix(iface, path+".") {
			if c := byKind["interface "+strings.TrimPrefix(iface, path+".")]; c != nil {
				c.Fulfillers = append(c.Fulfillers, group)
			}
		}
	}
}

func dedupe(names []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// derivePurity sets Pure on every activity card (ADR 0044): an activity is
// pure exactly when everything it reaches is a rule, a conversion, or
// another pure activity, and it uses no capability itself (ADR 0043: an
// activity may now call a capability group or a provider directly). Rules and conversions are pure by construction; a
// gateway, a workflow, or an unresolvable callee is not. A call cycle
// (itself a checker error, B9) is treated as impure rather than looping.
func derivePurity(cards []*Card) {
	byName := map[string]*Card{}
	for _, c := range cards {
		byName[c.Name] = c
	}
	state := map[string]int{} // 0 unvisited, 1 in progress, 2 done
	var pure func(c *Card) bool
	pure = func(c *Card) bool {
		if c.Kind != "activity" {
			return c.Kind == "rule" || c.Kind == "conversion" || c.Kind == "formula"
		}
		switch state[c.Name] {
		case 1:
			return false
		case 2:
			return c.Pure
		}
		state[c.Name] = 1
		ok := len(c.Uses) == 0
		for _, dep := range c.Dependencies {
			if callee := byName[dep]; callee == nil || !pure(callee) {
				ok = false
			}
		}
		c.Pure = ok
		state[c.Name] = 2
		return ok
	}
	for _, c := range cards {
		if c.Kind == "activity" {
			pure(c)
		}
	}
}

// filterKnown narrows names to the ones known — a dependency candidate
// collected from one declaration's own body, in isolation, can't yet tell
// a call to a real rule/conversion/activity/gateway/workflow apart from a call to
// something that doesn't exist in this build at all (or, Milestone 6,
// lives in an imported package this registry doesn't see). Silently
// dropping the latter matches this package's existing "mechanically
// derivable, not guessed at" policy, same as everywhere else here.
func filterKnown(names []string, known map[string]bool) []string {
	var out []string
	for _, n := range names {
		if known[n] {
			out = append(out, n)
		}
	}
	return out
}

// FileError collects the parse errors found in one source file during
// Generate, so a caller can report them without losing the rest of the
// registry.
type FileError struct {
	Source string         `json:"source"`
	Errors []parser.Error `json:"errors"`
}

// modelFieldParams lists a model's flattened fields, recording where each
// promoted one came from.
func modelFieldParams(m *syntax.ModelDecl) []Param {
	if m.Flat == nil {
		return fieldParams(m.AllFields())
	}
	out := make([]Param, len(m.Flat))
	for i, f := range m.Flat {
		out[i] = Param{Name: f.Name, Type: typeString(f.Type), PromotedFrom: strings.Join(f.Via, ".")}
	}
	return out
}

func fieldParams(fields []syntax.Field) []Param {
	out := make([]Param, len(fields))
	for i, f := range fields {
		out[i] = Param{Name: f.Name, Type: typeString(f.Type)}
	}
	return out
}

func paramParams(params []syntax.Param) []Param {
	out := make([]Param, len(params))
	for i, p := range params {
		out[i] = Param{Name: p.Name, Type: typeString(p.Type)}
	}
	return out
}

func variantsOf(d *syntax.EnumDecl) []Variant {
	out := make([]Variant, len(d.Variants))
	for i, v := range d.Variants {
		out[i] = Variant{Name: v.Name, Fields: fieldParams(v.Fields)}
		// In header order, not as written, so two cards compare field by field.
		for _, f := range d.Header {
			if e, ok := v.Value(f.Name); ok {
				c := Constant{Name: f.Name, Value: format.Expr(e)}
				if list, isList := e.(*syntax.ListLit); isList {
					for _, el := range list.Elems {
						c.Elements = append(c.Elements, format.Expr(el))
					}
				}
				out[i].Constants = append(out[i].Constants, c)
			}
		}
	}
	return out
}

func headerParams(header []syntax.EnumField) []Param {
	var out []Param
	for _, f := range header {
		out = append(out, Param{Name: f.Name, Type: typeString(f.Type), Key: f.Key})
	}
	return out
}

func routeSummaries(routes []*syntax.Route) []string {
	out := make([]string, len(routes))
	for i, r := range routes {
		out[i] = fmt.Sprintf("%s %s", r.Verb, r.Path)
	}
	return out
}

// errorTypeOf returns the E in a Result<S, E> return type, or "" when
// retType isn't a two-argument Result — a card's Error field, letting
// `catalog find --error` search the typed-error half of a signature
// without string-matching the rendered Returns field.
func errorTypeOf(retType syntax.Type) string {
	q, ok := retType.(*syntax.QualifiedType)
	if !ok || len(q.Path) != 1 || q.Path[0] != "Result" || len(q.Args) != 2 {
		return ""
	}
	return typeString(q.Args[1])
}

// typeString renders a syntax.Type back to the same spelling it would have
// in source — the compact, human/LLM-readable form contract cards need
// (fiig-lang.md §3.11), not a debug dump of the AST shape.
func typeString(t syntax.Type) string {
	switch t := t.(type) {
	case *syntax.QualifiedType:
		s := strings.Join(t.Path, ".")
		if len(t.Args) > 0 {
			args := make([]string, len(t.Args))
			for i, a := range t.Args {
				args[i] = typeString(a)
			}
			s += "<" + strings.Join(args, ", ") + ">"
		}
		return s
	case *syntax.RecordType:
		fields := make([]string, len(t.Fields))
		for i, f := range t.Fields {
			fields[i] = f.Name + ": " + typeString(f.Type)
		}
		return "{ " + strings.Join(fields, ", ") + " }"
	case *syntax.UnionType:
		members := make([]string, len(t.Members))
		for i, m := range t.Members {
			members[i] = typeString(m)
		}
		return strings.Join(members, " | ")
	default:
		return "?"
	}
}
