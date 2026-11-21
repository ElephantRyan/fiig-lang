// Package migrate records and applies a program's schema migrations (ADR
// 0096) through the Database capability: the root package's and every
// dependency's, dependencies first, each on its package's own Database
// binding or the process-wide one. compiler/migration reads the files.
package migrate

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"fiig-lang/compiler/manifest"
	"fiig-lang/compiler/migration"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/types"
)

// Mode is what Run does with pending migrations.
type Mode string

const (
	// Apply applies them (the default, §2).
	Apply Mode = "apply"
	// Check refuses to start while any are pending (-migrate=check).
	Check Mode = "check"
	// Skip leaves the database alone, for `fiig serve -check` (ADR 0124
	// §6); no flag sets it.
	Skip Mode = "skip"
)

// ParseMode reads the -migrate flag.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case Apply, Check:
		return Mode(s), nil
	}
	return "", fmt.Errorf("-migrate is apply or check, not %q", s)
}

// Package is one package's migrations.
type Package struct {
	Path  string // "" for a root package with no manifest
	Files []migration.File
}

// Collect is the root package's migrations and its dependencies', each
// package after the ones it depends on. A root migration file that
// doesn't fit ADR 0096 §1 is an error; a dependency's already were, at
// load.
func Collect(root string, packages types.Packages) ([]Package, []error) {
	rootFiles, errs := migration.Read(os.DirFS(root))
	if len(errs) > 0 {
		return nil, errs
	}
	var out []Package
	seen := map[*types.Package]bool{}
	var visit func(types.Packages)
	visit = func(pkgs types.Packages) {
		paths := make([]string, 0, len(pkgs))
		for p := range pkgs {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			pkg := pkgs[p]
			if pkg == nil || seen[pkg] {
				continue
			}
			seen[pkg] = true
			visit(pkg.Dependencies)
			if len(pkg.Migrations) > 0 {
				out = append(out, Package{Path: p, Files: pkg.Migrations})
			}
		}
	}
	visit(packages)
	if len(rootFiles) > 0 {
		path := ""
		if m, errs := manifest.Load(root); m != nil && len(errs) == 0 {
			path = m.Path
		}
		out = append(out, Package{Path: path, Files: rootFiles})
	}
	return out, nil
}

// Run brings each package's database up to date (or, in Check mode,
// reports what's pending). applied names each migration applied;
// problems is every reason the program shouldn't start, all at once.
func Run(ctx context.Context, pkgs []Package, caps *runtime.CapabilityRegistry, mode Mode) (applied, problems []string) {
	// Group the packages by the database they're bound to, in order.
	type target struct {
		db   runtime.CapabilityProvider
		pkgs []Package
	}
	var targets []*target
	byDB := map[runtime.CapabilityProvider]*target{}
	for _, p := range pkgs {
		db, ok := caps.LookupFor(p.Path, types.DatabaseName)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s has migrations, but no Database is bound for it; add one to the capabilities file (ADR 0096 §2)", describe(p.Path)))
			continue
		}
		t := byDB[db]
		if t == nil {
			t = &target{db: db}
			byDB[db] = t
			targets = append(targets, t)
		}
		t.pkgs = append(t.pkgs, p)
	}
	for _, t := range targets {
		a, p := RunOn(ctx, t.db, t.pkgs, mode)
		applied = append(applied, a...)
		problems = append(problems, p...)
	}
	return applied, problems
}

const createTable = "CREATE TABLE IF NOT EXISTS fiig_migrations (package TEXT NOT NULL, number INTEGER NOT NULL, name TEXT NOT NULL, sha256 TEXT NOT NULL, applied_at TEXT NOT NULL, PRIMARY KEY (package, number))"

type recorded struct {
	name, sum string
}

// RunOn migrates one database: §3's checks against what it records, then
// each pending migration in its own transaction (§4).
func RunOn(ctx context.Context, db runtime.CapabilityProvider, pkgs []Package, mode Mode) (applied, problems []string) {
	if len(pkgs) == 0 {
		return nil, nil
	}
	if _, f := db.Call(ctx, "execute", []runtime.Value{runtime.Str(createTable)}); f != nil {
		return nil, []string{"migrations: creating fiig_migrations: " + f.Message}
	}
	done, f := readApplied(ctx, db)
	if f != nil {
		return nil, []string{"migrations: reading fiig_migrations: " + f.Message}
	}
	var pending []pendingFile
	for _, p := range pkgs {
		files := map[int]migration.File{}
		highest := -1
		for _, file := range p.Files {
			files[file.Number] = file
		}
		numbers := make([]int, 0, len(done[p.Path]))
		for n := range done[p.Path] {
			numbers = append(numbers, n)
		}
		sort.Ints(numbers)
		for _, n := range numbers {
			r := done[p.Path][n]
			file, ok := files[n]
			switch {
			case !ok:
				problems = append(problems, fmt.Sprintf("%s: migration %s was applied, but its file is gone; restore it (ADR 0096 §3)", describe(p.Path), r.name))
			case file.Sum() != r.sum:
				problems = append(problems, fmt.Sprintf("%s: migrations/%s.sql was edited after it was applied; put it back and write the change as a new migration (ADR 0096 §3)", describe(p.Path), file.Name))
			}
			if n > highest {
				highest = n
			}
		}
		for _, file := range p.Files {
			if _, ok := done[p.Path][file.Number]; ok {
				continue
			}
			if file.Number < highest {
				problems = append(problems, fmt.Sprintf("%s: migrations/%s.sql is numbered before %s, which was already applied; renumber it after %d (ADR 0096 §3)", describe(p.Path), file.Name, done[p.Path][highest].name, highest))
				continue
			}
			pending = append(pending, pendingFile{p.Path, file})
		}
	}
	if len(problems) > 0 {
		return nil, problems
	}
	if mode == Check {
		for _, pf := range pending {
			problems = append(problems, fmt.Sprintf("%s: migration %s is pending; run fiig migrate, or start without -migrate=check (ADR 0096 §2)", describe(pf.pkg), pf.file.Name))
		}
		return nil, problems
	}
	for _, pf := range pending {
		ran, f := apply(ctx, db, pf)
		if f != nil {
			return applied, append(problems, fmt.Sprintf("%s: migration %s failed and was rolled back: %s", describe(pf.pkg), pf.file.Name, f.Message))
		}
		if ran {
			applied = append(applied, strings.TrimSpace(pf.pkg+" "+pf.file.Name))
		}
	}
	return applied, nil
}

type pendingFile struct {
	pkg  string
	file migration.File
}

// apply runs one migration in a transaction, recording it first, so a
// second process starting at once waits on the row and then finds it
// applied (§4). ran is false when that happened.
func apply(ctx context.Context, db runtime.CapabilityProvider, pf pendingFile) (bool, *runtime.Fault) {
	exec := db
	var tx runtime.Tx
	if t, ok := db.(runtime.TransactionalProvider); ok {
		var f *runtime.Fault
		if tx, f = t.Begin(ctx, ""); f != nil {
			return false, f
		}
		exec = tx
	}
	fail := func(f *runtime.Fault) (bool, *runtime.Fault) {
		if tx != nil {
			tx.Rollback()
		}
		return false, f
	}
	record := []runtime.Value{
		runtime.Str("INSERT INTO fiig_migrations (package, number, name, sha256, applied_at) VALUES (?, ?, ?, ?, ?)"),
		runtime.Str(pf.pkg), runtime.Int(pf.file.Number), runtime.Str(pf.file.Name), runtime.Str(pf.file.Sum()),
		runtime.Str(time.Now().UTC().Format(time.RFC3339)),
	}
	if _, f := exec.Call(ctx, "execute", record); f != nil {
		fail(nil)
		if done, err := readApplied(ctx, db); err == nil {
			if r, ok := done[pf.pkg][pf.file.Number]; ok && r.sum == pf.file.Sum() {
				return false, nil // another process applied it
			}
		}
		return false, f
	}
	for _, stmt := range migration.Statements(pf.file.SQL) {
		if _, f := exec.Call(ctx, "execute", []runtime.Value{runtime.Str(stmt)}); f != nil {
			return fail(f)
		}
	}
	if tx != nil {
		if f := tx.Commit(); f != nil {
			return false, f
		}
	}
	return true, nil
}

// readApplied is what fiig_migrations records, by package and number.
func readApplied(ctx context.Context, db runtime.CapabilityProvider) (map[string]map[int]recorded, *runtime.Fault) {
	v, f := db.Call(ctx, "queryMany", []runtime.Value{runtime.Str("SELECT package, number, name, sha256 FROM fiig_migrations")})
	if f != nil {
		return nil, f
	}
	rows, _ := v.(runtime.List)
	out := map[string]map[int]recorded{}
	for _, r := range rows.Elems {
		rec, _ := r.(runtime.Record)
		pkg, _ := rec.Fields["package"].(runtime.Str)
		n, _ := rec.Fields["number"].(runtime.Int)
		name, _ := rec.Fields["name"].(runtime.Str)
		sum, _ := rec.Fields["sha256"].(runtime.Str)
		if out[string(pkg)] == nil {
			out[string(pkg)] = map[int]recorded{}
		}
		out[string(pkg)][int(n)] = recorded{string(name), string(sum)}
	}
	return out, nil
}

func describe(path string) string {
	if path == "" {
		return "this program"
	}
	return "package " + path
}
