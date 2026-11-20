// Package layout is ADRs 0102, 0104 and 0105: a module is a folder with a
// manifest and a src/ folder; every folder under src/ is a package; test/
// mirrors src/ file for file, except test/scenarios/; and a source file is
// named after its declaration in TitleCase. This package says which files
// belong to which package and what breaks the rules; compiler/loader and
// the commands use it.
package layout

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// Src holds a module's program, Test its tests.
	Src  = "src"
	Test = "test"
	// Scenarios is the one folder under test/ outside the mirror, for a
	// test of a promise several entry points keep (ADR 0102 §3).
	Scenarios = "scenarios"
)

// IsModule reports whether fsys, a module's root, has a src/ folder.
func IsModule(fsys fs.FS) bool {
	info, err := fs.Stat(fsys, Src)
	return err == nil && info.IsDir()
}

// Package is one folder under src/: Folder is its path under src/ ("" for
// src/ itself, "ratings", "signin/password"), Sources its files and Tests
// its test files, each relative to the module root with forward slashes.
type Package struct {
	Folder  string
	Sources []string
	Tests   []string
}

// PathOf is the package path of folder in the module at modulePath.
func PathOf(modulePath, folder string) string {
	if folder == "" {
		return modulePath
	}
	return modulePath + "." + strings.ReplaceAll(folder, "/", ".")
}

var folderName = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// ValidFolder reports whether name can be one segment of a package folder:
// lowercase letters and digits, starting with a letter (ADR 0105).
func ValidFolder(name string) bool { return folderName.MatchString(name) }

// Packages is every package of the module in fsys, in folder order, with
// every way the module breaks the layout: a .fiig file outside src/ and
// test/, a folder name that isn't a path segment, a test file that mirrors
// no source file. A file's name is checked against its declaration by
// FileNameProblem, once it's parsed.
func Packages(fsys fs.FS) ([]*Package, []error) {
	byFolder := map[string]*Package{}
	var errs []error
	get := func(folder string) *Package {
		p := byFolder[folder]
		if p == nil {
			p = &Package{Folder: folder}
			byFolder[folder] = p
		}
		return p
	}
	get("")
	sources := map[string]bool{}
	var tests []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == "migrations" || strings.HasPrefix(d.Name(), ".") && p != "." {
				return fs.SkipDir
			}
			if under, ok := strings.CutPrefix(p, Src+"/"); ok {
				for _, seg := range strings.Split(under, "/") {
					if !folderName.MatchString(seg) {
						errs = append(errs, fmt.Errorf("%s: a package folder is named in lowercase letters and digits, starting with a letter, since its name is part of the package's path (ADR 0105)", p))
						return fs.SkipDir
					}
				}
				if under == Scenarios {
					errs = append(errs, fmt.Errorf("%s: scenarios is test/'s folder for cross-cutting tests (ADR 0102 §3); name this package something else", p))
					return fs.SkipDir
				}
				get(under)
			}
			return nil
		}
		if path.Ext(p) != ".fiig" {
			return nil
		}
		switch {
		case strings.HasPrefix(p, Src+"/"):
			sources[p] = true
			folder := path.Dir(strings.TrimPrefix(p, Src+"/"))
			if folder == "." {
				folder = ""
			}
			get(folder).Sources = append(get(folder).Sources, p)
		case strings.HasPrefix(p, Test+"/"):
			tests = append(tests, p)
		default:
			errs = append(errs, fmt.Errorf("%s: a module's code is under src/ and its tests under test/ (ADR 0102); fiig fmt -layout moves it", p))
		}
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}
	for _, t := range tests {
		under := strings.TrimPrefix(t, Test+"/")
		if under == Scenarios || strings.HasPrefix(under, Scenarios+"/") {
			get("").Tests = append(get("").Tests, t)
			continue
		}
		mirror := Src + "/" + under
		if !sources[mirror] {
			errs = append(errs, fmt.Errorf("%s: a test file mirrors a source file, and %s doesn't exist; name it after the file it tests, or put a test of several entry points in test/%s/ (ADR 0102 §3)", t, mirror, Scenarios))
			continue
		}
		folder := path.Dir(under)
		if folder == "." {
			folder = ""
		}
		get(folder).Tests = append(get(folder).Tests, t)
	}
	out := make([]*Package, 0, len(byFolder))
	for _, p := range byFolder {
		sort.Strings(p.Sources)
		sort.Strings(p.Tests)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Folder < out[j].Folder })
	return out, errs
}

// FileName is the file a declaration named name lives in (ADR 0104): the
// name with its first letter capitalised, and .fiig.
func FileName(name string) string {
	r, size := utf8.DecodeRuneInString(name)
	if r == utf8.RuneError {
		return name + ".fiig"
	}
	return string(unicode.ToUpper(r)) + name[size:] + ".fiig"
}

// FileNameProblem is what's wrong with a source file at p (module-relative)
// whose declaration is named decl, or "".
func FileNameProblem(p, decl string) string {
	want := FileName(decl)
	if path.Base(p) == want || decl == "" {
		return ""
	}
	return fmt.Sprintf("%s declares %s, so its file is %s (ADR 0104); fiig fmt -layout renames it", p, decl, path.Join(path.Dir(p), want))
}

// TopLevelProblem is what's wrong with a source file at p (module-relative)
// of an application: it sits directly in src/, where an application declares
// nothing (ADR 0108), or "". A library's top-level package is its public
// face, so the rule is the caller's to apply to applications only.
func TopLevelProblem(p string) string {
	if path.Dir(p) != Src {
		return ""
	}
	return fmt.Sprintf("an application's declarations live in a folder, one per domain (ADR 0108); move %s to src/<domain>/%s", p, path.Base(p))
}

// IsScenario reports whether a module-relative test file is in
// test/scenarios/.
func IsScenario(p string) bool {
	return strings.HasPrefix(p, Test+"/"+Scenarios+"/")
}
