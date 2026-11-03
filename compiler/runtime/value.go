// Package runtime implements Milestone 4: a tree-walking interpreter that
// actually executes a checked fiig program, plus the structured-concurrency
// machinery `concurrent`/`retry` need (see concurrent.go, retry.go).
//
// A capability call (`Database.queryOne(...)`) is dispatched through
// CapabilityRegistry (capability.go) to whichever CapabilityProvider a host
// registered for that name — ADR 0027's declarative config
// (LoadCapabilities, config.go) is how a real host wires one up without
// hand-written Go per application; there's still no capability *contract*
// the checker type-checks a call's signature against, so every capability
// call itself stays Unknown at check time regardless. `transaction` blocks
// (transaction.go) have real commit/rollback/timeout semantics against a
// TransactionalProvider, not a pass-through. A qualified call into another
// package's own declaration resolves and executes for real once Interp has
// package context (Imports, scope.go — Milestone 6, ADR 0032/0038); without
// it, every such call still falls through to a capability call, exactly as
// before that milestone.
package runtime

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
)

// Value is a runtime value. Every concrete type below is comparable by Go
// equality only where that happens to line up with language equality (Int,
// Str, Bool, None); Equal (below) is the real equality used by `==`/`!=`.
type Value interface {
	isValue()
	String() string
}

// Int is the language's only integer type (ADR 0019: arithmetic is
// int-only).
type Int int64

func (Int) isValue()         {}
func (i Int) String() string { return strconv.FormatInt(int64(i), 10) }

// Str is the language's string type.
type Str string

func (Str) isValue()         {}
func (s Str) String() string { return string(s) }

// Bool backs comparison/boolean-operator results, `if`/`not` conditions,
// and the `true`/`false` literals (ADR 0041) — a real, declarable
// primitive type (`bool`), not just an internal runtime-only value.
type Bool bool

func (Bool) isValue() {}
func (b Bool) String() string {
	if b {
		return "true"
	}
	return "false"
}

// None is the absence value (§3.4) — a single sentinel, not parameterized by
// what it's absent from.
type None struct{}

func (None) isValue()       {}
func (None) String() string { return "None" }

// List is `List<T>`. Elems is a view of the first len(Elems) slots of a
// buffer that several lists may share (ADR 0057 §5); used counts how many
// of that buffer's slots some list has claimed, and is nil for a list that
// owns no spare room (a literal, decoded JSON). See appendList.
type List struct {
	Elems []Value
	used  *atomic.Int64
}

func (List) isValue() {}
func (l List) String() string {
	parts := make([]string, len(l.Elems))
	for i, e := range l.Elems {
		parts[i] = e.String()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// Record is a model instance or an anonymous structural record — most commonly
// the shape a simulated capability call hands back (get_customer.fiig's
// `row`), which has no declared type at all (TypeName == "").
type Record struct {
	TypeName string
	Fields   map[string]Value
}

func (Record) isValue() {}
func (r Record) String() string {
	names := make([]string, 0, len(r.Fields))
	for n := range r.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = n + ": " + r.Fields[n].String()
	}
	prefix := r.TypeName
	return prefix + "{" + strings.Join(parts, ", ") + "}"
}

// EnumValue is one constructed variant of a declared `enum` type
// (CustomerError.NotFound(id)). Fields are positional, in the variant's own
// declared order — bindConstructorPattern (compiler/checker/expr.go) matches
// a pattern's sub-patterns against a variant's fields the same way, by
// position, so matching here uses the same convention rather than names.
type EnumValue struct {
	TypeName string
	Variant  string
	Fields   []Value
	// Consts is a headed enum's variant's header constants by field name
	// (ADR 0046 §4), read as `source.code` (§4b); nil otherwise. They are
	// fixed by Variant, so Equal ignores them.
	Consts map[string]Value
}

func (EnumValue) isValue() {}
func (e EnumValue) String() string {
	// A variant with no fields prints as it is written: `Rounding.HalfUp`.
	if len(e.Fields) == 0 {
		return e.TypeName + "." + e.Variant
	}
	parts := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		parts[i] = f.String()
	}
	return e.TypeName + "." + e.Variant + "(" + strings.Join(parts, ", ") + ")"
}

// Result is `Result<S, E>`'s only two constructors, Ok and Err, collapsed
// into one tagged value — Value holds the success payload when Ok, the
// error payload otherwise.
type Result struct {
	Ok    bool
	Value Value
}

func (Result) isValue() {}
func (r Result) String() string {
	if r.Ok {
		return "Ok(" + r.Value.String() + ")"
	}
	return "Err(" + r.Value.String() + ")"
}

// BatchItem is one input's outcome inside a BatchResult — Key preserves the
// input identity (§3.7: "preserve every input identity in successes and
// failures"), Outcome is the per-item Result the concurrent block's body
// produced for it.
type BatchItem struct {
	Key     Value
	Outcome Result
}

// BatchResult is `BatchResult<Key, Success, Failure>` — a `concurrent`
// block's result, built by concurrent.go. Items is in completion order by
// default, or input order when the block used `preserveOrder` (ADR 0024) —
// BatchResult itself doesn't know which; that's a property of how it was
// built, not of this value.
type BatchResult struct{ Items []BatchItem }

func (BatchResult) isValue() {}
func (b BatchResult) String() string {
	parts := make([]string, len(b.Items))
	for i, it := range b.Items {
		parts[i] = fmt.Sprintf("%s: %s", it.Key.String(), it.Outcome.String())
	}
	return "BatchResult[" + strings.Join(parts, ", ") + "]"
}

// Equal is `==`/`!=`'s runtime notion of equality — structural for the
// composite shapes, since the checker performs no type-level overload
// resolution here (ADR 0019's "never overloaded" policy extended to
// comparison generally: one fixed meaning per shape).
func Equal(a, b Value) bool {
	switch a := a.(type) {
	case Int:
		bv, ok := b.(Int)
		return ok && a == bv
	case Float:
		// The checker rejects == on floats (M1); this backs the structural
		// equality the interpreter itself needs, which is exact.
		bv, ok := b.(Float)
		return ok && a == bv
	case Decimal:
		// By value: 1.50 == 1.5 (ADR 0064 §5).
		bv, ok := b.(Decimal)
		return ok && a.Cmp(bv.Decimal) == 0
	case Str:
		bv, ok := b.(Str)
		return ok && a == bv
	case Bool:
		bv, ok := b.(Bool)
		return ok && a == bv
	case None:
		_, ok := b.(None)
		return ok
	case Record:
		bv, ok := b.(Record)
		if !ok || a.TypeName != bv.TypeName || len(a.Fields) != len(bv.Fields) {
			return false
		}
		if sameMap(a.Fields, bv.Fields) {
			return true
		}
		for k, av := range a.Fields {
			bvv, ok := bv.Fields[k]
			if !ok || !Equal(av, bvv) {
				return false
			}
		}
		return true
	case EnumValue:
		bv, ok := b.(EnumValue)
		if !ok || a.TypeName != bv.TypeName || a.Variant != bv.Variant || len(a.Fields) != len(bv.Fields) {
			return false
		}
		for i := range a.Fields {
			if !Equal(a.Fields[i], bv.Fields[i]) {
				return false
			}
		}
		return true
	case Result:
		bv, ok := b.(Result)
		return ok && a.Ok == bv.Ok && Equal(a.Value, bv.Value)
	case Bytes:
		bv, ok := b.(Bytes)
		return ok && bytesEqual(a, bv)
	case Map:
		bv, ok := b.(Map)
		return ok && mapsEqual(a, bv)
	case List:
		// ADR 0057 §6: same length, equal elements in the same order. Two
		// views of the same buffer slots are equal without walking them,
		// which is safe only because values are immutable.
		bv, ok := b.(List)
		if !ok || len(a.Elems) != len(bv.Elems) {
			return false
		}
		if len(a.Elems) == 0 || &a.Elems[0] == &bv.Elems[0] {
			return true
		}
		for i := range a.Elems {
			if !Equal(a.Elems[i], bv.Elems[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// Less backs `<`/`<=`/`>`/`>=` — defined for Int, Float (total, since no
// Float is NaN; ADR 0048 §2.3), Decimal (by value), and Str
// (lexicographic); ok is false for anything else.
func Less(a, b Value) (less bool, ok bool) {
	switch a := a.(type) {
	case Int:
		bv, ok := b.(Int)
		return a < bv, ok
	case Float:
		bv, ok := b.(Float)
		return a < bv, ok
	case Decimal:
		bv, ok := b.(Decimal)
		return ok && a.Cmp(bv.Decimal) < 0, ok
	case Str:
		bv, ok := b.(Str)
		return a < bv, ok
	}
	return false, false
}
