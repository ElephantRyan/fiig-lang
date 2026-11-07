// Contract-level upgrade diff (fiig-lang.md's Milestone 6 deliverable)
// and the version-bump policy check ADR 0032 point 6 calls for: the
// required semver bump between two releases of a package is derived from
// diffing their own contract cards, Elm-style, rather than trusting
// whoever typed a new number into the manifest's version field.
package registry

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"fiig-lang/compiler/semver"
)

// Bump is the smallest semver-component increment a change requires,
// mirroring Elm's own three-level model (PATCH/MINOR/MAJOR) rather than a
// binary breaking/non-breaking split: Patch means no API-visible change
// at all (nothing here rules out releasing a patch — a bugfix with no
// signature change is exactly that), Minor an additive, backward-
// compatible one, Major a breaking one. Ordered so the zero value is the
// smallest and max(...) picks the strictest requirement across many
// changes.
type Bump int

const (
	Patch Bump = iota
	Minor
	Major
)

func (b Bump) String() string {
	switch b {
	case Patch:
		return "patch"
	case Minor:
		return "minor"
	case Major:
		return "major"
	default:
		return "unknown"
	}
}

// MarshalJSON renders a Bump as its string form ("patch"/"minor"/"major")
// rather than the underlying int — a Change's whole point is to be
// readable directly (the Milestone 6 "readable contract diff" exit
// criterion) without the reader having to know this type's own ordering.
func (b Bump) MarshalJSON() ([]byte, error) {
	return json.Marshal(b.String())
}

// Change is one specific difference found between two releases of a
// single declaration — the "readable contract diff" the Milestone 6 exit
// criteria calls for; a human (or an LLM composing against this package)
// can read Msg directly without re-deriving it from two raw Card values.
type Change struct {
	Name string `json:"name"`
	Bump Bump   `json:"bump"`
	Msg  string `json:"message"`
}

// Diff compares two releases' contract cards and returns the minimum
// bump the newer release must carry, along with every individual change
// found. A declaration present in both is compared field-by-field; one
// only in old is a removal (Major); one only in newer is an addition
// (Minor). Dependencies is deliberately ignored — it's who a declaration
// happens to call internally, not part of its own contract.
func Diff(old, newer []*Card) (Bump, []Change) {
	oldByName := indexByName(old)
	newByName := indexByName(newer)

	var changes []Change
	for name, oc := range oldByName {
		nc, ok := newByName[name]
		if !ok {
			changes = append(changes, Change{Name: name, Bump: Major, Msg: fmt.Sprintf("%q removed", name)})
			continue
		}
		changes = append(changes, cardDiff(oc, nc)...)
	}
	for name := range newByName {
		if _, ok := oldByName[name]; !ok {
			changes = append(changes, Change{Name: name, Bump: Minor, Msg: fmt.Sprintf("%q added", name)})
			if nc := newByName[name]; nc.Kind == "config" {
				changes = append(changes, envChanges(name, nil, nc.Environment)...)
			}
		}
	}

	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Name != changes[j].Name {
			return changes[i].Name < changes[j].Name
		}
		return changes[i].Msg < changes[j].Msg
	})

	required := Patch
	for _, c := range changes {
		if c.Bump > required {
			required = c.Bump
		}
	}
	return required, changes
}

// CheckBump reports whether the version actually declared between old
// and newer satisfies required, the bump Diff computed. Declaring at
// least as large a bump as the diff demands is fine even when it's more
// than strictly necessary (an author may major-bump for reasons the
// contract diff can't see); declaring less is always an error — this is
// the "don't trust the author's own classification" half of ADR 0032
// point 6.
func CheckBump(old, newer semver.Version, required Bump) error {
	if newer.Compare(old) <= 0 {
		return fmt.Errorf("new version %s is not greater than old version %s", newer, old)
	}
	declared := declaredBump(old, newer)
	if declared < required {
		return fmt.Errorf("version bump from %s to %s is only a %s bump, but the contract diff requires at least a %s bump", old, newer, declared, required)
	}
	return nil
}

func declaredBump(old, newer semver.Version) Bump {
	switch {
	case newer.Major != old.Major:
		return Major
	case newer.Minor != old.Minor:
		return Minor
	default:
		return Patch
	}
}

func indexByName(cards []*Card) map[string]*Card {
	m := make(map[string]*Card, len(cards))
	for _, c := range cards {
		m[c.Name] = c
	}
	return m
}

// cardDiff compares two releases of the same-named declaration. A Kind
// change is reported alone (nothing else about the two cards is
// comparable once the declaration itself has become a different kind of
// thing); otherwise every kind-specific contract field is compared.
func cardDiff(old, newer *Card) []Change {
	note := func(bump Bump, format string, args ...any) Change {
		return Change{Name: old.Name, Bump: bump, Msg: fmt.Sprintf(format, args...)}
	}

	if old.Kind != newer.Kind {
		return []Change{note(Major, "kind changed from %q to %q", old.Kind, newer.Kind)}
	}

	var out []Change
	switch old.Kind {
	case "rule", "conversion", "activity", "gateway", "workflow", "formula":
		if !sameParams(old.Params, newer.Params) {
			out = append(out, note(Major, "parameters changed from %s to %s", paramsString(old.Params), paramsString(newer.Params)))
		}
		if old.Returns != newer.Returns {
			out = append(out, note(Major, "return type changed from %q to %q", old.Returns, newer.Returns))
		}
		if old.Error != newer.Error {
			out = append(out, note(Major, "error type changed from %q to %q", old.Error, newer.Error))
		}
		if old.Kind == "gateway" {
			if old.Requires != newer.Requires {
				out = append(out, note(Major, "required capability changed from %q to %q", old.Requires, newer.Requires))
			}
			if old.Idempotent && !newer.Idempotent {
				out = append(out, note(Major, "no longer idempotent"))
			} else if !old.Idempotent && newer.Idempotent {
				out = append(out, note(Minor, "now idempotent"))
			}
		}
	case "model":
		out = append(out, fieldChanges(old.Name, old.Fields, newer.Fields)...)
	case "enum":
		out = append(out, headerChanges(old.Name, old.Header, newer.Header)...)
		out = append(out, variantChanges(old.Name, old.Variants, newer.Variants)...)
		out = append(out, constantChanges(old.Name, newer.Header, old.Variants, newer.Variants)...)
	case "adapter":
		if old.AdapterKind != newer.AdapterKind {
			out = append(out, note(Major, "adapter kind changed from %q to %q", old.AdapterKind, newer.AdapterKind))
		}
		out = append(out, routeChanges(old.Name, old.Routes, newer.Routes)...)
	case "config":
		out = append(out, envChanges(old.Name, old.Environment, newer.Environment)...)
	}
	return out
}

// envChanges is ADR 0071 §5: reading the environment is authority, so a
// release that reads a new variable is reported the way a new capability
// is (ADR 0035). A new required variable breaks every deployment that
// doesn't set it, so it is Major; one with a default, or optional, is
// Minor. A variable whose type changes may no longer read a deployment's
// value, so that is Major too.
func envChanges(name string, old, newer []EnvVar) []Change {
	var out []Change
	before := map[string]EnvVar{}
	for _, v := range old {
		before[v.Name] = v
	}
	after := map[string]bool{}
	for _, v := range newer {
		after[v.Name] = true
		was, existed := before[v.Name]
		switch {
		case !existed && v.Default == "" && !strings.HasSuffix(v.Type, "| None"):
			out = append(out, Change{Name: name, Bump: Major, Msg: fmt.Sprintf("now reads environment variable %s (%s), which every deployment must set", v.Name, v.Type)})
		case !existed:
			out = append(out, Change{Name: name, Bump: Minor, Msg: fmt.Sprintf("now reads environment variable %s (%s, not required)", v.Name, v.Type)})
		case was.Type != v.Type:
			out = append(out, Change{Name: name, Bump: Major, Msg: fmt.Sprintf("environment variable %s changed type from %s to %s", v.Name, was.Type, v.Type)})
		case was.Default != v.Default:
			out = append(out, Change{Name: name, Bump: Minor, Msg: fmt.Sprintf("environment variable %s's default changed from %q to %q", v.Name, was.Default, v.Default)})
		}
	}
	for _, v := range old {
		if !after[v.Name] {
			out = append(out, Change{Name: name, Bump: Patch, Msg: fmt.Sprintf("no longer reads environment variable %s", v.Name)})
		}
	}
	return out
}

func sameParams(a, b []Param) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func paramsString(params []Param) string {
	s := "()"
	if len(params) == 0 {
		return s
	}
	out := "("
	for i, p := range params {
		if i > 0 {
			out += ", "
		}
		out += p.Name + ": " + p.Type
	}
	return out + ")"
}

// fieldChanges compares a model's fields by name: a removed field or one
// whose type changed is Major (an existing reader breaks); an added
// field is Minor.
func fieldChanges(name string, old, newer []Param) []Change {
	oldByName := map[string]Param{}
	for _, f := range old {
		oldByName[f.Name] = f
	}
	newByName := map[string]Param{}
	for _, f := range newer {
		newByName[f.Name] = f
	}
	var out []Change
	for n, of := range oldByName {
		if nf, ok := newByName[n]; !ok {
			out = append(out, Change{Name: name, Bump: Major, Msg: fmt.Sprintf("field %q removed", n)})
		} else if nf.Type != of.Type {
			out = append(out, Change{Name: name, Bump: Major, Msg: fmt.Sprintf("field %q type changed from %q to %q", n, of.Type, nf.Type)})
		}
	}
	for n := range newByName {
		if _, ok := oldByName[n]; !ok {
			out = append(out, Change{Name: name, Bump: Minor, Msg: fmt.Sprintf("field %q added", n)})
		}
	}
	return out
}

// headerChanges compares a headed enum's header (ADR 0046 §4) as
// fieldChanges does a model's, plus the key marker: losing one removes the
// derived lookup callers use (Major); gaining one adds a lookup (Minor).
func headerChanges(name string, old, newer []Param) []Change {
	out := fieldChanges(name, old, newer)
	oldByName := map[string]Param{}
	for _, f := range old {
		oldByName[f.Name] = f
	}
	for _, nf := range newer {
		of, ok := oldByName[nf.Name]
		switch {
		case !ok || of.Key == nf.Key:
		case of.Key:
			out = append(out, Change{Name: name, Bump: Major, Msg: fmt.Sprintf("field %q is no longer a key, so its lookup is gone", nf.Name)})
		default:
			out = append(out, Change{Name: name, Bump: Minor, Msg: fmt.Sprintf("field %q is now a key", nf.Name)})
		}
	}
	return out
}

// constantChanges compares the constants of variants present in both
// versions. A changed key value is Major: a lookup that found the variant
// by its old value now returns None. Any other changed constant is Minor.
func constantChanges(name string, header []Param, old, newer []Variant) []Change {
	isKey := map[string]bool{}
	for _, f := range header {
		isKey[f.Name] = f.Key
	}
	newByName := map[string]Variant{}
	for _, v := range newer {
		newByName[v.Name] = v
	}
	var out []Change
	for _, ov := range old {
		nv, ok := newByName[ov.Name]
		if !ok {
			continue
		}
		newValues := map[string]string{}
		for _, c := range nv.Constants {
			newValues[c.Name] = c.Value
		}
		for _, c := range ov.Constants {
			nval, ok := newValues[c.Name]
			if !ok || nval == c.Value {
				continue
			}
			bump := Minor
			if isKey[c.Name] {
				bump = Major
			}
			out = append(out, Change{Name: name, Bump: bump, Msg: fmt.Sprintf("variant %q %s changed from %s to %s", ov.Name, c.Name, c.Value, nval)})
		}
	}
	return out
}

// variantChanges compares an error's variants by name: a removed variant
// or one whose fields changed is Major; an added variant is Minor.
func variantChanges(name string, old, newer []Variant) []Change {
	oldByName := map[string]Variant{}
	for _, v := range old {
		oldByName[v.Name] = v
	}
	newByName := map[string]Variant{}
	for _, v := range newer {
		newByName[v.Name] = v
	}
	var out []Change
	for n, ov := range oldByName {
		nv, ok := newByName[n]
		if !ok {
			out = append(out, Change{Name: name, Bump: Major, Msg: fmt.Sprintf("variant %q removed", n)})
			continue
		}
		if !sameParams(ov.Fields, nv.Fields) {
			out = append(out, Change{Name: name, Bump: Major, Msg: fmt.Sprintf("variant %q fields changed from %s to %s", n, paramsString(ov.Fields), paramsString(nv.Fields))})
		}
	}
	for n := range newByName {
		if _, ok := oldByName[n]; !ok {
			out = append(out, Change{Name: name, Bump: Minor, Msg: fmt.Sprintf("variant %q added", n)})
		}
	}
	return out
}

// routeChanges compares an adapter's routes as a set of "VERB /path"
// strings (Card.Routes' own format) — a removed route is Major, an added
// one Minor. A route whose verb or path changed shows up as one removal
// plus one addition, which is exactly the right read: callers of the old
// route break, callers of the new one are new.
func routeChanges(name string, old, newer []string) []Change {
	oldSet := map[string]bool{}
	for _, r := range old {
		oldSet[r] = true
	}
	newSet := map[string]bool{}
	for _, r := range newer {
		newSet[r] = true
	}
	var out []Change
	for r := range oldSet {
		if !newSet[r] {
			out = append(out, Change{Name: name, Bump: Major, Msg: fmt.Sprintf("route %q removed", r)})
		}
	}
	for r := range newSet {
		if !oldSet[r] {
			out = append(out, Change{Name: name, Bump: Minor, Msg: fmt.Sprintf("route %q added", r)})
		}
	}
	return out
}
