package fmtcmd

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"fiig-lang/compiler/capability"
	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
	"fiig-lang/tooling/runprogram"
)

// typedOrs is the checker's verdict on every `or` in the modules paths
// belong to, by absolute file path and then byte offset: true for a
// default, which `fiig fmt` writes `??`, and false for a boolean `or`,
// written `||` (ADR 0142 §5). Each module is type-checked once, as `fiig
// check` does it but without writing a lockfile. A module that fails to
// load gives no verdicts, so its `or`s are reported as left alone.
func typedOrs(paths []string, stops map[string]string, workspaces []string) map[string]map[int]bool {
	out := map[string]map[int]bool{}
	done := map[string]bool{}
	for _, path := range paths {
		root := moduleRoot(path, stops[path])
		if done[root] {
			continue
		}
		done[root] = true
		files, rels := checkModule(root, workspaces)
		for i, f := range files {
			abs := resolve(root, rels[i])
			syntax.Inspect(f, func(n any) {
				b, ok := n.(*syntax.BinaryExpr)
				if !ok || b.Op != syntax.OpOr || b.Symbol || !b.OrKnown {
					return
				}
				if out[abs] == nil {
					out[abs] = map[int]bool{}
				}
				out[abs][b.Start.Offset] = b.OrDefault
			})
		}
	}
	return out
}

// checkModule type-checks the module or directory at root and returns its
// checked files with their paths, relative to root.
func checkModule(root string, workspaces []string) ([]*syntax.File, []string) {
	if path := manifestPath(root); strings.HasPrefix(path, "fiig.") {
		return checkShipped(root, path, workspaces)
	}
	return checkAt(root, workspaces)
}

// checkShipped checks a package fiig ships, whose `fiig.` path only the
// toolchain may load (ADR 0074 §1): a copy of it, renamed to `fiix.`, the
// same length, so every byte offset in its files is unchanged.
func checkShipped(root, path string, workspaces []string) ([]*syntax.File, []string) {
	tmp, err := os.MkdirTemp("", "fiig-fmt-")
	if err != nil {
		return nil, nil
	}
	defer os.RemoveAll(tmp)
	renamed := "fiix." + strings.TrimPrefix(path, "fiig.")
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		switch {
		case rel == "fiig.pkg.json":
			// As the standard library's own test does, the copy may use every
			// primitive capability; a shipped package lists none it uses.
			var m map[string]any
			if json.Unmarshal(data, &m) != nil {
				return nil
			}
			m["path"] = renamed
			m["capabilities"] = capability.Primitives()
			data, _ = json.Marshal(m)
		case filepath.Ext(p) == ".fiig":
			data = []byte(strings.ReplaceAll(string(data), "use "+path, "use "+renamed))
		}
		dest := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
	if err != nil {
		return nil, nil
	}
	return checkAt(tmp, workspaces)
}

// manifestPath is the package path in root's fiig.pkg.json, or "".
func manifestPath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "fiig.pkg.json"))
	if err != nil {
		return ""
	}
	var m struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(data, &m) != nil {
		return ""
	}
	return m.Path
}

func checkAt(root string, workspaces []string) ([]*syntax.File, []string) {
	var packages types.Packages
	var loaded *loader.Loaded
	if loader.Wanted(root, workspaces) {
		var errs []error
		loaded, errs = loader.Load(root, workspaces)
		if len(errs) > 0 {
			return nil, nil
		}
		packages = loaded.Packages
	}
	_, rels, files, err := checker.CheckDirFiles(root, packages)
	if err != nil {
		return nil, nil
	}
	if loaded != nil {
		if _, checked, err := runprogram.CheckModulePackages(root, loaded); err == nil {
			for _, c := range checked {
				files = append(files, c.Files...)
				rels = append(rels, c.Paths...)
			}
		}
	}
	return files, rels
}

// moduleRoot is the nearest directory at or above path holding a
// fiig.pkg.json. Without one, it is stop, the directory the path was found
// under on the command line (a loose example checks as one directory), or
// path's own directory for a file named directly.
func moduleRoot(path, stop string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Dir(path)
	}
	dir := filepath.Dir(abs)
	fallback := dir
	if stop != "" {
		fallback = absPath(stop)
	}
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "fiig.pkg.json")); err == nil {
			return d
		}
		if d == fallback || filepath.Dir(d) == d {
			return fallback
		}
	}
}

// resolve is a checked file's absolute path: rel under root, or rel itself
// when the checker reported it whole.
func resolve(root, rel string) string {
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel)
	}
	return filepath.Join(root, rel)
}

// absPath is path made absolute, for looking it up in typedOrs's result.
func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}
