// Package catalogcmd is catalog-cli, and `fiig catalog`: is the Milestone 3 prototype of `lang catalog`
// (fiig-lang.md §Milestone 3's example commands), built as its own small
// command rather than a `lang` subcommand since no umbrella CLI exists yet.
//
// Usage:
//
//	catalog-cli generate [--workspace D] <root>
//	catalog-cli find [--workspace D] [--text S] [--input T] [--output T] [--requires C] [--error E] [--pure] [--idempotent] [--workflows] [--leaf] [--deprecated] [--constant F=V] <root>
//	catalog-cli explain [--workspace D] <root> <name>
//	catalog-cli diff <oldRoot> <newRoot>
//
// --workspace resolves root's own fiig.pkg.json dependencies against
// every package found under D (compiler/loader) and merges every
// resolved package's own cards into one combined catalog
// (compiler/registry.Merge, Milestone 6's "catalog import and merge"),
// each card tagged with the canonical package it came from; omitted,
// only root's own declarations are catalogued, same as before this
// milestone.
//
// find and explain also search the prelude math group's members
// (registry.Prelude), which every file can call; generate and diff, which
// describe one package's own contract, leave them out.
package catalogcmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/manifest"
	"fiig-lang/compiler/registry"
	"fiig-lang/compiler/semver"
)

// stringList collects every occurrence of a repeatable flag, rather
// than keeping only the last one.
// prog is the name usage messages show: catalog-cli, or fiig catalog.
var prog = "catalog-cli"

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func Main(name string, args []string) {
	prog = name
	if len(args) < 1 {
		usage()
		os.Exit(2)
	}

	switch args[0] {
	case "generate":
		runGenerate(args[1:])
	case "find":
		runFind(args[1:])
	case "explain":
		runExplain(args[1:])
	case "diff":
		runDiff(args[1:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, strings.ReplaceAll(`usage:
  catalog-cli generate [--workspace D] <root>
  catalog-cli find [--workspace D] [--text S] [--input T] [--output T] [--requires C] [--error E] [--pure] [--idempotent] [--workflows] [--leaf] [--deprecated] [--constant F=V] <root>
  catalog-cli explain [--workspace D] <root> <name>
  catalog-cli diff <oldRoot> <newRoot>`, "catalog-cli", prog))
}

// reportFileErrors prints parse errors to stderr without stopping — a
// malformed file shouldn't hide the rest of the registry from the
// caller. This tool's own output is JSON-only already (no human-readable
// mode to begin with), so its errors are too — one JSON array of
// registry.FileError, matching the Card schema's own styling.
func reportFileErrors(fileErrs []registry.FileError) {
	if len(fileErrs) == 0 {
		return
	}
	enc := json.NewEncoder(os.Stderr)
	enc.SetIndent("", "  ")
	if err := enc.Encode(fileErrs); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// loadRegistry generates the registry for one package root alone, with
// no package context — unchanged from before Milestone 6's loader
// existed.
func loadRegistry(root string) []*registry.Card {
	cards, fileErrs, err := registry.Generate(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	reportFileErrors(fileErrs)
	return cards
}

// loadCatalog is loadRegistry's workspace-aware counterpart: when
// workspaces is empty, it behaves exactly like loadRegistry; otherwise
// it resolves root's dependencies via compiler/loader (searching every
// given root — an organization's own internal packages, e.g. a client
// library for an internal API, can live in their own directory tree
// rather than being forced into the main one) and merges every resolved
// package's own cards into one catalog (compiler/registry.Merge).
func loadCatalog(root string, workspaces []string) []*registry.Card {
	if !loader.Wanted(root, workspaces) {
		return loadRegistry(root)
	}
	loaded, errs := loader.Load(root, workspaces)
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "error:", e)
		}
		os.Exit(1)
	}
	sources := []registry.MergeSource{{Path: loaded.Manifest.Path, Version: loaded.Manifest.Version, Root: root}}
	for _, e := range loaded.Lockfile.Dependencies {
		source := registry.MergeSource{Path: e.Path, Version: e.Version, Root: e.Source}
		// A package fiig ships has no directory; its files are the
		// toolchain's (ADR 0074).
		if fsys, ok := loader.BuiltinFS(e.Source); ok {
			source.FS = fsys
		}
		sources = append(sources, source)
	}
	cards, fileErrs, err := registry.Merge(sources)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	reportFileErrors(fileErrs)
	return cards
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // generic types are full of < > — don't <-escape every one
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runGenerate(args []string) {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	var workspaces stringList
	fs.Var(&workspaces, "workspace", "directory to search for root's package dependencies; repeatable. Merges every resolved package's cards into one catalog")
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
		os.Exit(2)
	}
	printJSON(loadCatalog(fs.Arg(0), workspaces))
}

func runFind(args []string) {
	fs := flag.NewFlagSet("find", flag.ExitOnError)
	var workspaces stringList
	fs.Var(&workspaces, "workspace", "directory to search for root's package dependencies; repeatable. Merges every resolved package's cards into one catalog")
	text := fs.String("text", "", "substring match against declaration name")
	input := fs.String("input", "", "exact match against any parameter type")
	output := fs.String("output", "", "exact match against the return type")
	requires := fs.String("requires", "", "exact match against a gateway's required capability (the effect axis)")
	errType := fs.String("error", "", "exact match against the E in a Result<S, E> return type")
	pure := fs.Bool("pure", false, "only rules/conversions")
	idempotent := fs.Bool("idempotent", false, "only declarations explicitly declared idempotent")
	workflows := fs.Bool("workflows", false, "search the composition catalog (workflows, adapters) instead of the default one")
	leaf := fs.Bool("leaf", false, "only declarations with no dependencies (ADR 0030) — atomic building blocks")
	deprecated := fs.Bool("deprecated", false, "only declarations with a deprecation notice")
	constant := fs.String("constant", "", "field=value: the enum variants whose header constant matches exactly (ADR 0046), e.g. code=ACME")
	fs.Parse(args)

	if fs.NArg() != 1 {
		usage()
		os.Exit(2)
	}
	cards := append(loadCatalog(fs.Arg(0), workspaces), registry.Prelude()...)
	results := registry.Find(cards, registry.Query{
		Text: *text, Input: *input, Output: *output,
		Requires: *requires, Error: *errType, Pure: *pure, Idempotent: *idempotent,
		Composition: *workflows, Leaf: *leaf, Deprecated: *deprecated,
	})
	if *constant != "" {
		field, value, err := registry.ParseConstantQuery(*constant)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		printJSON(registry.FindConstant(results, field, value))
		return
	}
	printJSON(results)
}

func runExplain(args []string) {
	fs := flag.NewFlagSet("explain", flag.ExitOnError)
	var workspaces stringList
	fs.Var(&workspaces, "workspace", "directory to search for root's package dependencies; repeatable. Merges every resolved package's cards into one catalog")
	fs.Parse(args)
	if fs.NArg() != 2 {
		usage()
		os.Exit(2)
	}
	cards := append(loadCatalog(fs.Arg(0), workspaces), registry.Prelude()...)
	name := fs.Arg(1)
	card := registry.ByName(cards, name)
	if card == nil {
		fmt.Fprintf(os.Stderr, "no declaration named %q\n", name)
		os.Exit(1)
	}
	out := struct {
		*registry.Card
		Siblings []string `json:"sameSignatureSiblings,omitempty"`
		Callers  []string `json:"callers,omitempty"`
	}{Card: card, Siblings: registry.Siblings(cards, card), Callers: registry.Callers(cards, card)}
	printJSON(out)
}

// runDiff is Milestone 6's contract-level upgrade diff and version-bump
// policy check (ADR 0032 point 6): it generates both roots' registries,
// diffs them, and compares the bump the diff requires against the bump
// each root's own fiig.pkg.json actually declares — rather than trusting
// whichever number a manifest author happened to type in.
func runDiff(args []string) {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() != 2 {
		usage()
		os.Exit(2)
	}
	oldRoot, newRoot := fs.Arg(0), fs.Arg(1)

	oldManifest, mErrs := manifest.Load(oldRoot)
	if len(mErrs) > 0 {
		fmt.Fprintln(os.Stderr, "error loading", oldRoot, "manifest:", mErrs)
		os.Exit(1)
	}
	newManifest, mErrs := manifest.Load(newRoot)
	if len(mErrs) > 0 {
		fmt.Fprintln(os.Stderr, "error loading", newRoot, "manifest:", mErrs)
		os.Exit(1)
	}

	oldCards := loadRegistry(oldRoot)
	newCards := loadRegistry(newRoot)
	required, changes := registry.Diff(oldCards, newCards)

	oldVersion, err := semver.Parse(oldManifest.Version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	newVersion, err := semver.Parse(newManifest.Version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	bumpErr := registry.CheckBump(oldVersion, newVersion, required)

	out := struct {
		OldVersion   string            `json:"oldVersion"`
		NewVersion   string            `json:"newVersion"`
		RequiredBump string            `json:"requiredBump"`
		Changes      []registry.Change `json:"changes,omitempty"`
		OK           bool              `json:"ok"`
		Error        string            `json:"error,omitempty"`
	}{
		OldVersion:   oldManifest.Version,
		NewVersion:   newManifest.Version,
		RequiredBump: required.String(),
		Changes:      changes,
		OK:           bumpErr == nil,
	}
	if bumpErr != nil {
		out.Error = bumpErr.Error()
	}
	printJSON(out)
	if bumpErr != nil {
		os.Exit(1)
	}
}
