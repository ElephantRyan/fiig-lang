package runtime

import (
	"encoding/base64"
	"encoding/hex"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"fiig-lang/compiler/decimal"
	"fiig-lang/compiler/text"
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
		// ADR 0069 §7: decoding text back to bytes, reading what real
		// producers send.
		"fromHex": func(recv Value, _ []Value) (Value, *Fault) {
			return decodedOrNone(hex.DecodeString(string(recv.(Str))))
		},
		"fromBase64": func(recv Value, _ []Value) (Value, *Fault) {
			return decodedOrNone(decodeBase64(string(recv.(Str)), base64.StdEncoding))
		},
		"fromBase64Url": func(recv Value, _ []Value) (Value, *Fault) {
			return decodedOrNone(decodeBase64(string(recv.(Str)), base64.URLEncoding))
		},
		"contains": func(recv Value, args []Value) (Value, *Fault) {
			sub, ok := args[0].(Str)
			if !ok {
				return nil, Faultf("contains(...) argument must be a string")
			}
			return Bool(strings.Contains(string(recv.(Str)), string(sub))), nil
		},
		"toDecimalOrNone": func(recv Value, _ []Value) (Value, *Fault) {
			if d, ok := decimal.ParseText(string(recv.(Str))); ok {
				return Decimal{d}, nil
			}
			return None{}, nil
		},
		// ADR 0067 §1-§2: length and slice count grapheme clusters.
		"length": func(recv Value, _ []Value) (Value, *Fault) {
			return Int(text.GraphemeCount(string(recv.(Str)))), nil
		},
		"codePointCount": func(recv Value, _ []Value) (Value, *Fault) {
			return Int(utf8.RuneCountInString(string(recv.(Str)))), nil
		},
		"slice": func(recv Value, args []Value) (Value, *Fault) {
			s := string(recv.(Str))
			offsets := text.GraphemeOffsets(s)
			from, to, f := sliceArgs("string", args, len(offsets)-1)
			if f != nil {
				return nil, f
			}
			return Str(s[offsets[from]:offsets[to]]), nil
		},
		// §3, §5: searching matches code points.
		"startsWith":   stringTest(strings.HasPrefix),
		"endsWith":     stringTest(strings.HasSuffix),
		"removePrefix": stringOp(strings.TrimPrefix),
		"removeSuffix": stringOp(strings.TrimSuffix),
		// White_Space, as isBlank.
		"trim":      stringMap(strings.TrimSpace),
		"trimStart": stringMap(func(s string) string { return strings.TrimLeftFunc(s, unicode.IsSpace) }),
		"trimEnd":   stringMap(func(s string) string { return strings.TrimRightFunc(s, unicode.IsSpace) }),
		// §4: full mapping and folding, no locale.
		"lowercase":          stringMap(text.Lower),
		"uppercase":          stringMap(text.Upper),
		"equalsIgnoringCase": stringTest(text.EqualFold),
		"split": func(recv Value, args []Value) (Value, *Fault) {
			sep := string(args[0].(Str))
			if sep == "" {
				return nil, Faultf("split(\"\"): the separator is empty")
			}
			parts := strings.Split(string(recv.(Str)), sep)
			elems := make([]Value, len(parts))
			for i, p := range parts {
				elems[i] = Str(p)
			}
			return List{Elems: elems}, nil
		},
		"replaceAll": func(recv Value, args []Value) (Value, *Fault) {
			old := string(args[0].(Str))
			if old == "" {
				return nil, Faultf("replaceAll(\"\", ...): the text to replace is empty")
			}
			return Str(strings.ReplaceAll(string(recv.(Str)), old, string(args[1].(Str)))), nil
		},
		"padLeft":  pad(true),
		"padRight": pad(false),
		// ADR 0129 §1a: the text count times; a negative count faults, as take
		// and drop do, and so does a result too long for a string to hold.
		"repeat": func(recv Value, args []Value) (Value, *Fault) {
			s := string(recv.(Str))
			count, ok := args[0].(Int)
			if !ok {
				return nil, Faultf("repeat's count must be an int (got %s)", args[0])
			}
			if count < 0 {
				return nil, Faultf("repeat(%d): the count can't be negative", count)
			}
			if len(s) > 0 && int64(count) > int64(math.MaxInt)/int64(len(s)) {
				return nil, Faultf("repeat(%d) of a %d-byte string is too long for a string", count, len(s))
			}
			return Str(strings.Repeat(s, int(count))), nil
		},
		// ADR 0126: the text as a Secret that prints as Secret(name).
		"toSecret": func(recv Value, args []Value) (Value, *Fault) {
			return NewSecret(string(args[0].(Str)), string(recv.(Str))), nil
		},
		// §7: an optional sign and digits; None when out of range.
		"toIntOrNone": func(recv Value, _ []Value) (Value, *Fault) {
			s := string(recv.(Str))
			digits := strings.TrimLeft(s, "+-")
			if len(s)-len(digits) > 1 || digits == "" || strings.TrimLeft(digits, "0123456789") != "" {
				return None{}, nil
			}
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return None{}, nil
			}
			return Int(n), nil
		},
		"toFloatOrNone": func(recv Value, _ []Value) (Value, *Fault) {
			norm, ok := decimal.NormalizeText(string(recv.(Str)))
			if !ok {
				return None{}, nil
			}
			f, err := strconv.ParseFloat(norm, 64)
			v, ok := NewFloat(f)
			if err != nil || !ok {
				return None{}, nil // out of range: no text produces an infinity
			}
			return v, nil
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
		// ADR 0068 §3: lowercase hex; RFC 4648 §4 padded; §5 unpadded.
		"toHex": func(recv Value, _ []Value) (Value, *Fault) {
			return Str(hex.EncodeToString(recv.(Bytes).B)), nil
		},
		"toBase64": func(recv Value, _ []Value) (Value, *Fault) {
			return Str(base64.StdEncoding.EncodeToString(recv.(Bytes).B)), nil
		},
		"toBase64Url": func(recv Value, _ []Value) (Value, *Fault) {
			return Str(base64.RawURLEncoding.EncodeToString(recv.(Bytes).B)), nil
		},
		// ADR 0085 §7.
		"xor": func(recv Value, args []Value) (Value, *Fault) {
			a, b := recv.(Bytes).B, args[0].(Bytes).B
			if len(a) != len(b) {
				return nil, Faultf("bytes.xor: the two are %d and %d bytes; xor needs the same length", len(a), len(b))
			}
			out := make([]byte, len(a))
			for i := range a {
				out[i] = a[i] ^ b[i]
			}
			return Bytes{B: out}, nil
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
		// ADR 0129: new lists. take and drop clamp to the length, share the
		// receiver's storage as slice does, and fault on a negative count.
		"reverse": func(recv Value, _ []Value) (Value, *Fault) {
			elems := recv.(List).Elems
			out := make([]Value, len(elems))
			for i, e := range elems {
				out[len(elems)-1-i] = e
			}
			return List{Elems: out}, nil
		},
		"take": func(recv Value, args []Value) (Value, *Fault) {
			elems := recv.(List).Elems
			n, f := countArg("take", args[0], len(elems))
			if f != nil {
				return nil, f
			}
			return List{Elems: elems[:n:n]}, nil
		},
		"drop": func(recv Value, args []Value) (Value, *Fault) {
			elems := recv.(List).Elems
			n, f := countArg("drop", args[0], len(elems))
			if f != nil {
				return nil, f
			}
			return List{Elems: elems[n:len(elems):len(elems)]}, nil
		},
		// ADR 0129 §2a: the receiver's elements then other's, in a list of
		// its own, so neither input's spare capacity is claimed (ADR 0057 §5).
		"concat": func(recv Value, args []Value) (Value, *Fault) {
			first := recv.(List).Elems
			other, ok := args[0].(List)
			if !ok {
				return nil, Faultf("concat takes a list, got %s", args[0])
			}
			out := make([]Value, 0, len(first)+len(other.Elems))
			out = append(append(out, first...), other.Elems...)
			return List{Elems: out}, nil
		},
		// The first of each run of equal elements, in order, by == (a Map
		// keys by it, so a decimal matches by value).
		"distinct": func(recv Value, _ []Value) (Value, *Fault) {
			var seen Map
			var out []Value
			for _, e := range recv.(List).Elems {
				if _, dup := seen.Get(e); dup {
					continue
				}
				seen = seen.Put(e, Bool(true))
				out = append(out, e)
			}
			return List{Elems: out}, nil
		},
		"indexOfOrNone": func(recv Value, args []Value) (Value, *Fault) {
			for i, e := range recv.(List).Elems {
				if Equal(e, args[0]) {
					return Int(i), nil
				}
			}
			return None{}, nil
		},
		// ADR 0065 §2: a new list, sorted stably. Descending is its own
		// stable sort, not the ascending one reversed, which would reverse
		// ties (1.5 and 1.50 print differently).
		"sorted":           func(recv Value, _ []Value) (Value, *Fault) { return sortedList(recv.(List).Elems, false) },
		"sortedDescending": func(recv Value, _ []Value) (Value, *Fault) { return sortedList(recv.(List).Elems, true) },
		// ADR 0067 §5: the checker allows it on List<string> only.
		"join": func(recv Value, args []Value) (Value, *Fault) {
			elems := recv.(List).Elems
			parts := make([]string, len(elems))
			for i, e := range elems {
				parts[i] = string(e.(Str))
			}
			return Str(strings.Join(parts, string(args[0].(Str)))), nil
		},
	},
	"Map": mapMethods,
	// ADR 0148 §2: a batch's one method.
	"BatchResult": {
		"length": func(recv Value, _ []Value) (Value, *Fault) { return Int(len(recv.(BatchResult).Items)), nil },
	},
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
	"atOr": func(recv Value, args []Value) (Value, *Fault) {
		if v, ok := recv.(Map).Get(args[0]); ok {
			return v, nil
		}
		return args[1], nil
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
// stringTest, stringOp, and stringMap adapt a Go string function to a
// string method's body.
func stringTest(f func(s, arg string) bool) builtinFunc {
	return func(recv Value, args []Value) (Value, *Fault) {
		return Bool(f(string(recv.(Str)), string(args[0].(Str)))), nil
	}
}

func stringOp(f func(s, arg string) string) builtinFunc {
	return func(recv Value, args []Value) (Value, *Fault) {
		return Str(f(string(recv.(Str)), string(args[0].(Str)))), nil
	}
}

func stringMap(f func(string) string) builtinFunc {
	return func(recv Value, _ []Value) (Value, *Fault) {
		return Str(f(string(recv.(Str)))), nil
	}
}

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
	case BatchResult:
		return "BatchResult"
	case Secret:
		return "Secret"
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

// countArg is take's and drop's count, clamped to length.
func countArg(method string, v Value, length int) (int, *Fault) {
	n, ok := v.(Int)
	if !ok {
		return 0, Faultf("List.%s argument must be an int (got %s)", method, v)
	}
	if n < 0 {
		return 0, Faultf("%s(%d): the count is negative", method, n)
	}
	if int64(n) > int64(length) {
		return length, nil
	}
	return int(n), nil
}

// pad is s padded to width grapheme clusters with fill, repeated and its last
// copy cut to fit (ADR 0129); s as it is when it is already that wide.
func pad(left bool) builtinFunc {
	return func(recv Value, args []Value) (Value, *Fault) {
		s := string(recv.(Str))
		width, ok := args[0].(Int)
		if !ok {
			return nil, Faultf("padLeft/padRight width must be an int (got %s)", args[0])
		}
		fill := string(args[1].(Str))
		if fill == "" {
			return nil, Faultf("padLeft/padRight: the fill is empty")
		}
		have := len(text.GraphemeOffsets(s)) - 1
		need := int64(width) - int64(have)
		if need <= 0 {
			return Str(s), nil
		}
		fo := text.GraphemeOffsets(fill)
		var b strings.Builder
		for i := int64(0); i < need; i++ {
			k := int(i % int64(len(fo)-1))
			b.WriteString(fill[fo[k]:fo[k+1]])
		}
		if left {
			return Str(b.String() + s), nil
		}
		return Str(s + b.String()), nil
	}
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

func decodedOrNone(b []byte, err error) (Value, *Fault) {
	if err != nil {
		return None{}, nil
	}
	return Bytes{B: b}, nil
}

// decodeBase64 reads base64 in padded's alphabet (ADR 0069 §7): CR and LF
// are skipped anywhere, since MIME and PEM wrap it; the = padding is
// optional, but if present must be complete; and leftover bits in the
// last symbol are ignored, as Go's non-strict decoder ignores them. Any
// other character, a space or the other alphabet's, is an error.
func decodeBase64(s string, padded *base64.Encoding) ([]byte, error) {
	s = strings.NewReplacer("\r", "", "\n", "").Replace(s)
	if strings.HasSuffix(s, "=") {
		return padded.DecodeString(s)
	}
	return padded.WithPadding(base64.NoPadding).DecodeString(s)
}
