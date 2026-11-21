// Package manifest defines ADR 0032's per-package manifest: the identity
// (canonical dotted package path, kind, own version) and direct
// dependencies (package path -> minimum version) every fiig package —
// library or application — declares about itself. `use` no longer carries
// a version (ADR 0032); this is where one lives instead.
//
// This package only defines and validates the manifest's shape. Resolving
// a build's actual dependency closure (Minimum Version Selection within a
// major version track, cross-major coexistence, the lockfile) is a later
// Milestone 6 step built on top of this.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"fiig-lang/compiler/capability"
	"fiig-lang/compiler/semver"
)

// Filename is the manifest's fixed name at a package root — the same role
// go.mod plays for a Go module.
const Filename = "fiig.pkg.json"

// Kind distinguishes a library package (importable, publishes
// models/rules/conversions/activities/gateways) from an application
// package (defines workflows/adapters, never itself importable) —
// docs/terminology.md's "Package" entry.
type Kind string

const (
	Library     Kind = "library"
	Application Kind = "application"
)

// Manifest is one package's declared identity and direct dependencies.
// Dependencies maps a canonical package path to the minimum version this
// package was built against — never an exact pin, never range syntax
// (ADR 0032). What version actually gets used in a given application
// build is resolved later, across every manifest pulled into that build,
// not decided by any single manifest alone.
type Manifest struct {
	Path string `json:"path"`
	Kind Kind   `json:"kind"`

	// Formerly is the paths this package had before a rename, newest
	// last. Migration history recorded under one of them is moved to Path
	// the next time the program migrates (ADR 0138), so a rename doesn't
	// orphan a database's history (Hearsay GAPS G55).
	Formerly     []string          `json:"formerly,omitempty"`
	Version      string            `json:"version"`
	Dependencies map[string]string `json:"dependencies,omitempty"`

	// Workspaces is where to look for this package's dependencies, as
	// directories relative to the package, the way -workspace names them
	// on the command line (ADR 0097). Only the root package's are read:
	// a dependency's dependencies are found from the root's.
	Workspaces []string `json:"workspaces,omitempty"`

	// Capabilities is the explicit allowlist of capability names (a
	// gateway's `requires` clause, grammar.md §4) this package's own
	// build is permitted to touch — the resolved build list, across
	// every transitive dependency, must never need a capability absent
	// from this list (Milestone 6's security exit criterion: "Imported
	// gateways cannot gain undeclared capabilities"). compiler/loader
	// is what actually enforces this against a resolved dependency
	// closure; this package only validates the list's own shape.
	Capabilities []string `json:"capabilities,omitempty"`

	// NativeCapabilities is the transitional "grandfather list" of ADR 0042:
	// capabilities that are not in the closed primitive set (compiler/
	// capability) yet are still provided natively by the host, because no
	// fiig fulfiller exists for them (that needs ADR 0043). A resolved build
	// may require a non-primitive capability only if it is named here, so no
	// new Go-only capability can appear unnoticed; the list is the visible
	// debt, and is meant to shrink to empty as fulfillers are written. A
	// primitive never belongs here.
	NativeCapabilities []string `json:"nativeCapabilities,omitempty"`

	// Deprecations maps one of this package's own declared symbol
	// names (bare, not dotted — it's always this package's own, never
	// another package's) to the deprecation notice for it — Milestone
	// 6's "deprecation metadata" deliverable. Deprecating a symbol is
	// advisory, not enforced by anything in compiler/checker or
	// compiler/loader; it only ever surfaces as Card.Deprecated in
	// compiler/registry's output, for a human or an LLM composing
	// against the registry to see before choosing to depend on it.
	Deprecations map[string]Deprecation `json:"deprecations,omitempty"`

	// Origin is this package's own declared canonical source location —
	// an organization's internal client library for an internal API is
	// the attested motivating case (Milestone 6's "organization package
	// source support"): a free-form string (typically a git URL or an
	// internal description) read directly by a human, never parsed or
	// fetched by anything here. compiler/loader still only ever
	// discovers a package by scanning local directories (there's no
	// network fetch step); Origin is provenance for whoever's auditing
	// or onboarding onto a build, not an instruction for how to obtain
	// one.
	Origin string `json:"origin,omitempty"`

	// Provides maps an interface, named by package path
	// (`acme.payments.PaymentProvider`), to the capability group in this
	// package that fulfills it (ADR 0043 §2). It is the declared intent that
	// makes completeness checkable — types.BindProvides verifies each entry
	// structurally — and the only place conformance is declared: nothing in
	// the group's source mentions the interface.
	Provides map[string]string `json:"provides,omitempty"`

	// Environment renames a dependency's config variables for this
	// application's deployment (ADR 0071 §5): package path -> its variable
	// -> the one the deployment sets. Only an application may rename,
	// since only it knows the deployment; compiler/loader checks that each
	// renamed variable is one the package reads.
	Environment map[string]map[string]string `json:"environment,omitempty"`
}

// Deprecation is one declaration's deprecation notice: Since is the
// first version of this package a consumer should treat it as no
// longer recommended; Message is free-form guidance (typically naming
// a replacement) a human or an LLM reads directly, not parsed by
// anything.
type Deprecation struct {
	Since   string `json:"since"`
	Message string `json:"message,omitempty"`
}

// packagePathPattern matches grammar.md's `package-path ::= identifier {
// '.' identifier }` — the same dotted-identifier shape `use` already
// requires, reused here since a manifest's own Path and every dependency
// key name the same kind of thing.
var packagePathPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)*$`)

// identifierPattern matches a bare, undotted identifier — a gateway's
// `requires` clause (grammar.md §4: `requires Database`, never `requires
// some.Database`) and a Deprecations key (always one of this package's
// own declared names, which are never dotted either) share this exact
// shape.
var identifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// envNamePattern is an environment variable's name as a config field
// writes one (ADR 0071 §1).
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Parse decodes and validates one manifest. Every problem is collected
// and returned together rather than stopping at the first — the same
// style checker.CheckFile already uses for diagnostics, since a manifest
// author fixing it by hand wants the full list in one pass.
func Parse(data []byte) (*Manifest, []error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, []error{fmt.Errorf("invalid JSON: %w", err)}
	}
	return &m, m.Validate()
}

// Load reads and validates the manifest at root's Filename.
func Load(root string) (*Manifest, []error) {
	data, err := os.ReadFile(filepath.Join(root, Filename))
	if err != nil {
		return nil, []error{err}
	}
	return Parse(data)
}

// Validate checks every field, collecting every problem found rather than
// stopping at the first.
func (m *Manifest) Validate() []error {
	var errs []error
	if !packagePathPattern.MatchString(m.Path) {
		errs = append(errs, fmt.Errorf("path %q is not a valid dotted package path", m.Path))
	}
	seenFormer := map[string]bool{}
	for _, former := range m.Formerly {
		switch {
		case !packagePathPattern.MatchString(former):
			errs = append(errs, fmt.Errorf("formerly %q is not a valid dotted package path", former))
		case former == m.Path:
			errs = append(errs, fmt.Errorf("formerly %q is this package's path; list only the paths it had before", former))
		case seenFormer[former]:
			errs = append(errs, fmt.Errorf("formerly lists %q twice", former))
		}
		seenFormer[former] = true
	}
	if m.Kind != Library && m.Kind != Application {
		errs = append(errs, fmt.Errorf("kind %q must be %q or %q", m.Kind, Library, Application))
	}
	if _, err := semver.Parse(m.Version); err != nil {
		errs = append(errs, fmt.Errorf("version %q: %w", m.Version, err))
	}
	for dep, min := range m.Dependencies {
		if !packagePathPattern.MatchString(dep) {
			errs = append(errs, fmt.Errorf("dependency path %q is not a valid dotted package path", dep))
		}
		if _, err := semver.Parse(min); err != nil {
			errs = append(errs, fmt.Errorf("dependency %q minimum version %q: %w", dep, min, err))
		}
	}
	for _, name := range m.NativeCapabilities {
		if !identifierPattern.MatchString(name) {
			errs = append(errs, fmt.Errorf("native capability %q is not a valid capability name", name))
		} else if capability.IsPrimitive(name) {
			errs = append(errs, fmt.Errorf("native capability %q is already a primitive; primitives need no grandfathering", name))
		}
	}
	for _, capability := range m.Capabilities {
		if !identifierPattern.MatchString(capability) {
			errs = append(errs, fmt.Errorf("capability %q is not a valid capability name", capability))
		}
	}
	for _, w := range m.Workspaces {
		if strings.TrimSpace(w) == "" {
			errs = append(errs, fmt.Errorf("workspaces: an entry is empty; name a directory, relative to this package, such as \"..\" (ADR 0097)"))
		}
	}
	if len(m.Environment) > 0 && m.Kind != Application {
		errs = append(errs, fmt.Errorf("environment renames are an application's to make, since only it knows the deployment; a %s can't rename its dependencies' variables (ADR 0071 §5)", m.Kind))
	}
	for pkg, renames := range m.Environment {
		if !packagePathPattern.MatchString(pkg) {
			errs = append(errs, fmt.Errorf("environment key %q is not a valid dotted package path", pkg))
		} else if _, dep := m.Dependencies[pkg]; !dep {
			errs = append(errs, fmt.Errorf("environment renames variables of %q, which isn't a dependency", pkg))
		}
		for from, to := range renames {
			if !envNamePattern.MatchString(from) || !envNamePattern.MatchString(to) {
				errs = append(errs, fmt.Errorf("environment %q: %q -> %q must both be environment variable names", pkg, from, to))
			}
		}
	}
	for iface, group := range m.Provides {
		if !packagePathPattern.MatchString(iface) || !strings.Contains(iface, ".") {
			errs = append(errs, fmt.Errorf("provides key %q must name an interface by package path, e.g. acme.payments.PaymentProvider", iface))
		}
		if !identifierPattern.MatchString(group) {
			errs = append(errs, fmt.Errorf("provides %q: %q is not a capability group name", iface, group))
		}
	}
	for name, dep := range m.Deprecations {
		if !identifierPattern.MatchString(name) {
			errs = append(errs, fmt.Errorf("deprecation key %q is not a valid declaration name", name))
		}
		if _, err := semver.Parse(dep.Since); err != nil {
			errs = append(errs, fmt.Errorf("deprecation %q since %q: %w", name, dep.Since, err))
		}
	}
	return errs
}
