// Package migration reads a package's schema migrations (ADR 0096): the
// `migrations/<number>_<name>.sql` files, each one or more SQL statements,
// applied in number order and never edited once applied. This package only
// reads and splits them; tooling/migrate records and applies them.
package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Dir is the directory, at a package's root, that holds its migrations.
const Dir = "migrations"

// File is one migration.
type File struct {
	Number int
	Name   string // the file name without .sql: 0003_add_sync_state
	SQL    string
}

// Sum is the file's sha256, which the fiig_migrations table records so an
// edit after it ran is found (§3).
func (f File) Sum() string {
	h := sha256.Sum256([]byte(f.SQL))
	return hex.EncodeToString(h[:])
}

var fileName = regexp.MustCompile(`^([0-9]+)_[a-z0-9_]+\.sql$`)

// Read reads fsys's migrations/ directory, in number order. No directory
// is no migrations. A name that doesn't fit §1, a number used twice, and
// a file in a subdirectory are errors, all of them returned.
func Read(fsys fs.FS) ([]File, []error) {
	entries, err := fs.ReadDir(fsys, Dir)
	if err != nil {
		return nil, nil
	}
	var files []File
	var errs []error
	byNumber := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			errs = append(errs, fmt.Errorf("%s/%s: migrations are files directly in %s/, not in a subdirectory (ADR 0096 §1)", Dir, name, Dir))
			continue
		}
		m := fileName.FindStringSubmatch(name)
		if m == nil {
			errs = append(errs, fmt.Errorf("%s/%s: a migration is named <number>_<name>.sql, the name in lowercase letters, digits and underscores, such as 0003_add_sync_state.sql (ADR 0096 §1)", Dir, name))
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %s is too large a number", Dir, name, m[1]))
			continue
		}
		if other, taken := byNumber[n]; taken {
			errs = append(errs, fmt.Errorf("%s/%s and %s/%s have the same number, %d; give each migration its own (ADR 0096 §1)", Dir, other, Dir, name, n))
			continue
		}
		byNumber[n] = name
		data, err := fs.ReadFile(fsys, path.Join(Dir, name))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		files = append(files, File{Number: n, Name: strings.TrimSuffix(name, ".sql"), SQL: string(data)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Number < files[j].Number })
	return files, errs
}

// Statements splits sql at each `;` that ends a statement, outside quotes
// ('...', "..."), comments (-- and /* */), and Postgres dollar quotes
// ($tag$...$tag$), and drops the statements that are only space and
// comments. A driver takes one statement per call.
func Statements(sql string) []string {
	var out []string
	start := 0
	emit := func(end int) {
		if s := strings.TrimSpace(sql[start:end]); s != "" && !onlyComments(s) {
			out = append(out, s)
		}
	}
	for i := 0; i < len(sql); {
		switch c := sql[i]; {
		case c == '\'' || c == '"':
			i = quotedEnd(sql, i, c)
		case c == '-' && strings.HasPrefix(sql[i:], "--"):
			if nl := strings.IndexByte(sql[i:], '\n'); nl >= 0 {
				i += nl + 1
			} else {
				i = len(sql)
			}
		case c == '/' && strings.HasPrefix(sql[i:], "/*"):
			if end := strings.Index(sql[i+2:], "*/"); end >= 0 {
				i += 2 + end + 2
			} else {
				i = len(sql)
			}
		case c == '$':
			i = dollarEnd(sql, i)
		case c == ';':
			emit(i)
			i++
			start = i
		default:
			i++
		}
	}
	emit(len(sql))
	return out
}

// quotedEnd is the index after the quote that closes the one at i; a
// doubled quote is an escaped one.
func quotedEnd(sql string, i int, q byte) int {
	for j := i + 1; j < len(sql); j++ {
		if sql[j] == q {
			if j+1 < len(sql) && sql[j+1] == q {
				j++
				continue
			}
			return j + 1
		}
	}
	return len(sql)
}

// dollarEnd is the index after the dollar quote that closes the one
// opening at i, or i+1 when $ at i doesn't open one ($1, a parameter).
func dollarEnd(sql string, i int) int {
	j := i + 1
	for j < len(sql) && (sql[j] == '_' || sql[j] >= 'a' && sql[j] <= 'z' || sql[j] >= 'A' && sql[j] <= 'Z' || j > i+1 && sql[j] >= '0' && sql[j] <= '9') {
		j++
	}
	if j >= len(sql) || sql[j] != '$' {
		return i + 1
	}
	tag := sql[i : j+1]
	if end := strings.Index(sql[j+1:], tag); end >= 0 {
		return j + 1 + end + len(tag)
	}
	return len(sql)
}

func onlyComments(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "--") {
			return false
		}
	}
	return true
}
