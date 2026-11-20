package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"fiig-lang/compiler/migration"
	"fiig-lang/compiler/runtime"
	fiigsql "fiig-lang/libraries/sql"
	"fiig-lang/tooling/migrate"
)

// ADR 0096 on a real SQLite database: apply once, record, refuse an edit,
// a missing file, and an out-of-order one, and roll a failed migration
// back whole.
func TestMigrationsOnSQLite(t *testing.T) {
	db, err := fiigsql.Factory(map[string]any{"type": "sql", "driver": "sqlite", "dsn": fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	file := func(n int, name, sql string) migration.File {
		return migration.File{Number: n, Name: fmt.Sprintf("%04d_%s", n, name), SQL: sql}
	}
	create := file(1, "create", "CREATE TABLE rating (id TEXT PRIMARY KEY);\nCREATE INDEX rating_by_id ON rating (id);")
	addNote := file(2, "add_note", "ALTER TABLE rating ADD COLUMN note TEXT NOT NULL DEFAULT ''")
	app := func(files ...migration.File) []migrate.Package {
		return []migrate.Package{{Path: "app", Files: files}}
	}

	applied, problems := migrate.RunOn(ctx, db, app(create), migrate.Apply)
	if len(problems) > 0 || len(applied) != 1 || applied[0] != "app 0001_create" {
		t.Fatalf("first run: applied %v, problems %v", applied, problems)
	}
	if applied, problems := migrate.RunOn(ctx, db, app(create), migrate.Apply); len(applied) != 0 || len(problems) != 0 {
		t.Fatalf("second run: applied %v, problems %v; want nothing to do", applied, problems)
	}
	if _, problems := migrate.RunOn(ctx, db, app(create, addNote), migrate.Check); len(problems) != 1 || !strings.Contains(problems[0], "0002_add_note is pending") {
		t.Fatalf("check mode: problems %v", problems)
	}
	if applied, problems := migrate.RunOn(ctx, db, app(create, addNote), migrate.Apply); len(problems) != 0 || len(applied) != 1 {
		t.Fatalf("adding one: applied %v, problems %v", applied, problems)
	}
	if _, f := db.Call(ctx, "execute", []runtime.Value{runtime.Str("INSERT INTO rating (id, note) VALUES ('r1', 'ok')")}); f != nil {
		t.Fatalf("the migrated table: %v", f)
	}

	edited := create
	edited.SQL += "\n-- a comment is an edit too"
	cases := []struct {
		files []migration.File
		want  string
	}{
		{[]migration.File{edited, addNote}, "0001_create.sql was edited after it was applied"},
		{[]migration.File{addNote}, "migration 0001_create was applied, but its file is gone"},
	}
	for _, c := range cases {
		if _, problems := migrate.RunOn(ctx, db, app(c.files...), migrate.Apply); len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), c.want) {
			t.Errorf("problems = %v, want %q", problems, c.want)
		}
	}
	other := []migrate.Package{{Path: "lib", Files: []migration.File{file(5, "five", "CREATE TABLE five (n INTEGER)")}}}
	if _, problems := migrate.RunOn(ctx, db, other, migrate.Apply); len(problems) != 0 {
		t.Fatalf("lib: %v", problems)
	}
	other[0].Files = append([]migration.File{file(3, "three", "SELECT 1")}, other[0].Files...)
	if _, problems := migrate.RunOn(ctx, db, other, migrate.Apply); len(problems) != 1 || !strings.Contains(problems[0], "0003_three.sql is numbered before 0005_five") {
		t.Errorf("out of order: problems %v", problems)
	}

	// A failing migration leaves neither its table nor its record.
	broken := file(3, "broken", "CREATE TABLE half (n INTEGER);\nCREATE TABLE half (n INTEGER);")
	if _, problems := migrate.RunOn(ctx, db, app(create, addNote, broken), migrate.Apply); len(problems) != 1 || !strings.Contains(problems[0], "failed and was rolled back") {
		t.Fatalf("broken: problems %v", problems)
	}
	v, f := db.Call(ctx, "queryMany", []runtime.Value{runtime.Str("SELECT name FROM sqlite_master WHERE name = 'half' UNION ALL SELECT name FROM fiig_migrations WHERE number = 3 AND package = 'app'")})
	if f != nil || len(v.(runtime.List).Elems) != 0 {
		t.Fatalf("after a failed migration: %v, %v; want neither the table nor its record", v, f)
	}
}
