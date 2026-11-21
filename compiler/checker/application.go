package checker

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
)

// ADR 0132: an application says what each process of the program serves. The
// bug classes, in O:
//
//   - O1: a name in a serve block that is no adapter (not imported, no such
//     adapter, written with more than `alias.adapter`), or a block or an
//     application that serves nothing.
//   - O2: an adapter of the application's own packages that no serve block
//     names, so nothing runs it.
//   - O3: an application in a library, or a second one in a module.
//   - O4: a role declared twice, an adapter named twice in a block, or an
//     unnamed block beside others.

// checkApplicationDecl is O1, O2 and O4 for one application.
func (c *checker) checkApplicationDecl(d *syntax.ApplicationDecl) {
	if len(d.Serves) == 0 {
		c.report(d.Start, "O1", "application %s serves nothing; say what each process runs, as in `serve { sessions.sessionsApi }` (ADR 0132)", d.Name)
		return
	}
	served := map[syntax.Decl]bool{}
	roles := map[string]bool{}
	unnamed := 0
	for _, b := range d.Serves {
		if b.Role == "" {
			unnamed++
		} else if roles[b.Role] {
			c.report(b.RoleSpan.Start, "O4", "application %s has two serve blocks for role %q (ADR 0132)", d.Name, b.Role)
		}
		roles[b.Role] = true
		if len(b.Adapters) == 0 {
			c.report(b.Start, "O1", "a serve block names no adapter, so the process would run nothing (ADR 0132)")
		}
		inBlock := map[syntax.Decl]bool{}
		for _, r := range b.Adapters {
			decl := c.resolveServed(r)
			if decl == nil {
				continue
			}
			if inBlock[decl] {
				c.report(r.Start, "O4", "adapter %s is named twice in this serve block (ADR 0132)", strings.Join(r.Path, "."))
			}
			inBlock[decl] = true
			served[decl] = true
		}
	}
	if unnamed > 0 && len(d.Serves) > 1 {
		c.report(d.Start, "O4", "application %s mixes a serve block with no role and others; name every block, or write just one (ADR 0132)", d.Name)
	}
	c.checkEveryAdapterIsServed(d, served)
}

// resolveServed is the adapter a serve block's name refers to, or nil after
// reporting O1. `alias.adapter` is an adapter of an imported package, and a
// bare name one of this package's.
func (c *checker) resolveServed(r *syntax.AdapterRef) syntax.Decl {
	name := r.Path[len(r.Path)-1]
	switch len(r.Path) {
	case 1:
		decl, found := c.lookup(name)
		if !found {
			c.report(r.Start, "O1", "no adapter named %s in this package; name an imported package's adapter as alias.%s (ADR 0132)", name, name)
			return nil
		}
		return c.adapterNamed(r, decl, "this package")
	case 2:
		alias := r.Path[0]
		pkg, ok := c.imports[alias]
		if !ok {
			if _, bound := c.aliases[alias]; bound {
				return nil // a package not loaded in this run
			}
			c.report(r.Start, "O1", "%s isn't an imported package; add a `use` line for the package that declares %s, and name it by its last word (ADR 0132)", alias, name)
			return nil
		}
		path := pkg.Path
		if imp := c.aliases[alias]; imp != nil {
			path = strings.Join(imp.Path, ".")
		}
		decl, found := pkg.Universe.Lookup(name)
		if !found {
			c.report(r.Start, "O1", "package %s has no adapter named %s (ADR 0132)", path, name)
			return nil
		}
		if ad := c.adapterNamed(r, decl, "package "+path); ad != nil {
			c.refAfter(r.Start, alias, name, decl, "")
			return ad
		}
		return nil
	}
	c.report(r.Start, "O1", "write an adapter as alias.adapter, the imported package and the adapter, not %s (ADR 0132)", strings.Join(r.Path, "."))
	return nil
}

// adapterNamed is decl when it is an adapter, and nil after reporting that it
// isn't one.
func (c *checker) adapterNamed(r *syntax.AdapterRef, decl syntax.Decl, where string) syntax.Decl {
	if _, ok := decl.(*syntax.AdapterDecl); !ok {
		c.report(r.Start, "O1", "%s in %s is a %s, not an adapter; a serve block names the adapters a process runs (ADR 0132)", r.Path[len(r.Path)-1], where, syntax.DeclKindName(decl))
		return nil
	}
	if len(r.Path) == 1 {
		c.ref(r.Start, r.Path[0], decl, "")
	}
	return decl
}

// checkEveryAdapterIsServed is O2: an adapter in this package or the module's
// others that no serve block names is run by nothing.
func (c *checker) checkEveryAdapterIsServed(d *syntax.ApplicationDecl, served map[syntax.Decl]bool) {
	type own struct{ name, pkg string }
	var missing []own
	for _, decl := range c.universe.Decls {
		if ad, ok := decl.(*syntax.AdapterDecl); ok && !served[decl] {
			missing = append(missing, own{ad.Name, "this package"})
		}
	}
	for path, pkg := range c.siblings {
		if path == c.ownPath {
			continue
		}
		for _, decl := range pkg.Universe.Decls {
			if ad, ok := decl.(*syntax.AdapterDecl); ok && !served[decl] {
				missing = append(missing, own{ad.Name, path})
			}
		}
	}
	sort.Slice(missing, func(i, j int) bool {
		if missing[i].pkg != missing[j].pkg {
			return missing[i].pkg < missing[j].pkg
		}
		return missing[i].name < missing[j].name
	})
	for _, m := range missing {
		c.report(d.Start, "O2", "adapter %s (%s) is named by no serve block, so nothing runs it; add it to one, or delete it (ADR 0132)", m.name, m.pkg)
	}
}

// applicationPlacement is O3 for a module's top-level files: an application in
// a library, which doesn't run, and a second application in one module.
func applicationPlacement(root string, sources []string, library bool) []Diagnostic {
	var out []Diagnostic
	first := ""
	for _, rel := range sources {
		if !declaresApplication(root, rel) {
			continue
		}
		switch {
		case library:
			out = append(out, Diagnostic{File: rel, Rule: "O3", Msg: "a library doesn't run, so it declares no application; an application belongs to the program that serves it (ADR 0132)"})
		case first != "":
			out = append(out, Diagnostic{File: rel, Rule: "O3", Msg: "a module has one application, and " + first + " already declares it (ADR 0132)"})
		default:
			first = rel
		}
	}
	return out
}

// declaresApplication reports whether the source file at root/rel declares an
// application, for the checks that depend on it before the file is checked.
func declaresApplication(root, rel string) bool {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	f, _ := parser.Parse(string(data))
	return DeclaresApplication(f)
}

// DeclaresApplication reports whether f declares an application (ADR 0132).
func DeclaresApplication(f *syntax.File) bool {
	for _, d := range f.Decls {
		if _, ok := d.(*syntax.ApplicationDecl); ok {
			return true
		}
	}
	return false
}
