package runtime

import (
	"sort"
	"strings"
	"unicode/utf8"

	"fiig-lang/compiler/decimal"
	"fiig-lang/compiler/types"
)

// builtinFunc is one builtin method's behaviour. Its receiver kind, name,
// and arity are declared in compiler/types's table (ADR 0049 §2.8), which
// the checker types calls against; this file supplies only bodies, and
// TestEveryBuiltinMethodIsImplemented keeps the two in step.
type builtinFunc func(recv Value, args []Value) (Value, *Fault)

var builtinMethods = map[string]map[string]builtinFunc{
	"string": {
		"isBlank": func(recv Value, _ []Value) (Value, *Fault) {
			return Bool(strings.TrimSpace(string(recv.(Str))) == ""), nil
		},
		// ADR 0058 §1: a string's UTF-8 bytes; cannot fail.
		"utf8": func(recv Value, _ []Value) (Value, *Fault) {
			return Bytes{B: []byte(recv.(Str))}, nil
		},
		"contains": func(recv Value, args []Value) (Value, *Fault) {
			sub, ok := args[0].(Str)
			if !ok {
				return nil, Faultf("contains(...) argument must be a string")
			}
			return Bool(strings.Contains(string(recv.(Str)), string(sub))), nil
		},
		"toDecimalOrNone": func(recv Value, _ []Value) (Value, *Fault) {
			if d, ok := decimal.Parse(string(recv.(Str))); ok {
				return Decimal{d}, nil
			}
			return None{}, nil
		},
	},
	// ADR 0058 §1. at and slice out of range fault: a pattern is the tool
	// for input of unknown length.
	"bytes": {
		"length": func(recv Value, _ []Value) (Value, *Fault) {
			return Int(len(recv.(Bytes).B)), nil
		},
		"at": func(recv Value, args []Value) (Value, *Fault) {
			b := recv.(Bytes).B
			i, f := indexArg("bytes", "at", args[0], len(b))
			if f != nil {
				return nil, f
			}
			return Int(b[i]), nil
		},
		"slice": func(recv Value, args []Value) (Value, *Fault) {
			b := recv.(Bytes).B
			from, to, f := sliceArgs("bytes", args, len(b))
			if f != nil {
				return nil, f
			}
			return Bytes{B: b[from:to:to]}, nil
		},
		"utf8": func(recv Value, _ []Value) (Value, *Fault) {
			b := recv.(Bytes).B
			if !utf8.Valid(b) {
				return None{}, nil
			}
			return Str(b), nil
		},
	},
	// ADR 0049 §2.6.
	"List": {
		"length": func(recv Value, _ []Value) (Value, *Fault) {
			return Int(len(recv.(List).Elems)), nil
		},
		"at": func(recv Value, args []Value) (Value, *Fault) {
			elems := recv.(List).Elems
			i, f := indexArg("List", "at", args[0], len(elems))
			if f != nil {
				return nil, f
			}
			return elems[i], nil
		},
		"atOrNone": func(recv Value, args []Value) (Value, *Fault) {
			elems := recv.(List).Elems
			n, ok := args[0].(Int)
			if !ok {
				return nil, Faultf("List.atOrNone argument must be an int (got %s)", args[0])
			}
			if n < 0 || int64(n) >= int64(len(elems)) {
				return None{}, nil
			}
			return elems[n], nil
		},
		// The slice shares the list's storage. Its capacity ends at its
		// length and it claims no slot, so an append to it copies rather
		// than writing into the original (ADR 0057's claim-the-slot).
		"slice": func(recv Value, args []Value) (Value, *Fault) {
			elems := recv.(List).Elems
			from, to, f := sliceArgs("List", args, len(elems))
			if f != nil {
				return nil, f
			}
			return List{Elems: elems[from:to:to]}, nil
		},
		// ADR 0065 §1: the faulting form and the OrNone form, as at and
		// atOrNone.
		"first":       listEnd("first", true, false),
		"firstOrNone": listEnd("first", true, true),
		"last":        listEnd("last", false, false),
		"lastOrNone":  listEnd("last", false, true),
		// == semantics, so a decimal matches by value (ADR 0064 §5).
		"contains": func(recv Value, args []Value) (Value, *Fault) {
			for _, e := range recv.(List).Elems {
				if Equal(e, args[0]) {
					return Bool(true), nil
				}
			}
			return Bool(false), nil
		},
		// ADR 0065 §2: a new list, sorted stably. Descending is its own
		// stable sort, not the ascending one reversed, which would reverse
		// ties (1.5 and 1.50 print differently).
		"sorted":           func(recv Value, _ []Value) (Value, *Fault) { return sortedList(recv.(List).Elems, false) },
		"sortedDescending": func(recv Value, _ []Value) (Value, *Fault) { return sortedList(recv.(List).Elems, true) },
	},
	"Map": mapMethods,
}

// mapMethods is Map's table (ADR 0065 §6): every one returns a value, and
// put, putIfAbsent, and remove a new map.
var mapMethods = map[string]builtinFunc{
	"at": func(recv Value, args []Value) (Value, *Fault) {
		if v, ok := recv.(Map).Get(args[0]); ok {
			return v, nil
		}
		return nil, Faultf("Map.at: no key %s; use atOrNone or containsKey when it may be absent", args[0])
	},
	"atOrNone": func(recv Value, args []Value) (Value, *Fault) {
		if v, ok := recv.(Map).Get(args[0]); ok {
			return v, nil
		}
		return None{}, nil
	},
	"containsKey": func(recv Value, args []Value) (Value, *Fault) {
		_, ok := recv.(Map).Get(args[0])
		return Bool(ok), nil
	},
	"length": func(recv Value, _ []Value) (Value, *Fault) { return Int(recv.(Map).Len()), nil },
	"keys": func(recv Value, _ []Value) (Value, *Fault) {
		out := List{}
		recv.(Map).Each(func(k, _ Value) bool { out.Elems = append(out.Elems, k); return true })
		return out, nil
	},
	"values": func(recv Value, _ []Value) (Value, *Fault) {
		out := List{}
		recv.(Map).Each(func(_, v Value) bool { out.Elems = append(out.Elems, v); return true })
		return out, nil
	},
	"put": func(recv Value, args []Value) (Value, *Fault) { return recv.(Map).Put(args[0], args[1]), nil },
	// Present means present, even with a None value; the map is returned
	// unchanged, spelling and position included (§6).
	"putIfAbsent": func(recv Value, args []Value) (Value, *Fault) {
		m := recv.(Map)
		if _, ok := m.Get(args[0]); ok {
			return m, nil
		}
		return m.Put(args[0], args[1]), nil
	},
	"remove": func(recv Value, args []Value) (Value, *Fault) { return recv.(Map).Remove(args[0]), nil },
}

// listEnd is first/last and their OrNone forms.
func listEnd(name string, first, orNone bool) builtinFunc {
	return func(recv Value, _ []Value) (Value, *Fault) {
		elems := recv.(List).Elems
		if len(elems) == 0 {
			if orNone {
				return None{}, nil
			}
			return nil, Faultf("List.%s() on an empty list; use %sOrNone() when the list may be empty", name, name)
		}
		if first {
			return elems[0], nil
		}
		return elems[len(elems)-1], nil
	}
}

// sortedList is a stably sorted copy of elems. The checker allows only
// ordered element types, so a pair Less can't order is a fault for an
// unchecked program.
func sortedList(elems []Value, descending bool) (Value, *Fault) {
	out := append([]Value(nil), elems...)
	var fault *Fault
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if descending {
			a, b = b, a
		}
		less, ok := Less(a, b)
		if !ok && fault == nil {
			fault = Faultf("cannot sort %s and %s: not ordered", out[i], out[j])
		}
		return less
	})
	if fault != nil {
		return nil, fault
	}
	return List{Elems: out}, nil
}

// methodKind is the table a runtime value's methods are in.
func methodKind(v Value) string {
	switch v.(type) {
	case Str:
		return "string"
	case Bytes:
		return "bytes"
	case List:
		return "List"
	case Map:
		return "Map"
	}
	return ""
}

// evalBuiltinMethod dispatches `target.method(args)` through the shared
// table. The checker reports a missing method (K1) or a wrong argument
// count before a run, so the faults here are for unchecked programs.
func evalBuiltinMethod(target Value, method string, args []Value) (Value, *Fault) {
	kind := methodKind(target)
	sigs, ok := types.MethodsOfKind(kind)
	if !ok {
		return nil, Faultf("%s has no method %q", target, method)
	}
	sig, ok := sigs[method]
	if !ok {
		return nil, Faultf("%s has no method %q", kind, method)
	}
	if len(args) != len(sig.Params) {
		return nil, Faultf("%s.%s takes %d argument(s), got %d", kind, method, len(sig.Params), len(args))
	}
	return builtinMethods[kind][method](target, args)
}

func indexArg(kind, method string, arg Value, length int) (int, *Fault) {
	n, ok := arg.(Int)
	if !ok {
		return 0, Faultf("%s.%s argument must be an int (got %s)", kind, method, arg)
	}
	if n < 0 || int64(n) >= int64(length) {
		return 0, Faultf("index %d out of range for %s of length %d", n, kind, length)
	}
	return int(n), nil
}

func sliceArgs(kind string, args []Value, length int) (int, int, *Fault) {
	from, ok1 := args[0].(Int)
	to, ok2 := args[1].(Int)
	if !ok1 || !ok2 {
		return 0, 0, Faultf("%s.slice arguments must be ints", kind)
	}
	if from < 0 || to < from || int64(to) > int64(length) {
		return 0, 0, Faultf("slice(%d, %d) out of range for %s of length %d", from, to, kind, length)
	}
	return int(from), int(to), nil
}
