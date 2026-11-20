// Package runprogram collects the small, genuinely host-independent
// pieces of loading, resolving, and preparing to execute a checked fiig
// program — shared by tooling/run-cli and any other host that wants to
// run one (e.g. libraries/sql's own executable, which additionally
// registers a real "sql" capability factory). Each host still owns its
// own CLI surface (flags, output format, exit codes) and its own
// CapabilityRegistry; this package only holds what would otherwise be
// duplicated between them line for line.
package runprogram

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/manifest"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
	"fiig-lang/tooling/migrate"
)

// LoadFiles parses every .fiig file under root, mirroring
// compiler/checker.CheckDir's own walk — but (unlike CheckDir) returns
// the parsed files themselves, since a host needs them to build a
// Universe and actually execute against, not just report diagnostics.
// The package's tests/ directory is left out: tests aren't part of the
// program (ADR 0091 §2).
func LoadFiles(root string) ([]*syntax.File, []checker.Diagnostic, error) {
	var files []*syntax.File
	var diags []checker.Diagnostic
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path == filepath.Join(root, "tests") {
			return filepath.SkipDir
		}
		if d.IsDir() || filepath.Ext(path) != ".fiig" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		f, errs := parser.Parse(string(src))
		for _, e := range errs {
			diags = append(diags, checker.Diagnostic{File: rel, Msg: e.Msg, At: e.At, Rule: e.Rule})
		}
		files = append(files, f)
		return nil
	})
	return files, diags, err
}

// BuildImports resolves every one of files' own `use ... as alias`
// imports against packages, keyed by declaration name (one entry per
// declaration, pointing at whichever file it was declared in's own
// aliases) — compiler/runtime.Interp.Imports' own shape, built the same
// way checker.CheckFile resolves a file's imports, just reorganized so
// Interp.Call can look it up by the declaration name it's given rather
// than by file. nil when packages is empty, same as every pre-
// Milestone-6 caller.
func BuildImports(files []*syntax.File, packages types.Packages) map[string]map[string]*types.Package {
	if len(packages) == 0 {
		return nil
	}
	imports := map[string]map[string]*types.Package{}
	for _, f := range files {
		aliases := checker.ResolveFileImports(f, packages)
		if len(aliases) == 0 {
			continue
		}
		for _, d := range f.Decls {
			if name := types.DeclName(d); name != "" {
				imports[name] = aliases
			}
		}
	}
	return imports
}

// BuildGroups is BuildImports for capability-group imports (`use
// acme.http.Client`, ADR 0043) — compiler/runtime.Interp.Groups' shape.
func BuildGroups(files []*syntax.File, packages types.Packages) map[string]map[string]*types.GroupRef {
	if len(packages) == 0 {
		return nil
	}
	groups := map[string]map[string]*types.GroupRef{}
	for _, f := range files {
		aliases := checker.ResolveFileGroups(f, packages)
		if len(aliases) == 0 {
			continue
		}
		for _, d := range f.Decls {
			if name := types.DeclName(d); name != "" {
				groups[name] = aliases
			}
		}
	}
	return groups
}

// Check type-checks files as the package rooted at root: with its
// manifest's path and `provides` map when root has one (ADR 0043 §2), so a
// capability group is assignable to the interfaces it provides, the same as
// checker.CheckDir. Without a manifest it is checker.Check.
func Check(root string, files []*syntax.File, packages types.Packages) []checker.Diagnostic {
	if _, err := os.Stat(filepath.Join(root, manifest.Filename)); err != nil {
		return checker.Check(files, packages)
	}
	m, errs := manifest.Load(root)
	if len(errs) > 0 {
		var diags []checker.Diagnostic
		for _, e := range errs {
			diags = append(diags, checker.Diagnostic{File: manifest.Filename, Msg: e.Error()})
		}
		return diags
	}
	return checker.CheckPackage(files, packages, m.Path, m.Provides)
}

// UniversesOf flattens an alias -> *types.Package map to alias ->
// *types.Universe — all types.ResolveWithImports needs to resolve an
// entry declaration's own parameter types (e.g. systemTime.Instant)
// before decoding JSON arguments against them.
func UniversesOf(imports map[string]*types.Package) map[string]*types.Universe {
	if len(imports) == 0 {
		return nil
	}
	us := make(map[string]*types.Universe, len(imports))
	for alias, pkg := range imports {
		us[alias] = pkg.Universe
	}
	return us
}

// DeclParams returns name's own parameter list, for any of the five
// callable declaration kinds this language has.
func DeclParams(u *types.Universe, name string) ([]syntax.Param, bool) {
	decl, found := u.Lookup(name)
	if !found {
		return nil, false
	}
	switch decl := decl.(type) {
	case *syntax.RuleDecl:
		return decl.Params, true
	case *syntax.ConversionDecl:
		return decl.Params, true
	case *syntax.ActivityDecl:
		return decl.Params, true
	case *syntax.GatewayDecl:
		return decl.Params, true
	case *syntax.WorkflowDecl:
		return decl.Params, true
	default:
		return nil, false
	}
}

// ParseArgs reads a -args JSON object through runtime.ParseJSON, so numbers
// keep their text and a duplicate argument name is an error (ADR 0062 §6).
func ParseArgs(argsJSON string) (map[string]any, error) {
	parsed, err := runtime.ParseJSON([]byte(argsJSON))
	if err != nil {
		return nil, err
	}
	args, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("must be a JSON object")
	}
	return args, nil
}

// Migrate brings the program's databases up to date before it starts
// (ADR 0096 §2), logging each migration it applies, or refuses with
// every problem at once, as a configuration problem does.
func Migrate(ctx context.Context, root string, packages types.Packages, caps *runtime.CapabilityRegistry, mode migrate.Mode) error {
	pkgs, errs := migrate.Collect(root, packages)
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	applied, problems := migrate.Run(ctx, pkgs, caps, mode)
	for _, a := range applied {
		fmt.Fprintln(os.Stderr, "fiig: applied migration", a)
	}
	if len(problems) > 0 {
		return fmt.Errorf("refusing to start:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}
