// Package sql is fiig's reference "Database" capability — the first real
// (not simulated/test) capability library this project ships. It wraps
// Go's own database/sql generically, the same way JDBC, Python's DB-API,
// and database/sql itself all handle "SQL support" in a general-purpose
// language: a driver-agnostic interface here, with the actual database
// engine chosen by whoever deploys the application (via the driver they
// pass to sql.Open), not by this package or fiig's core. Nothing here
// depends on any specific database driver — only on the standard library's
// database/sql — so the core `fiig-lang` module (and this library) stay at
// zero third-party dependencies; only this module's own tests need a real
// driver to run against, and that dependency is isolated to
// libraries/sql's own go.mod (see go.work at the repo root).
//
// The capability contract (method names, argument/return shapes):
//
//	Database.queryOne(sql, args...)  -> an anonymous record, or None if no row matched
//	Database.queryMany(sql, args...) -> a List of anonymous records
//	Database.execute(sql, args...)   -> an int (rows affected)
//
// A new row's ID is the program's to make, with Random.uuid() (ADR 0068
// §4), not this provider's.
//
// A result column decodes to the obvious fiig Value: SQL integers become
// Int, floating-point columns Float (ADR 0048), text becomes Str, NULL
// becomes None. A decimal binds as TEXT in plain notation (ADR 0064 §8)
// and reads back as text, a string, since a row has no declared types to
// say which text is a decimal; `toDecimalOrNone()` converts it. A non-finite float (Postgres stores 'NaN' and 'Infinity')
// has no fiig value, and since these calls have no error channel yet it
// Faults; ADR 0048 §2.3 records that as this boundary's open gap.
package sql

import (
	"context"
	"database/sql"
	"fmt"

	"fiig-lang/compiler/runtime"
)

// Provider is the reference Database capability, backed by an already-
// opened *sql.DB (opened by the caller with whatever driver they chose —
// this package never calls sql.Open itself).
type Provider struct {
	db *sql.DB
}

// New wraps db as a runtime.CapabilityProvider + runtime.TransactionalProvider.
func New(db *sql.DB) *Provider {
	return &Provider{db: db}
}

func (p *Provider) Call(ctx context.Context, method string, args []runtime.Value) (runtime.Value, *runtime.Fault) {
	return dispatch(ctx, p.db, method, args)
}

// Begin opens a real database/sql transaction — compiler/runtime/
// transaction.go's evalTransaction calls this for `transaction Database {
// ... }`, then routes every capability call made inside the block to the
// returned Tx instead of back to Provider, so writes are visible to
// later reads in the same block (fiig-lang.md §3.8) for exactly the same
// reason database/sql's own *sql.Tx works that way.
func (p *Provider) Begin(ctx context.Context) (runtime.Tx, *runtime.Fault) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, runtime.Faultf("begin transaction: %v", err)
	}
	return &txHandle{tx: tx}, nil
}

type txHandle struct{ tx *sql.Tx }

func (t *txHandle) Call(ctx context.Context, method string, args []runtime.Value) (runtime.Value, *runtime.Fault) {
	return dispatch(ctx, t.tx, method, args)
}

func (t *txHandle) Commit() *runtime.Fault {
	if err := t.tx.Commit(); err != nil {
		return runtime.Faultf("commit: %v", err)
	}
	return nil
}

func (t *txHandle) Rollback() *runtime.Fault {
	if err := t.tx.Rollback(); err != nil {
		return runtime.Faultf("rollback: %v", err)
	}
	return nil
}

// execer is the common subset of *sql.DB and *sql.Tx this package needs —
// lets dispatch work identically whether or not a transaction is active,
// without Provider and txHandle each duplicating the method bodies.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func dispatch(ctx context.Context, e execer, method string, args []runtime.Value) (runtime.Value, *runtime.Fault) {
	switch method {
	case "queryOne":
		return queryOne(ctx, e, args)
	case "queryMany":
		return queryMany(ctx, e, args)
	case "execute":
		return execute(ctx, e, args)
	default:
		return nil, runtime.Faultf("Database has no method %q", method)
	}
}

func splitSQLArgs(args []runtime.Value) (string, []any, *runtime.Fault) {
	if len(args) < 1 {
		return "", nil, runtime.Faultf("requires a SQL string as its first argument")
	}
	text, ok := args[0].(runtime.Str)
	if !ok {
		return "", nil, runtime.Faultf("first argument must be a string (got %s)", args[0])
	}
	driverArgs := make([]any, len(args)-1)
	for i, a := range args[1:] {
		v, err := toDriverValue(a)
		if err != nil {
			return "", nil, runtime.Faultf("argument %d: %v", i+1, err)
		}
		driverArgs[i] = v
	}
	return string(text), driverArgs, nil
}

func queryOne(ctx context.Context, e execer, args []runtime.Value) (runtime.Value, *runtime.Fault) {
	text, driverArgs, f := splitSQLArgs(args)
	if f != nil {
		return nil, f
	}
	rows, err := e.QueryContext(ctx, text, driverArgs...)
	if err != nil {
		return nil, runtime.Faultf("queryOne: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, runtime.Faultf("queryOne: %v", err)
		}
		return runtime.None{}, nil
	}
	rec, err := scanRow(rows)
	if err != nil {
		return nil, runtime.Faultf("queryOne: %v", err)
	}
	return rec, nil
}

func queryMany(ctx context.Context, e execer, args []runtime.Value) (runtime.Value, *runtime.Fault) {
	text, driverArgs, f := splitSQLArgs(args)
	if f != nil {
		return nil, f
	}
	rows, err := e.QueryContext(ctx, text, driverArgs...)
	if err != nil {
		return nil, runtime.Faultf("queryMany: %v", err)
	}
	defer rows.Close()
	var elems []runtime.Value
	for rows.Next() {
		rec, err := scanRow(rows)
		if err != nil {
			return nil, runtime.Faultf("queryMany: %v", err)
		}
		elems = append(elems, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, runtime.Faultf("queryMany: %v", err)
	}
	return runtime.List{Elems: elems}, nil
}

func execute(ctx context.Context, e execer, args []runtime.Value) (runtime.Value, *runtime.Fault) {
	text, driverArgs, f := splitSQLArgs(args)
	if f != nil {
		return nil, f
	}
	result, err := e.ExecContext(ctx, text, driverArgs...)
	if err != nil {
		return nil, runtime.Faultf("execute: %v", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, runtime.Faultf("execute: %v", err)
	}
	return runtime.Int(affected), nil
}

// toDriverValue converts a fiig argument into whatever database/sql
// expects to bind as a query parameter. Only scalars bind; a record has no
// single SQL value.
func toDriverValue(v runtime.Value) (any, error) {
	switch v := v.(type) {
	case runtime.Int:
		return int64(v), nil
	case runtime.Float:
		return float64(v), nil
	case runtime.Str:
		return string(v), nil
	case runtime.Decimal:
		// ADR 0064 §8: TEXT in plain notation at its scale, so the value
		// and its scale survive; a REAL would round it to a float.
		return v.String(), nil
	case runtime.Bool:
		return bool(v), nil
	case runtime.None:
		return nil, nil
	case runtime.Record:
		return nil, fmt.Errorf("cannot bind a %s value as a SQL parameter", v)
	default:
		return nil, fmt.Errorf("cannot bind %T as a SQL parameter", v)
	}
}
