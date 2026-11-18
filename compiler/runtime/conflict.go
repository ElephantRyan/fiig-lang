package runtime

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0087 §2: a database's report of a failure, sorted into the
// conflicts a program may handle.

// Conflict is one DatabaseConflict variant and its name.
type Conflict struct {
	Variant string
	Name    string
}

// Value is the conflict as the prelude enum.
func (c *Conflict) Value() Value {
	return EnumValue{TypeName: "DatabaseConflict", Variant: c.Variant, Fields: []Value{Str(c.Name)}, Decl: types.DatabaseConflictDecl}
}

// SQLStateConflict classifies a Postgres SQLSTATE (§2's table), naming it
// by the constraint, or for NotNull the column, the server reported; nil
// for a code that isn't a conflict.
func SQLStateConflict(code, constraint, column, message string) *Conflict {
	switch code {
	case "23505":
		return &Conflict{"Unique", constraint}
	case "23503":
		return &Conflict{"ForeignKey", constraint}
	case "23502":
		return &Conflict{"NotNull", column}
	case "23514":
		return &Conflict{"Check", constraint}
	case "40001", "40P01":
		return &Conflict{"Retryable", message}
	}
	return nil
}

// SQLiteConflict classifies an SQLite extended result code (§2's table).
// SQLite names no constraint, only the columns after the message's colon
// ("UNIQUE constraint failed: customers.email"), so that is the name.
func SQLiteConflict(code int, message string) *Conflict {
	name := message
	if i := strings.LastIndex(message, ": "); i >= 0 {
		name = message[i+2:]
	}
	if i := strings.LastIndex(name, " ("); i >= 0 && strings.HasSuffix(name, ")") {
		name = name[:i]
	}
	switch code {
	case 2067, 1555:
		return &Conflict{"Unique", name}
	case 787:
		return &Conflict{"ForeignKey", name}
	case 1299:
		return &Conflict{"NotNull", name}
	case 275:
		return &Conflict{"Check", name}
	}
	if code&0xff == 5 || code&0xff == 6 {
		return &Conflict{"Retryable", message}
	}
	return nil
}

var savepointSeq atomic.Int64

// evalHandlingConflicts runs a Database call that opted in (ADR 0087 §1):
// Ok with what it returns, or Err with its conflict. Inside a transaction
// the call runs between a savepoint and its release, rolled back to on a
// conflict, so the transaction stays usable (§3).
func (in *Interp) evalHandlingConflicts(ctx context.Context, e *syntax.CallExpr, fa *syntax.FieldAccess, env *Env) (Value, bool, *Fault) {
	tx, inTx := lookupActiveTx(ctx, types.DatabaseName)
	savepoint := ""
	if inTx {
		savepoint = "fiig_conflict_" + strconv.FormatInt(savepointSeq.Add(1), 10)
		if _, f := tx.Call(ctx, "execute", []Value{Str("SAVEPOINT " + savepoint)}); f != nil {
			return nil, false, f
		}
	}
	v, earlyReturn, f := in.evalQualifiedCall(ctx, fa, e.Args, env)
	if f == nil && !earlyReturn {
		if into, typed := e.DecodeInto.(types.Type); typed {
			v, f = in.readQuery(fa.Field, v, into)
		}
	}
	if earlyReturn {
		return v, true, nil
	}
	if f != nil && f.Conflict == nil {
		return nil, false, f
	}
	if inTx {
		end := "RELEASE SAVEPOINT " + savepoint
		if f != nil {
			if _, rf := tx.Call(ctx, "execute", []Value{Str("ROLLBACK TO SAVEPOINT " + savepoint)}); rf != nil {
				return nil, false, rf
			}
		}
		if _, rf := tx.Call(ctx, "execute", []Value{Str(end)}); rf != nil {
			return nil, false, rf
		}
	}
	if f != nil {
		return Result{Ok: false, Value: f.Conflict.Value()}, false, nil
	}
	return Result{Ok: true, Value: v}, false, nil
}
