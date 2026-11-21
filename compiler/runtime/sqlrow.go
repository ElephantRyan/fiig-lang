package runtime

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"fiig-lang/compiler/decimal"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0082 §5-§8: a Database query's rows are read into the type the
// checker took from the call's context. A provider hands back each row as
// a record with no type, its columns holding whatever the database stored
// (Int, Float, Str, Bytes, Bool, None); this reader fills the row type,
// so the Go provider, a scripted one, and a provider written in fiig share
// one rule.

// readQueryResult is readQuery for evalCall.
func (in *Interp) readQueryResult(op string, got Value, into types.Type) (Value, bool, *Fault) {
	v, f := in.readQuery(op, got, into)
	return v, false, f
}

// readQuery reads what op (queryOne or queryMany) returned into into, the
// row type: a queryOne result is None or one row, a queryMany result a
// list of rows. A mismatch is a fault at the query (§8).
func (in *Interp) readQuery(op string, got Value, into types.Type) (Value, *Fault) {
	read := func(v Value) (Value, *Fault) {
		row, ok := v.(Record)
		if !ok {
			return nil, Faultf("Database.%s: the provider returned %s, not a row", op, v)
		}
		out, err := readRow(row, into, in.fieldType)
		if err != nil {
			return nil, Faultf("Database.%s: %v", op, err)
		}
		return out, nil
	}
	switch op {
	case "queryOne":
		if _, isNone := got.(None); isNone {
			return None{}, nil
		}
		return read(got)
	case "queryMany":
		list, ok := got.(List)
		if !ok {
			return nil, Faultf("Database.queryMany: the provider returned %s, not a list of rows", got)
		}
		elems := make([]Value, len(list.Elems))
		for i, e := range list.Elems {
			v, f := read(e)
			if f != nil {
				return nil, f
			}
			elems[i] = v
		}
		return List{Elems: elems}, nil
	}
	return got, nil
}

// readRow fills t from one row. A model reads each field from the column
// whose name matches it (§6, ADR 0094), and the match is checked both
// ways; any other t is a one-column row.
func readRow(row Record, t types.Type, u fieldTypes) (Value, error) {
	if named, ok := t.(types.Named); ok {
		if decl, ok := named.Decl.(*syntax.ModelDecl); ok {
			return readModelRow(row, decl, u)
		}
	}
	if len(row.Fields) != 1 {
		return nil, fmt.Errorf("the query returns %d columns (%s), but reads one %s", len(row.Fields), columnList(row), t)
	}
	for col, v := range row.Fields {
		return readColumn(col, v, t, "the query's "+t.String())
	}
	return nil, nil
}

// readModelRow matches each column to a field: the field of its exact
// name, or else the one whose name matches ignoring case and underscores
// (ADR 0094 §1), so user_id, USER_ID, and Postgres's folded userid all
// fill userId. Two columns for one field, a column for no field, and a
// field with no column each fault (§2).
func readModelRow(row Record, decl *syntax.ModelDecl, u fieldTypes) (Value, error) {
	all := decl.AllFields()
	exact := make(map[string]bool, len(all))
	byKey := make(map[string]string, len(all))
	for _, f := range all {
		exact[f.Name] = true
		byKey[syntax.ColumnKey(f.Name)] = f.Name
	}
	cols := make([]string, 0, len(row.Fields))
	for col := range row.Fields {
		cols = append(cols, col)
	}
	sort.Strings(cols)
	columnOf := make(map[string]string, len(all))
	var extra []string
	for _, col := range cols {
		field, ok := col, exact[col]
		if !ok {
			field, ok = byKey[syntax.ColumnKey(col)]
		}
		if !ok {
			extra = append(extra, strconv.Quote(col))
			continue
		}
		if other, taken := columnOf[field]; taken {
			return nil, fmt.Errorf("columns %q and %q both fill %s.%s; select one of them (ADR 0094 §2)", other, col, decl.Name, field)
		}
		columnOf[field] = col
	}
	if len(extra) > 0 {
		return nil, fmt.Errorf("column %s has no field in %s; select only the columns %s reads", strings.Join(extra, ", "), decl.Name, decl.Name)
	}
	fields := make(map[string]Value, len(all))
	for _, f := range all {
		ft, ok := u(decl, f.Type)
		if !ok {
			return nil, fmt.Errorf("%s.%s has a type that doesn't resolve", decl.Name, f.Name)
		}
		col, present := columnOf[f.Name]
		if !present {
			hint := ""
			if IsOptional(ft) {
				// Hearsay: an optional field still needs its column (ADR
				// 0094), which a reader adding one is likely to miss.
				hint = fmt.Sprintf("; a field that may be None still needs a column, so for a query with nothing for it, select NULL AS %s", f.Name)
			}
			return nil, fmt.Errorf("%s.%s has no column (looked for %s); the query returns %s%s", decl.Name, f.Name, lookedFor(f.Name), columnList(row), hint)
		}
		read, err := readColumn(col, row.Fields[col], ft, decl.Name+"."+f.Name)
		if err != nil {
			return nil, err
		}
		fields[f.Name] = read
	}
	return Record{TypeName: decl.Name, Fields: fields, Decl: decl}, nil
}

// lookedFor is a field's name and its snake_case spelling, the two a
// reader is likeliest to have meant: "userId or user_id".
func lookedFor(field string) string {
	var snake strings.Builder
	for i, r := range field {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				snake.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		snake.WriteRune(r)
	}
	if snake.String() == field {
		return field
	}
	return field + " or " + snake.String()
}

// readColumn reads one stored value as t, by §7's table; what names the
// field or row type for the message.
func readColumn(col string, v Value, t types.Type, what string) (Value, error) {
	refuse := func(hint string) error {
		msg := fmt.Sprintf("column %q holds %s, but %s is %s", col, storedAs(v), what, t)
		if hint != "" {
			msg += "; " + hint
		}
		return fmt.Errorf("%s", msg)
	}
	if u, ok := t.(types.Union); ok {
		if _, isNone := v.(None); isNone && IsOptional(u) {
			return None{}, nil
		}
		for _, m := range u.Members {
			if _, isNone := m.(types.NoneType); !isNone {
				return readColumn(col, v, m, what)
			}
		}
	}
	if _, isNone := v.(None); isNone {
		return nil, refuse("only a `T | None` field reads NULL")
	}
	switch t := t.(type) {
	case types.Primitive:
		switch t.Name {
		case "int":
			if n, ok := v.(Int); ok {
				return n, nil
			}
		case "float":
			switch v := v.(type) {
			case Float:
				return v, nil
			case Int:
				if f := float64(v); math.Abs(f) <= 1<<53 {
					out, _ := NewFloat(f)
					return out, nil
				}
				return nil, refuse("it isn't exact in binary64")
			}
		case "decimal":
			switch v := v.(type) {
			case Str:
				if d, ok := decimal.ParseText(string(v)); ok {
					return Decimal{d}, nil
				}
				return nil, refuse("it isn't a number")
			case Bytes:
				if d, ok := decimal.ParseText(string(v.B)); ok && utf8.Valid(v.B) {
					return Decimal{d}, nil
				}
			case Int:
				d, _ := decimal.Parse(strconv.FormatInt(int64(v), 10))
				return Decimal{d}, nil
			case Float:
				return nil, refuse("a REAL can't be read as a decimal exactly; store decimals as TEXT, or read a float field and convert with math.toDecimal (ADR 0082 §7)")
			}
		case "string":
			switch v := v.(type) {
			case Str:
				return v, nil
			case Bytes:
				// Some drivers hand TEXT back as bytes, indistinguishable
				// from a BLOB; valid UTF-8 reads as the text it is.
				if utf8.Valid(v.B) {
					return Str(string(v.B)), nil
				}
			}
		case "bytes":
			switch v := v.(type) {
			case Bytes:
				return v, nil
			case Str:
				return Bytes{B: []byte(v)}, nil
			}
		case "bool":
			switch v := v.(type) {
			case Bool:
				return v, nil
			case Int:
				if v == 0 || v == 1 {
					return Bool(v == 1), nil
				}
			}
		}
	case types.Named:
		if decl, ok := t.Decl.(*syntax.EnumDecl); ok && !decl.HasPayload() {
			var text string
			switch v := v.(type) {
			case Str:
				text = string(v)
			case Bytes:
				text = string(v.B)
			default:
				return nil, refuse("")
			}
			if variant, ok := decl.VariantByText(text); ok {
				return EnumValue{TypeName: decl.Name, Variant: variant.Name, Decl: decl}, nil
			}
			return nil, refuse("its variants are spelled " + variantNames(decl))
		}
	}
	return nil, refuse("")
}

// storedAs describes a stored value by its storage class, as SQL calls
// it, for a fault message.
func storedAs(v Value) string {
	switch v := v.(type) {
	case None:
		return "NULL"
	case Int:
		return "INTEGER " + v.String()
	case Float:
		return "REAL " + v.String()
	case Str:
		return "TEXT " + strconv.Quote(string(v))
	case Bytes:
		return fmt.Sprintf("a BLOB of %d bytes", len(v.B))
	case Bool:
		return "BOOLEAN " + v.String()
	}
	return v.String()
}

func columnList(row Record) string {
	cols := make([]string, 0, len(row.Fields))
	for c := range row.Fields {
		cols = append(cols, strconv.Quote(c))
	}
	sort.Strings(cols)
	if len(cols) == 0 {
		return "no columns"
	}
	return strings.Join(cols, ", ")
}
