package registry

import (
	"fmt"
	"strings"
)

// Query filters cards for `catalog find` (fiig-lang.md §Milestone 3's
// example commands). Text search is name-only: there's no intent/doc-comment
// text in the AST yet (ADR 0007), so type- and capability-based search is
// the reliable path, same as that ADR already calls out.
type Query struct {
	Text        string // case-insensitive substring match against Name
	Input       string // exact type match against any parameter's type
	Output      string // exact type match against Returns
	Requires    string // exact match against a gateway's capability (the "effect" axis)
	Error       string // exact match against the E in a Result<S, E> Returns
	Pure        bool   // when true, rules, conversions, formulas (ADR 0049), and activities that reach only those (ADR 0044)
	Idempotent  bool   // when true, only cards explicitly declared idempotent (ADR 0014)
	Composition bool   // search the composition catalog (workflows, adapters) instead of the default one
	Leaf        bool   // when true, only cards with no Dependencies (ADR 0030) — atomic building blocks, not composed from anything else
	Deprecated  bool   // when true, only cards with a deprecation notice (compiler/manifest's Deprecations) — an audit view of what still needs migrating
}

// Find returns cards matching q, scoped to one catalog
// (docs/decisions/0008-separate-composition-catalog.md) — the default
// catalog unless q.Composition is set.
func Find(cards []*Card, q Query) []*Card {
	scope := Default
	if q.Composition {
		scope = Composition
	}
	var out []*Card
	for _, c := range cards {
		if c.Catalog != scope {
			continue
		}
		if q.Text != "" && !strings.Contains(strings.ToLower(c.Name), strings.ToLower(q.Text)) {
			continue
		}
		if !matchesSignature(c, q.Input, q.Output) {
			continue
		}
		if q.Requires != "" && c.Requires != q.Requires {
			continue
		}
		if q.Error != "" && c.Error != q.Error {
			continue
		}
		if q.Pure && !c.Pure {
			continue
		}
		if q.Idempotent && !c.Idempotent {
			continue
		}
		if q.Leaf && len(c.Dependencies) > 0 {
			continue
		}
		if q.Deprecated && c.Deprecated == nil {
			continue
		}
		out = append(out, c)
	}
	return out
}

// ConstantMatch is one enum variant found by FindConstant.
type ConstantMatch struct {
	Enum    string `json:"enum"`
	Variant string `json:"variant"`
	Value   string `json:"value"` // the matched constant, as declared
	Package string `json:"package,omitempty"`
	Source  string `json:"source"`
}

// ParseConstantQuery splits `catalog find --constant field=value`.
func ParseConstantQuery(s string) (field, value string, err error) {
	field, value, ok := strings.Cut(s, "=")
	if !ok || field == "" || value == "" {
		return "", "", fmt.Errorf("--constant wants field=value, e.g. code=ACME; got %q", s)
	}
	return field, value, nil
}

// FindConstant returns every variant of a headed enum among cards whose
// header field `field` holds value, or, for a list field, has it as an
// element (ADR 0046). The answer is the variant, not the enum, because the
// question is "which client has code ACME". On a key field it is at most
// one variant per enum, since A16 forbids duplicates; on any other field
// it may be many ("which sources use FieldLayout.Csv").
//
// value matches exactly, as declared: `FieldLayout.Csv`, `5`, or a string
// with or without its quotes (`ACME` or `"ACME"`).
func FindConstant(cards []*Card, field, value string) []ConstantMatch {
	var out []ConstantMatch
	for _, c := range cards {
		for _, v := range c.Variants {
			for _, k := range v.Constants {
				if k.Name != field || !constantMatches(k, value) {
					continue
				}
				out = append(out, ConstantMatch{Enum: c.Name, Variant: v.Name, Value: k.Value, Package: c.Package, Source: c.Source})
			}
		}
	}
	return out
}

func constantMatches(k Constant, value string) bool {
	quoted := `"` + value + `"`
	if k.Elements != nil || k.Value == "[]" {
		for _, el := range k.Elements {
			if el == value || el == quoted {
				return true
			}
		}
		return false
	}
	return k.Value == value || k.Value == quoted
}

// ByName returns the first card (across both catalogs) with the given
// name, for `catalog explain`. In a catalog combined by Merge, a bare
// name alone can't disambiguate two different packages that happen to
// share it — this intentionally doesn't try, since ADR 0032 already
// requires every cross-package reference to go through an alias, not a
// bare name, so a real ambiguity here is a sign the caller wants
// Card.Package too, not a case this function needs to resolve itself.
func ByName(cards []*Card, name string) *Card {
	for _, c := range cards {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Siblings returns the names of other cards in the same catalog that share
// c's exact ordered parameter-type and return-type signature — the
// non-judgmental "N other declarations share this signature" fact ADR 0012
// calls for, not a similarity score. Cards with no params and no return type
// (models, values, errors) never report siblings; signature identity isn't
// meaningful for them, nor for a math constant, whose Returns is only the
// type it takes by default.
func Siblings(cards []*Card, c *Card) []string {
	if (len(c.Params) == 0 && c.Returns == "") || c.Kind == "constant" {
		return nil
	}
	var names []string
	for _, other := range cards {
		if other == c || other.Kind == "constant" || other.Catalog != c.Catalog || other.Returns != c.Returns {
			continue
		}
		if sameParamTypes(c.Params, other.Params) {
			names = append(names, other.Name)
		}
	}
	return names
}

// matchesSignature reports whether one of c's signatures takes input (when
// set) and returns output (when set): its own, or for a formula with an int
// and a float form, either form's.
func matchesSignature(c *Card, input, output string) bool {
	forms := c.Forms
	if len(forms) == 0 {
		forms = []Operation{{Params: c.Params, Returns: c.Returns}}
	}
	for _, f := range forms {
		if (input == "" || hasParamType(f.Params, input)) && (output == "" || f.Returns == output) {
			return true
		}
	}
	return false
}

func hasParamType(params []Param, typ string) bool {
	for _, p := range params {
		if p.Type == typ {
			return true
		}
	}
	return false
}

func sameParamTypes(a, b []Param) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Type != b[i].Type {
			return false
		}
	}
	return true
}
