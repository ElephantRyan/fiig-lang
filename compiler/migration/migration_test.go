package migration

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestReadOrdersAndRefuses(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/0002_add_note.sql":    {Data: []byte("ALTER TABLE rating ADD COLUMN note TEXT")},
		"migrations/0001_create.sql":      {Data: []byte("CREATE TABLE rating (id TEXT)")},
		"migrations/0010_index.sql":       {Data: []byte("CREATE INDEX r ON rating (id)")},
		"migrations/README.md":            {Data: []byte("notes")},
		"migrations/2_again.sql":          {Data: []byte("SELECT 1")},
		"migrations/old/0004_skipped.sql": {Data: []byte("SELECT 1")},
	}
	files, errs := Read(fsys)
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
	}
	if want := []string{"0001_create", "0002_add_note", "0010_index"}; !reflect.DeepEqual(names, want) {
		t.Errorf("files = %v, want %v", names, want)
	}
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "\n"
	}
	for _, want := range []string{"README.md: a migration is named", "have the same number, 2", "old: migrations are files directly"} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors = %s\nwant one containing %q", joined, want)
		}
	}
	if files, errs := Read(fstest.MapFS{"rules/a.fiig": {}}); files != nil || errs != nil {
		t.Errorf("no migrations/: got %v, %v", files, errs)
	}
}

func TestStatements(t *testing.T) {
	sql := `-- the rating table
CREATE TABLE rating (id TEXT, note TEXT DEFAULT 'a;b');
/* an index; for lookups */
CREATE INDEX rating_by_id ON rating (id);
CREATE FUNCTION f() RETURNS int AS $body$ SELECT 1; $body$ LANGUAGE sql;
INSERT INTO rating VALUES ('it''s;', "x;y")
;
-- trailing comment only
`
	want := []string{
		"-- the rating table\nCREATE TABLE rating (id TEXT, note TEXT DEFAULT 'a;b')",
		"/* an index; for lookups */\nCREATE INDEX rating_by_id ON rating (id)",
		"CREATE FUNCTION f() RETURNS int AS $body$ SELECT 1; $body$ LANGUAGE sql",
		"INSERT INTO rating VALUES ('it''s;', \"x;y\")",
	}
	if got := Statements(sql); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if got := Statements("SELECT $1"); len(got) != 1 {
		t.Errorf("a $1 parameter isn't a dollar quote: got %q", got)
	}
}
