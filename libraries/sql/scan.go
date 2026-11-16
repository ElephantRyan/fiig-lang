package sql

import (
	"database/sql"
	"fmt"

	"fiig-lang/compiler/runtime"
)

// scanRow reads the current row of rows into a runtime.Record with no
// type, keyed by column name, which the interpreter then reads into the
// query's row type (ADR 0082 §5). Two columns of one name fault (§6),
// since a record can hold only one: a join renames one with AS.
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
		if _, dup := fields[col]; dup {
			return runtime.Record{}, fmt.Errorf("two columns are named %q; rename one with AS (ADR 0082 §6)", col)
		}
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
		// A BLOB, or TEXT from a driver that hands text back as bytes;
		// the row type's field decides which it reads as (ADR 0082 §7).
		return runtime.Bytes{B: append([]byte(nil), v...)}, nil
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
