package registry

import (
	"fmt"
	"io/fs"
	"sort"
)

// MergeSource is one package's own canonical identity and the directory
// to generate its cards from — Path/Version typically come straight from
// that package's own fiig.pkg.json (compiler/manifest) or, for an
// already-resolved dependency, compiler/loader's own Lockfile entry.
type MergeSource struct {
	Path    string
	Version string
	Root    string
	// FS, when set, is the package's files, read in place of Root's: a
	// package built into the toolchain has no directory (ADR 0074).
	FS fs.FS
}

// Merge generates contract cards from every source and combines them
// into one catalog spanning a whole resolved build — Milestone 6's
// "catalog import and merge" deliverable. Each card is tagged with the
// package it came from (Card.Package/PackageVersion) before combining,
// giving every card the canonical identity docs/terminology.md's
// "Canonical identity" entry calls for, independent of whatever alias
// any particular consuming file happens to use for that package. Each
// source's own Dependencies are still computed only against that same
// source's own cards (Generate's existing behavior, unchanged) — a
// cross-package call is not resolved into a cross-package dependency
// edge here; see Generate's filterKnown doc for why that's deliberately
// out of scope rather than silently wrong.
func Merge(sources []MergeSource) ([]*Card, []FileError, error) {
	var all []*Card
	var allFileErrs []FileError
	for _, s := range sources {
		var cards []*Card
		var fileErrs []FileError
		var err error
		if s.FS != nil {
			cards, fileErrs, err = GenerateFS(s.FS)
		} else {
			cards, fileErrs, err = Generate(s.Root)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", s.Root, err)
		}
		for _, c := range cards {
			c.Package = s.Path
			c.PackageVersion = s.Version
		}
		all = append(all, cards...)
		for _, fe := range fileErrs {
			fe.Source = s.Path + ":" + fe.Source
			allFileErrs = append(allFileErrs, fe)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Package != all[j].Package {
			return all[i].Package < all[j].Package
		}
		if all[i].Name != all[j].Name {
			return all[i].Name < all[j].Name
		}
		return all[i].Source < all[j].Source
	})
	return all, allFileErrs, nil
}
