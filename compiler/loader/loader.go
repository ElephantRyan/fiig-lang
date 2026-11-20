// Package loader implements Milestone 6's real disk-based dependency
// loader: given one package root and a workspace directory to search, it
// discovers every transitive dependency's manifest and source on disk,
// resolves each one's version by Minimum Version Selection within its own
// major-version track (ADR 0032), builds the fully-wired types.Packages
// CheckDir/Check need to resolve root's own files' imports, and records
// the resolution as a lockfile.
//
// There's no remote registry or fetch step — Milestone 6 doesn't call for
// one — so every dependency is expected to already exist somewhere under
// workspaceRoot, discovered the same way go.work discovers a workspace's
// own member modules: by its own fiig.pkg.json, not a separate workspace
// manifest file nothing in the corpus attests.
package loader

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"

	"fiig-lang/compiler/capability"
	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/lockfile"
	"fiig-lang/compiler/manifest"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/semver"
	"fiig-lang/compiler/stdlib"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// Loaded is one Load call's result: root's own manifest, the fully
// resolved and built dependency closure root's own files need to resolve
// their imports against (keyed by canonical path, exactly
// resolveFileImports' shape), the lockfile recording what was actually
// resolved, and every capability (a gateway's `requires` clause) found
// anywhere in the build — root's own gateways plus every dependency's,
// already checked against root's manifest's own declared allowlist by
// the time Load returns successfully (Milestone 6's security exit
// criterion: "Imported gateways cannot gain undeclared capabilities").
type Loaded struct {
	Manifest     *manifest.Manifest
	Packages     types.Packages
	Lockfile     *lockfile.Lockfile
	Capabilities []string
}

// discovered is one package found while scanning the workspace, or built
// into the toolchain (ADR 0074 §1). dir names it in errors and in the
// lockfile; fsys holds its files.
type discovered struct {
	manifest *manifest.Manifest
	dir      string
	fsys     fs.FS
}

// reqKey identifies one major-version track of one package path — ADR
// 0032's unit of resolution: MVS unifies within a major, but two majors
// of the same path are allowed to coexist as distinct built instances.
type reqKey struct {
	path  string
	major int
}

func (k reqKey) String() string { return fmt.Sprintf("%s (major %d)", k.path, k.major) }

// Load resolves root's own manifest's dependency closure against every
// package found under any of workspaceRoots — an organization's own
// internal packages (e.g. a client library for an internal API) rarely
// live in the same directory tree as everything else a build depends
// on, so more than one root can be searched rather than forcing
// everything into one shared tree. The same package path discovered
// under two different roots at the same major is still the one
// ambiguous-install error validateInstalled already reports; which root
// it came from doesn't make two conflicting installs any less
// conflicting.
func Load(root string, workspaceRoots []string) (*Loaded, []error) {
	rootManifest, errs := manifest.Load(root)
	if len(errs) > 0 {
		return nil, errs
	}

	if reserved(rootManifest.Path) {
		return nil, []error{reservedError(root, rootManifest.Path)}
	}
	rootFiles, _, err := parsePackageDir(os.DirFS(root), root)
	if err != nil {
		return nil, []error{err}
	}
	rootCapabilities := gatewayCapabilities(rootFiles, rootManifest.Dependencies)

	index, errs := scanBuiltin()
	if len(errs) > 0 {
		return nil, errs
	}
	for _, workspaceRoot := range workspaceRoots {
		rootIndex, rootErrs := scanWorkspace(workspaceRoot)
		if len(rootErrs) > 0 {
			return nil, rootErrs
		}
		for path, found := range rootIndex {
			index[path] = append(index[path], found...)
		}
	}

	reqs := map[reqKey]semver.Version{}
	if errs := collectRequirements(rootManifest, index, reqs); len(errs) > 0 {
		return nil, errs
	}
	if errs := validateInstalled(reqs, index); len(errs) > 0 {
		return nil, errs
	}

	l := &loaderState{index: index, built: map[reqKey]*types.Package{}, entries: map[reqKey]lockfile.Entry{}, building: map[reqKey]bool{}}
	for key := range reqs {
		if _, errs := l.build(key); len(errs) > 0 {
			return nil, errs
		}
	}

	packages := types.Packages{}
	for depPath, minStr := range rootManifest.Dependencies {
		minV, _ := semver.Parse(minStr) // already validated by manifest.Validate
		packages[depPath] = l.built[reqKey{depPath, minV.Major}]
	}

	entryList := make([]lockfile.Entry, 0, len(l.entries))
	for _, e := range l.entries {
		entryList = append(entryList, e)
	}
	sort.Slice(entryList, func(i, j int) bool { return entryList[i].Path < entryList[j].Path })

	if errs := checkCapabilities(string(rootManifest.Kind), rootManifest.Capabilities, rootManifest.NativeCapabilities, rootCapabilities, entryList); len(errs) > 0 {
		return nil, errs
	}
	if errs := checkRenames(rootManifest, packages); len(errs) > 0 {
		return nil, errs
	}

	capabilities := map[string]bool{}
	for _, c := range rootCapabilities {
		capabilities[c] = true
	}
	for _, e := range entryList {
		for _, c := range e.Capabilities {
			capabilities[c] = true
		}
	}

	return &Loaded{
		Manifest:     rootManifest,
		Packages:     packages,
		Lockfile:     &lockfile.Lockfile{Dependencies: entryList},
		Capabilities: sortedKeys(capabilities),
	}, nil
}

// gatewayCapabilities collects the deduplicated, sorted set of
// capability names (`requires` clauses) any gateway among files
// declares — the per-package half of the security exit criterion;
// Milestone 6's capability check is this set compared against a
// manifest's own declared allowlist, not a new mechanism of its own.
//
// Since ADR 0043 a `requires` may name a capability group written in fiig,
// local or imported by path tail; those are left out. A group is fiig code
// whose own gateways' requirements are collected where they are declared —
// here for a local group, in its own package's lockfile entry for an
// imported one — so the allowlist is checked over the primitives a build
// reaches transitively, and naming a group never grants what is behind it.
// deps is the package's manifest dependencies, to tell a path-tail group
// import from a package import.
func gatewayCapabilities(files []*syntax.File, deps map[string]string) []string {
	local := map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			if g, ok := d.(*syntax.CapabilityDecl); ok {
				local[g.Name] = true
			}
		}
	}
	caps := map[string]bool{}
	for _, f := range files {
		groups := map[string]bool{}
		for _, imp := range f.Imports {
			if n := len(imp.Path); n >= 2 {
				_, isPackage := deps[strings.Join(imp.Path, ".")]
				_, parentIsPackage := deps[strings.Join(imp.Path[:n-1], ".")]
				alias := imp.Alias
				if alias == "" {
					alias = imp.Path[n-1]
				}
				// A required package is a domain the gateway calls into (ADR
				// 0079 §1), not a capability; what it reaches is collected in
				// its own lockfile entry, like a group's.
				if isPackage || (!isPackage && parentIsPackage) {
					groups[alias] = true
				}
			}
		}
		add := func(g *syntax.GatewayDecl) {
			if g.Requires != "" && !local[g.Requires] && !groups[g.Requires] {
				caps[g.Requires] = true
			}
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *syntax.GatewayDecl:
				add(d)
			case *syntax.CapabilityDecl:
				for _, g := range d.Gateways {
					add(g)
				}
			}
		}
	}
	return sortedKeys(caps)
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// checkCapabilities enforces Milestone 6's security exit criterion —
// "Imported gateways cannot gain undeclared capabilities" — against a
// fully resolved build: every capability rootCapabilities or any
// entries lists must appear in declared (the application's own manifest
// allowlist), or it's reported by name together with exactly which
// package introduced it, so the fix is always "add it to the manifest"
// or "stop depending on whatever actually needs it," never a mystery.
//
// It also enforces ADR 0042's closed floor (bug class H2): a required
// capability must either be one of the primitives or be named in native
// (the manifest's NativeCapabilities grandfather list). Otherwise a new
// Go-only domain capability could slip in, which is how the floor ended up
// at the domain layer in the first place.
func checkCapabilities(kind string, declared, native, rootCapabilities []string, entries []lockfile.Entry) []error {
	declaredSet := map[string]bool{}
	for _, c := range declared {
		declaredSet[c] = true
	}
	nativeSet := map[string]bool{}
	for _, c := range native {
		nativeSet[c] = true
	}
	var errs []error
	check := func(c, who string) {
		if !declaredSet[c] {
			errs = append(errs, fmt.Errorf("capability %q is required by %s but not declared in this %s's manifest; add %q to \"capabilities\" in its fiig.pkg.json", c, who, kind, c))
		}
		if !capability.IsPrimitive(c) && !nativeSet[c] {
			errs = append(errs, fmt.Errorf("capability %q (required by %s) is neither a primitive (%s) nor listed in the manifest's nativeCapabilities; a domain capability needs a fiig fulfiller (ADR 0042)", c, who, strings.Join(capability.Primitives(), ", ")))
		}
	}
	for _, c := range rootCapabilities {
		check(c, "this package's own gateways")
	}
	for _, e := range entries {
		for _, c := range e.Capabilities {
			check(c, fmt.Sprintf("dependency %q", e.Path))
		}
	}
	return errs
}

// scanWorkspace finds every package under root by its own fiig.pkg.json,
// grouping discoveries by canonical path (the same path can legitimately
// appear more than once, at different major versions — ADR 0032's
// coexistence). A package's own subtree is never descended into looking
// for further nested packages, since fiig has no notion of one package
// living inside another.
func scanWorkspace(root string) (map[string][]discovered, []error) {
	index := map[string][]discovered{}
	var errs []error
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if _, statErr := os.Stat(filepath.Join(path, manifest.Filename)); statErr != nil {
			return nil
		}
		m, mErrs := manifest.Load(path)
		if len(mErrs) > 0 {
			for _, e := range mErrs {
				errs = append(errs, fmt.Errorf("%s: %w", path, e))
			}
			return filepath.SkipDir
		}
		if reserved(m.Path) {
			errs = append(errs, reservedError(path, m.Path))
			return filepath.SkipDir
		}
		index[m.Path] = append(index[m.Path], discovered{manifest: m, dir: path, fsys: os.DirFS(path)})
		return filepath.SkipDir
	})
	if err != nil {
		errs = append(errs, err)
	}
	return index, errs
}

// Wanted reports whether root should be loaded as a package even with no
// workspace roots: it has a manifest that names dependencies, which may be
// built into the toolchain (ADR 0074 §1), and which, when they aren't,
// deserve the loader's "not found" rather than an unresolved import.
func Wanted(root string, workspaceRoots []string) bool {
	if len(workspaceRoots) > 0 {
		return true
	}
	m, errs := manifest.Load(root)
	return len(errs) == 0 && len(m.Dependencies) > 0
}

// BuiltinPrefix starts the path of every package the toolchain ships, and
// of no other (ADR 0074 §1), as Java reserves java.*.
const BuiltinPrefix = "fiig."

func reserved(path string) bool {
	return path == "fiig" || strings.HasPrefix(path, BuiltinPrefix)
}

func reservedError(dir, path string) error {
	return fmt.Errorf("%s: package path %q starts with %q, which is reserved for packages the fiig toolchain ships (ADR 0074 §1); choose another path", filepath.ToSlash(dir), path, BuiltinPrefix)
}

// scanBuiltin indexes the packages built into the toolchain (stdlib.FS),
// which every Load searches as if they were one more workspace root. Each
// is named builtin:<dir> in errors and the lockfile.
func scanBuiltin() (map[string][]discovered, []error) {
	index := map[string][]discovered{}
	var errs []error
	err := fs.WalkDir(stdlib.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		data, readErr := fs.ReadFile(stdlib.FS, pathpkg.Join(path, manifest.Filename))
		if readErr != nil {
			return nil
		}
		name := "builtin:" + path
		m, mErrs := manifest.Parse(data)
		for _, e := range mErrs {
			errs = append(errs, fmt.Errorf("%s: %w", name, e))
		}
		if len(mErrs) == 0 {
			sub, _ := fs.Sub(stdlib.FS, path)
			index[m.Path] = append(index[m.Path], discovered{manifest: m, dir: name, fsys: sub})
		}
		return fs.SkipDir
	})
	if err != nil {
		errs = append(errs, err)
	}
	return index, errs
}

// filterMajor narrows candidates sharing path to the ones at a specific
// major version.
func filterMajor(candidates []discovered, major int) []discovered {
	var out []discovered
	for _, c := range candidates {
		v, err := semver.Parse(c.manifest.Version)
		if err == nil && v.Major == major {
			out = append(out, c)
		}
	}
	return out
}

// collectRequirements walks start's own transitive manifest graph,
// recording the maximum minimum version requested for each (path, major)
// pair encountered anywhere in the closure — the "maximum of every
// minimum" half of MVS (ADR 0032 point 3). Recursion into a dependency's
// own manifest only happens once per (path, major) regardless of how many
// minimums are requested for it, since the installed manifest's own
// further dependencies don't depend on which minimum triggered the visit —
// there's only one copy on disk per major.
func collectRequirements(start *manifest.Manifest, index map[string][]discovered, reqs map[reqKey]semver.Version) []error {
	visited := map[reqKey]bool{}
	var errs []error
	var walk func(m *manifest.Manifest)
	walk = func(m *manifest.Manifest) {
		for depPath, minStr := range m.Dependencies {
			minV, err := semver.Parse(minStr)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: dependency %q: %w", m.Path, depPath, err))
				continue
			}
			key := reqKey{depPath, minV.Major}
			if existing, ok := reqs[key]; !ok || minV.Compare(existing) > 0 {
				reqs[key] = minV
			}
			if visited[key] {
				continue
			}
			visited[key] = true
			cands := filterMajor(index[depPath], minV.Major)
			if len(cands) != 1 {
				continue // validateInstalled reports the precise not-found/ambiguous error
			}
			walk(cands[0].manifest)
		}
	}
	walk(start)
	return errs
}

// validateInstalled checks every requirement MVS settled on against what
// actually exists on disk: exactly one candidate at that major, whose own
// declared version is at least the resolved minimum.
func validateInstalled(reqs map[reqKey]semver.Version, index map[string][]discovered) []error {
	var errs []error
	for key, minV := range reqs {
		cands := filterMajor(index[key.path], key.major)
		switch len(cands) {
		case 0:
			errs = append(errs, fmt.Errorf("dependency %s needs >= %s, but nothing at that major was found under the workspace or built into fiig", key, minV))
		case 1:
			installed, err := semver.Parse(cands[0].manifest.Version)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: invalid version %q: %w", cands[0].dir, cands[0].manifest.Version, err))
				continue
			}
			if installed.Compare(minV) < 0 {
				errs = append(errs, fmt.Errorf("dependency %s needs >= %s, but %s only has %s installed", key, minV, cands[0].dir, installed))
			}
		default:
			dirs := make([]string, len(cands))
			for i, c := range cands {
				dirs[i] = c.dir
			}
			errs = append(errs, fmt.Errorf("ambiguous dependency %s: found at %s", key, strings.Join(dirs, ", ")))
		}
	}
	return errs
}

// loaderState carries the mutable state one Load call threads through its
// recursive, memoized package construction.
type loaderState struct {
	index    map[string][]discovered
	built    map[reqKey]*types.Package
	entries  map[reqKey]lockfile.Entry
	building map[reqKey]bool
}

// build constructs key's own *types.Package, recursively building
// whatever it depends on first (post-order — a dependency must exist
// before checker.BuildPackage can resolve the dependent's imports against
// it) and memoizing the result so two different dependents sharing one
// (path, major) get the exact same built instance, not two independent
// copies — the type-identity guarantee MVS unification exists for.
func (l *loaderState) build(key reqKey) (*types.Package, []error) {
	if pkg, ok := l.built[key]; ok {
		return pkg, nil
	}
	if l.building[key] {
		return nil, []error{fmt.Errorf("dependency cycle involving %s", key)}
	}
	l.building[key] = true
	defer delete(l.building, key)

	cands := filterMajor(l.index[key.path], key.major) // exactly one, per validateInstalled
	inst := cands[0]

	files, hashed, err := parsePackageDir(inst.fsys, inst.dir)
	if err != nil {
		return nil, []error{err}
	}

	depPackages := types.Packages{}
	for depPath, minStr := range inst.manifest.Dependencies {
		minV, _ := semver.Parse(minStr) // already validated
		depPkg, errs := l.build(reqKey{depPath, minV.Major})
		if len(errs) > 0 {
			return nil, errs
		}
		depPackages[depPath] = depPkg
	}

	pkg, _ := checker.BuildPackage(files, depPackages)
	// A dependency is checked like the root, not only built: a program that
	// doesn't check never runs, and checking is also what gives its
	// constants their types (ADR 0048 §2.4). CheckPackage reports
	// BuildPackage's name conflicts too, so they come from here once.
	diags := checker.CheckPackage(files, depPackages, key.path, inst.manifest.Provides)
	if len(diags) > 0 {
		errs := make([]error, len(diags))
		for i, d := range diags {
			// Without its package a dependency's line number points nowhere.
			errs[i] = fmt.Errorf("package %s (%s): %s", key.path, filepath.ToSlash(inst.dir), d)
		}
		return nil, errs
	}

	pkg.Path = key.path
	pkg.Provides = inst.manifest.Provides
	l.built[key] = pkg
	l.entries[key] = lockfile.Entry{
		Path:         key.path,
		Version:      inst.manifest.Version,
		Hash:         lockfile.HashFiles(hashed),
		Source:       filepath.ToSlash(inst.dir),
		Capabilities: gatewayCapabilities(files, inst.manifest.Dependencies),
		Origin:       inst.manifest.Origin,
	}
	return pkg, nil
}

// parsePackageDir parses every .fiig file in fsys, recursively, and
// collects each one's source keyed by its path in fsys for
// lockfile.HashFiles. dir names the package in errors.
func parsePackageDir(fsys fs.FS, dir string) ([]*syntax.File, map[string][]byte, error) {
	var files []*syntax.File
	hashed := map[string][]byte{}
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path == "tests" {
			return fs.SkipDir // a dependency's tests aren't part of it (ADR 0091 §2)
		}
		if d.IsDir() || pathpkg.Ext(path) != ".fiig" {
			return nil
		}
		src, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		file, parseErrs := parser.Parse(string(src))
		if len(parseErrs) > 0 {
			// One line per error, each naming its file, as the checker's do.
			name := filepath.ToSlash(filepath.Join(dir, path))
			lines := make([]string, len(parseErrs))
			for i, e := range parseErrs {
				lines[i] = name + ":" + e.String()
			}
			return errors.New(strings.Join(lines, "\n"))
		}
		files = append(files, file)
		hashed[path] = src
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return files, hashed, nil
}

// checkRenames is ADR 0071 §5: an application may rename only a variable
// one of the package's config declarations reads, so a rename a later
// version made stale fails loudly instead of doing nothing.
func checkRenames(m *manifest.Manifest, packages types.Packages) []error {
	var errs []error
	for _, path := range sortedKeys(boolKeys(m.Environment)) {
		reads := map[string]bool{}
		if pkg := packages[path]; pkg != nil {
			for _, d := range pkg.Universe.Decls {
				if cd, ok := d.(*syntax.ConfigDecl); ok {
					for _, f := range cd.Fields {
						reads[f.Env] = true
					}
				}
			}
		}
		for _, from := range sortedKeys(boolKeys(m.Environment[path])) {
			if !reads[from] {
				errs = append(errs, fmt.Errorf("%s renames %s's variable %s, which none of its config declarations reads", manifest.Filename, path, from))
			}
		}
	}
	return errs
}

func boolKeys[V any](m map[string]V) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}
