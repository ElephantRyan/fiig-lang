package runprogram

import (
	"fmt"
	"os"
	"sort"
	"unicode"

	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// Bindings is ADR 0079 §6a's listing of where each domain's capabilities
// come from: for every dependency that declares a workflow (a domain a
// gateway or a served adapter enters) or has bindings of its own, each
// capability its gateways require, and whether it has its own binding or
// shares the process-wide one. own reports a package's own binding.
func Bindings(packages types.Packages, own func(path, capability string) bool, ownPaths []string) []string {
	listed := map[string]bool{}
	for _, path := range ownPaths {
		listed[path] = true
	}
	for path, pkg := range packages {
		for _, d := range pkg.Universe.Decls {
			if _, ok := d.(*syntax.WorkflowDecl); ok {
				listed[path] = true
			}
		}
	}
	paths := make([]string, 0, len(listed))
	for path := range listed {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var out []string
	for _, path := range paths {
		pkg := packages[path]
		if pkg == nil {
			continue
		}
		for _, capability := range requiredCapabilities(pkg) {
			how := "shared with the application"
			if own(path, capability) {
				how = "its own binding"
			}
			out = append(out, fmt.Sprintf("%s: %s (%s)", path, capability, how))
		}
	}
	return out
}

// requiredCapabilities is every capability pkg's gateways require, sorted:
// a capitalized name that isn't one of its own or imported capability
// groups (those are fiig code, bound by what they require in turn).
func requiredCapabilities(pkg *types.Package) []string {
	seen := map[string]bool{}
	add := func(name string) {
		if name == "" || !unicode.IsUpper(rune(name[0])) {
			return
		}
		if d, ok := pkg.Universe.Decls[name]; ok {
			if _, group := d.(*syntax.CapabilityDecl); group {
				return
			}
		}
		if _, group := pkg.Groups[name]; group {
			return
		}
		seen[name] = true
	}
	for _, d := range pkg.Universe.Decls {
		switch d := d.(type) {
		case *syntax.GatewayDecl:
			add(d.Requires)
		case *syntax.CapabilityDecl:
			for _, g := range d.Gateways {
				add(g.Requires)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// CapsPackages is a capabilities file's "packages" section (ADR 0079 §6a)
// read for its shape alone: package path -> the capabilities it binds,
// without reading the environment, so `fiig config` can list bindings
// before the variables are set. An empty path has none.
func CapsPackages(path string) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	if path == "" {
		return out, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parsed, err := runtime.ParseJSON(raw)
	if err != nil {
		return nil, err
	}
	top, _ := parsed.(map[string]any)
	perPackage, _ := top["packages"].(map[string]any)
	for pkg, v := range perPackage {
		caps, _ := v.(map[string]any)
		out[pkg] = map[string]bool{}
		for capability := range caps {
			out[pkg][capability] = true
		}
	}
	return out, nil
}

// UnknownPackages is every path the caps file binds for that isn't one of
// packages: a binding for a package the root doesn't depend on, an error
// like a stale rename (ADR 0071 §5).
func UnknownPackages(bound []string, packages types.Packages) []string {
	var out []string
	for _, path := range bound {
		if _, ok := packages[path]; !ok {
			out = append(out, fmt.Sprintf("the capabilities file binds for package %s, which the application doesn't depend on (ADR 0079 §6a)", path))
		}
	}
	return out
}
