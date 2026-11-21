// Package lockfile implements ADR 0032's deliverable 7: the generated
// record of a build's actual resolved dependency versions and content
// hashes, as opposed to the manifest's own merely-declared minimums
// (compiler/manifest). It never decides policy itself — compiler/loader
// resolves the build and hands the outcome here to be written down, the
// same split go.sum/Cargo.lock each draw against their own module/crate
// resolver.
package lockfile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fiig-lang/compiler/version"
)

// Filename is the lockfile's fixed name at a package root.
const Filename = "fiig.lock.json"

// Entry is one resolved dependency: the canonical path it was resolved
// for, the exact on-disk version actually used (as opposed to the
// minimum any manifest declared), a content hash over its own source
// files, and the directory it was loaded from.
type Entry struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Hash    string `json:"hash,omitempty"` // none for a builtin (ADR 0138 §2)
	Source  string `json:"source"`

	// Capabilities is every capability name (a gateway's `requires`
	// clause) this specific dependency's own gateways require — recorded
	// per-entry so an auditor (or compiler/loader's own capability
	// check) can see exactly which dependency is responsible for a given
	// capability showing up in a build, not just that the build as a
	// whole touches it somewhere.
	Capabilities []string `json:"capabilities,omitempty"`

	// Origin is this dependency's own declared canonical source
	// location (compiler/manifest's Origin field), copied through
	// unchanged — an organization's internal client library is the
	// attested motivating case, so whoever reads the lockfile can see
	// where each dependency actually lives without re-deriving it from
	// every manifest in the closure by hand.
	Origin string `json:"origin,omitempty"`
}

// Lockfile is one build's full resolved dependency closure, sorted by
// Path so Write produces the same bytes across identical resolutions —
// the same determinism Milestone 3's registry.Generate already commits
// to for its own output.
type Lockfile struct {
	// Toolchain is the fiig release that wrote the file
	// (compiler/version), so a build's lockfile says which toolchain
	// resolved it. Write sets it.
	Toolchain    string  `json:"toolchain,omitempty"`
	Dependencies []Entry `json:"dependencies,omitempty"`
}

// HashFiles computes one package's content hash from its own source
// files, keyed by path relative to the package root. Hashing is over
// every (path, content) pair in sorted-path order, so file-system
// iteration order never changes the result — sha256, matching go.sum's
// own choice of hash algorithm for the same job.
func HashFiles(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		h.Write([]byte(p))
		h.Write([]byte{0})
		h.Write(files[p])
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// Write records lf at root's Filename, with this toolchain's version, sorting Dependencies by Path
// first so the written file is deterministic regardless of the order
// its caller resolved them in.
//
// A package the toolchain ships (Source "builtin:...") is written with no
// Hash: the Toolchain field pins its bytes, as Go keeps the standard
// library out of go.sum, so a newer toolchain changes the file only where
// it changes a dependency (ADR 0138 §2, Hearsay GAPS G62).
func Write(root string, lf *Lockfile) error {
	sorted := append([]Entry(nil), lf.Dependencies...)
	for i := range sorted {
		if strings.HasPrefix(sorted[i].Source, "builtin:") {
			sorted[i].Hash = ""
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	data, err := json.MarshalIndent(&Lockfile{Toolchain: version.Version, Dependencies: sorted}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(root, Filename), data, 0o644)
}

// Load reads the lockfile at root's Filename.
func Load(root string) (*Lockfile, error) {
	data, err := os.ReadFile(filepath.Join(root, Filename))
	if err != nil {
		return nil, err
	}
	var lf Lockfile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("%s: invalid JSON: %w", Filename, err)
	}
	return &lf, nil
}
