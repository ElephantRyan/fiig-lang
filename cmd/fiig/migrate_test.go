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
	"fiig-lang/tooling/runprogram"
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

// ADR 0138 (Hearsay GAPS G55): a package renamed with `formerly` adopts the
// history recorded under its old path, instead of re-running its first
// migration against tables that already exist.
func TestARenamedPackageKeepsItsMigrationHistory(t *testing.T) {
	db, err := fiigsql.Factory(map[string]any{"type": "sql", "driver": "sqlite", "dsn": fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	create := migration.File{Number: 1, Name: "0001_create", SQL: "CREATE TABLE rating (id TEXT PRIMARY KEY)"}
	addNote := migration.File{Number: 2, Name: "0002_add_note", SQL: "ALTER TABLE rating ADD COLUMN note TEXT NOT NULL DEFAULT ''"}
	if _, problems := migrate.RunOn(ctx, db, []migrate.Package{{Path: "abs.recommendations", Files: []migration.File{create}}}, migrate.Apply); len(problems) > 0 {
		t.Fatal(problems)
	}

	renamed := []migrate.Package{{Path: "hearsay", Files: []migration.File{create, addNote}, Formerly: []string{"abs.recommendations"}}}
	if _, problems := migrate.RunOn(ctx, db, renamed, migrate.Check); len(problems) != 1 || !strings.Contains(problems[0], "recorded under abs.recommendations, its former path") {
		t.Fatalf("check mode: problems %v", problems)
	}
	applied, problems := migrate.RunOn(ctx, db, renamed, migrate.Apply)
	if len(problems) > 0 || len(applied) != 2 || !strings.Contains(applied[0], "moved 1 recorded under abs.recommendations") || applied[1] != "hearsay 0002_add_note" {
		t.Fatalf("rename: applied %v, problems %v", applied, problems)
	}
	if applied, problems := migrate.RunOn(ctx, db, renamed, migrate.Apply); len(applied) != 0 || len(problems) != 0 {
		t.Fatalf("after the move: applied %v, problems %v; want nothing to do", applied, problems)
	}

	// History under both paths is for a person to settle; nothing moves.
	if _, f := db.Call(ctx, "execute", []runtime.Value{runtime.Str("INSERT INTO fiig_migrations (package, number, name, sha256, applied_at) VALUES ('abs.recommendations', 1, '0001_create', 'x', 'now')")}); f != nil {
		t.Fatal(f)
	}
	if _, problems := migrate.RunOn(ctx, db, renamed, migrate.Apply); len(problems) != 1 || !strings.Contains(problems[0], "recorded both under hearsay and under abs.recommendations") {
		t.Fatalf("both: problems %v", problems)
	}
}

// ADR 0140 §2-§3 on a real SQLite database: a save bumps the version, a
// clear removes the row, and a rename (ADR 0138) moves the settings of the
// module and its folders, and no other package's.
func TestSettingsLiveInTheDatabaseAndMoveWithARename(t *testing.T) {
	db, err := fiigsql.Factory(map[string]any{"type": "sql", "driver": "sqlite", "dsn": fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store, f := runprogram.NewDatabaseSettings(ctx, db)
	if f != nil {
		t.Fatal(f)
	}
	if _, f := runprogram.NewDatabaseSettings(ctx, db); f != nil {
		t.Fatalf("making the tables twice: %v", f)
	}
	v0, _ := store.Version(ctx)
	for _, s := range [][2]string{{"abs.recommendations.settings", "abs.url"}, {"abs.recommendations", "server.name"}, {"abs.recommendationsx", "other"}} {
		if f := store.Save(ctx, s[0], s[1], "v1", "admin"); f != nil {
			t.Fatal(f)
		}
	}
	if f := store.Save(ctx, "abs.recommendations.settings", "abs.url", "v2", "admin"); f != nil {
		t.Fatalf("saving again: %v", f)
	}
	values, v1, f := store.Load(ctx)
	if f != nil || v1 != v0+4 || values["abs.recommendations.settings"]["abs.url"] != "v2" {
		t.Fatalf("after saves: %v, version %d (from %d), %v", values, v1, v0, f)
	}
	if f := store.Clear(ctx, "abs.recommendationsx", "other", "admin"); f != nil {
		t.Fatal(f)
	}
	if f := store.Save(ctx, "abs.recommendationsx", "other", "kept", "admin"); f != nil {
		t.Fatal(f)
	}

	create := migration.File{Number: 1, Name: "0001_create", SQL: "CREATE TABLE rating (id TEXT PRIMARY KEY)"}
	if _, problems := migrate.RunOn(ctx, db, []migrate.Package{{Path: "abs.recommendations", Files: []migration.File{create}}}, migrate.Apply); len(problems) > 0 {
		t.Fatal(problems)
	}
	renamed := []migrate.Package{{Path: "hearsay", Files: []migration.File{create}, Formerly: []string{"abs.recommendations"}}}
	if _, problems := migrate.RunOn(ctx, db, renamed, migrate.Apply); len(problems) > 0 {
		t.Fatal(problems)
	}
	values, _, _ = store.Load(ctx)
	if values["hearsay.settings"]["abs.url"] != "v2" || values["hearsay"]["server.name"] != "v1" || values["abs.recommendationsx"]["other"] != "kept" || len(values) != 3 {
		t.Fatalf("after the rename: %v", values)
	}
}
