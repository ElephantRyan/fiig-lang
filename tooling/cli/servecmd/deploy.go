package servecmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// deployID names the deploy this process belongs to, so a startup route that
// says `once` runs once per deploy and not once ever (ADR 0124 §6). Replicas
// of one deploy must agree on it and a new deploy must not, which no
// timestamp or process id can give.
//
// $FIIG_DEPLOY, when set, is the name: a release tag or a commit, for a team
// that wants a redeploy of unchanged code to run its routes again. Otherwise
// it is a hash of the program's files under root (.fiig, .sql and .json,
// with line endings normalised), so the same code is the same deploy and
// changed code is a new one.
func deployID(root string, lookup func(string) (string, bool)) (int64, error) {
	h := sha256.New()
	if v, ok := lookup("FIIG_DEPLOY"); ok && v != "" {
		h.Write([]byte(v))
		return idOf(h.Sum(nil)), nil
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".fiig", ".sql", ".json":
		default:
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		h.Write([]byte(filepath.ToSlash(rel)))
		h.Write([]byte{0})
		h.Write(bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n")))
		h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return 0, err
	}
	return idOf(h.Sum(nil)), nil
}

// idOf is the first eight bytes of sum, as a non-negative int.
func idOf(sum []byte) int64 {
	return int64(binary.BigEndian.Uint64(sum[:8]) & math.MaxInt64)
}
