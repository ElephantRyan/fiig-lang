package sql

import (
	"database/sql"
	"fmt"

	"fiig-lang/compiler/runtime"
)

// scanRow reads the current row of rows into an anonymous runtime.Record
// keyed by column name — exactly the structural-record shape §3.3 already
// allows as a local value; there's no declared type for a SQL result row,
// so this is the one place in fiig that produces a record with no
// TypeName at all, same as a simulated capability's "row" always has.
func scanRow(rows *sql.Rows) (runtime.Record, error) {
	cols, err := rows.Columns()
	if err != nil {
		return runtime.Record{}, err
	}
	raw := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return runtime.Record{}, err
	}
	fields := make(map[string]runtime.Value, len(cols))
	for i, col := range cols {
		v, err := fromDriverValue(raw[i])
		if err != nil {
			return runtime.Record{}, fmt.Errorf("column %q: %w", col, err)
		}
		fields[col] = v
	}
	return runtime.Record{Fields: fields}, nil
}

// fromDriverValue converts one scanned column value (whatever the driver
// handed back for Scan into an `any`) into a runtime.Value. A non-finite
// float is an error (package doc comment).
func fromDriverValue(v any) (runtime.Value, error) {
	switch v := v.(type) {
	case nil:
		return runtime.None{}, nil
	case int64:
		return runtime.Int(v), nil
	case int:
		return runtime.Int(v), nil
	case bool:
		return runtime.Bool(v), nil
	case string:
		return runtime.Str(v), nil
	case []byte:
		// Several drivers (notably SQLite ones) return TEXT columns as
		// []byte rather than string.
		return runtime.Str(string(v)), nil
	case float32:
		return fromDriverValue(float64(v))
	case float64:
		f, ok := runtime.NewFloat(v)
		if !ok {
			return nil, fmt.Errorf("non-finite float %v has no fiig value (ADR 0048 §2.3)", v)
		}
		return f, nil
	default:
		return nil, fmt.Errorf("unsupported column type %T", v)
	}
}
