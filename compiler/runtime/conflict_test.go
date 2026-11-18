package runtime

import "testing"

// ADR 0087 §2's table, for both databases.
func TestConflictCodes(t *testing.T) {
	pg := []struct {
		code, variant, name string
	}{
		{"23505", "Unique", "people_pk"},
		{"23503", "ForeignKey", "people_pk"},
		{"23502", "NotNull", "name"},
		{"23514", "Check", "people_pk"},
		{"40001", "Retryable", "could not serialize"},
		{"40P01", "Retryable", "could not serialize"},
	}
	for _, tc := range pg {
		c := SQLStateConflict(tc.code, "people_pk", "name", "could not serialize")
		if c == nil || c.Variant != tc.variant || c.Name != tc.name {
			t.Errorf("SQLSTATE %s = %+v, want %s(%s)", tc.code, c, tc.variant, tc.name)
		}
	}
	if c := SQLStateConflict("42601", "", "", "syntax error"); c != nil {
		t.Errorf("a syntax error is %+v, want no conflict", c)
	}
	sqlite := []struct {
		code          int
		message       string
		variant, name string
	}{
		{2067, "constraint failed: UNIQUE constraint failed: people.email (2067)", "Unique", "people.email"},
		{1555, "UNIQUE constraint failed: people.id", "Unique", "people.id"},
		{787, "FOREIGN KEY constraint failed", "ForeignKey", "FOREIGN KEY constraint failed"},
		{1299, "NOT NULL constraint failed: people.name", "NotNull", "people.name"},
		{275, "CHECK constraint failed: age >= 0", "Check", "age >= 0"},
		{5, "database is locked", "Retryable", "database is locked"},
		{517, "database is locked", "Retryable", "database is locked"},
	}
	for _, tc := range sqlite {
		c := SQLiteConflict(tc.code, tc.message)
		if c == nil || c.Variant != tc.variant || c.Name != tc.name {
			t.Errorf("SQLite %d = %+v, want %s(%s)", tc.code, c, tc.variant, tc.name)
		}
	}
	if c := SQLiteConflict(1, "no such table: x"); c != nil {
		t.Errorf("an SQL error is %+v, want no conflict", c)
	}
}
