// Package schemacheck is `fiig check -schema` (ADR 0131): the program's
// migrations are applied to a new in-memory database, and every Database call
// whose SQL is a literal is prepared against it, so a misspelt table or column
// is found at check time and not at the first request.
package schemacheck

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
	"fiig-lang/tooling/migrate"
)

// dbSeq numbers the in-memory databases a process makes.
var dbSeq atomic.Int64

// Query is one Database call with a literal SQL text.
type Query struct {
	File   string
	At     lexer.Pos
	Method string
	SQL    string
}

// Validator is what a database provider offers to have a statement prepared.
// libraries/sql's Provider does.
type Validator interface {
	Validate(ctx context.Context, query string) error
}

// Queries is every `Database.queryOne`, `queryMany` and `execute` in files
// whose first argument is a string with no interpolation (Q1 refuses the
// rest). paths names each file, parallel to files.
func Queries(files []*syntax.File, paths []string) []Query {
	var out []Query
	for i, f := range files {
		path := ""
		if i < len(paths) {
			path = paths[i]
		}
		visit := func(e syntax.Expr) {
			call, ok := e.(*syntax.CallExpr)
			if !ok || len(call.Args) == 0 {
				return
			}
			fa, ok := call.Callee.(*syntax.FieldAccess)
			if !ok {
				return
			}
			target, ok := fa.Target.(*syntax.Ident)
			if !ok || target.Name != types.DatabaseName {
				return
			}
			switch fa.Field {
			case "queryOne", "queryMany", "execute":
			default:
				return
			}
			lit, ok := call.Args[0].Value.(*syntax.StringLit)
			if !ok {
				return
			}
			var sql strings.Builder
			for _, part := range lit.Parts {
				if part.Interp != nil {
					return
				}
				sql.WriteString(part.Text)
			}
			start, _ := call.Args[0].Value.(syntax.Spanned).Bounds()
			out = append(out, Query{File: path, At: start, Method: fa.Field, SQL: sql.String()})
		}
		for _, body := range bodiesOf(f) {
			syntax.WalkBlockExprs(body, visit)
		}
	}
	return out
}

// bodiesOf is every block of code in f: its public declaration's, a
// capability group's gateways', and its private helpers'.
func bodiesOf(f *syntax.File) []*syntax.Block {
	var out []*syntax.Block
	add := func(d syntax.Decl) {
		switch d := d.(type) {
		case *syntax.RuleDecl:
			out = append(out, d.Body)
		case *syntax.ConversionDecl:
			out = append(out, d.Body)
		case *syntax.ActivityDecl:
			out = append(out, d.Body)
		case *syntax.GatewayDecl:
			out = append(out, d.Body)
		case *syntax.WorkflowDecl:
			out = append(out, d.Body)
		case *syntax.HelperDecl:
			out = append(out, d.Body)
		case *syntax.CapabilityDecl:
			for _, g := range d.Gateways {
				out = append(out, g.Body)
			}
		}
	}
	for _, d := range f.Decls {
		add(d)
	}
	if f.Private != nil {
		for _, m := range f.Private.Members {
			add(m)
		}
	}
	return out
}

// schemaProblems are the prepare errors that say the schema doesn't have
// what the query names. Any other refusal may be SQL the check's SQLite
// doesn't speak (Postgres's), and is counted, not reported.
var schemaProblems = []string{"no such table", "no such column", "has no column named", "ambiguous column name"}

// Result is what a schema check found.
type Result struct {
	Diagnostics []checker.Diagnostic
	// Note says what wasn't checked and why: the migrations didn't apply to
	// SQLite, or some queries use SQL it can't prepare. "" when everything was.
	Note string
}

// Check applies the program's migrations to a new in-memory database from
// factories["sql"], then prepares each query against it. root and packages
// are the program's, for its migrations and its dependencies'.
func Check(ctx context.Context, root string, packages types.Packages, queries []Query, factories runtime.FactoryRegistry) (Result, error) {
	var res Result
	if len(queries) == 0 {
		return res, nil
	}
	f, ok := factories["sql"]
	if !ok {
		return res, fmt.Errorf("this toolchain has no SQL driver to check queries with")
	}
	// A name of its own: an in-memory database lives as long as any
	// connection to it, so a second check in this process must not see the
	// first's tables.
	db, err := f(map[string]any{"type": "sql", "driver": "sqlite", "dsn": fmt.Sprintf("file:fiig-schema-%d-%d?mode=memory&cache=shared", os.Getpid(), dbSeq.Add(1))})
	if err != nil {
		return res, err
	}
	if c, ok := db.(interface{ Close() }); ok {
		defer c.Close()
	}
	v, ok := db.(Validator)
	if !ok {
		return res, fmt.Errorf("this toolchain's SQL provider can't prepare a statement")
	}
	migrations, errs := migrate.Collect(root, packages)
	if len(errs) > 0 {
		return res, errs[0]
	}
	files := 0
	for _, p := range migrations {
		files += len(p.Files)
	}
	if files == 0 {
		// A program that makes its tables in a gateway has no schema in files
		// to check against, and every query would look wrong.
		res.Note = "queries were not checked against the schema: the program has no migrations (ADR 0096)"
		return res, nil
	}
	if _, problems := migrate.RunOn(ctx, db, migrations, migrate.Apply); len(problems) > 0 {
		res.Note = "queries were not checked against the schema: the migrations don't apply to SQLite (" + strings.Join(problems, "; ") + ")"
		return res, nil
	}
	skipped := 0
	for _, q := range queries {
		err := v.Validate(ctx, q.SQL)
		if err == nil {
			continue
		}
		if !isSchemaProblem(err.Error()) {
			skipped++
			continue
		}
		res.Diagnostics = append(res.Diagnostics, checker.Diagnostic{
			File: q.File, At: q.At, Rule: "Q4",
			Msg: fmt.Sprintf("Database.%s's query doesn't fit the schema the migrations make: %s (ADR 0131)", q.Method, err),
		})
	}
	if skipped > 0 {
		res.Note = fmt.Sprintf("%d of %d queries use SQL that SQLite can't prepare (Postgres's, say) and were not checked", skipped, len(queries))
	}
	return res, nil
}

func isSchemaProblem(msg string) bool {
	for _, p := range schemaProblems {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}
