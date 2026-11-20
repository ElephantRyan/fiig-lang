// Package callscmd is `fiig calls <file>` (ADR 0106): each declaration a
// file calls, with its kind and, when it has effects, the capabilities it
// reaches, so a reader or an LLM sees what a bare call does without
// opening every callee.
package callscmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fiig-lang/compiler/manifest"
	"fiig-lang/compiler/registry"
)

// Call is one declaration the file calls.
type Call struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Pure    bool     `json:"pure"`
	Effects []string `json:"effects,omitempty"` // the capabilities it reaches
	Source  string   `json:"source,omitempty"`
}

func Main(name string, argv []string) {
	os.Exit(Run(name, argv, os.Stdout, os.Stderr))
}

// Run is Main without the exit, for tests.
func Run(name string, argv []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "print the calls as a JSON array")
	rootFlag := fs.String("root", "", "the program's root; default: the nearest directory above the file with a fiig.pkg.json, else the current directory")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: "+name+" [-json] [-root DIR] <file.fiig>")
		return 2
	}
	calls, err := Of(fs.Arg(0), *rootFlag)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if *jsonOut {
		if calls == nil {
			calls = []Call{}
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(calls)
		return 0
	}
	width := 0
	for _, c := range calls {
		width = max(width, len(c.Name))
	}
	for _, c := range calls {
		effect := "pure"
		if !c.Pure {
			effect = "effects"
			if len(c.Effects) > 0 {
				effect += ": " + strings.Join(c.Effects, ", ")
			}
		}
		fmt.Fprintf(stdout, "%-*s  %-10s  %s\n", width, c.Name, c.Kind, effect)
	}
	return 0
}

// Of is the calls made by the declaration in file, found in the registry
// of the program at root ("" for the default).
func Of(file, root string) ([]Call, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	if root == "" {
		root = programRoot(filepath.Dir(abs))
	}
	if root, err = filepath.Abs(root); err != nil {
		return nil, err
	}
	cards, _, err := registry.Generate(root)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)
	var own *registry.Card
	for _, c := range cards {
		if c.Source == rel {
			own = c
			break
		}
	}
	if own == nil {
		return nil, fmt.Errorf("%s declares nothing the registry lists (is it under the package at %s?)", file, root)
	}
	var out []Call
	for _, name := range own.Dependencies {
		callee := registry.ByName(cards, name)
		if callee == nil {
			out = append(out, Call{Name: name, Kind: "unknown"})
			continue
		}
		c := Call{Name: name, Kind: callee.Kind, Pure: callee.Pure, Source: callee.Source}
		if !c.Pure {
			c.Effects = reached(cards, callee)
		}
		out = append(out, c)
	}
	return out, nil
}

// reached is every capability card's calls reach, through its
// dependencies, sorted.
func reached(cards []*registry.Card, card *registry.Card) []string {
	seen := map[string]bool{}
	caps := map[string]bool{}
	var walk func(*registry.Card)
	walk = func(c *registry.Card) {
		if c == nil || seen[c.Name] {
			return
		}
		seen[c.Name] = true
		if c.Requires != "" {
			caps[c.Requires] = true
		}
		for _, d := range c.Dependencies {
			walk(registry.ByName(cards, d))
		}
	}
	walk(card)
	out := make([]string, 0, len(caps))
	for c := range caps {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// programRoot is the nearest directory at or above dir with a manifest,
// else the current directory.
func programRoot(dir string) string {
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, manifest.Filename)); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return "."
}
