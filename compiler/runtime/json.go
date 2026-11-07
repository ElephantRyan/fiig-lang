package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// DecodeJSON turns a JSON value from ParseJSON (string/json.Number/bool/
// nil/[]any/map[string]any) into a Value, guided by t — a checked parameter/field type,
// resolved via types.Resolve against the program's Universe. Without that
// guidance there'd be no way to tell which model a JSON object decodes
// into, or decide which member of a
// `T | None` union a JSON value belongs to. Used by tooling/run-cli for
// -args, and by compiler/adapter for an HTTP route's request body.
func DecodeJSON(raw any, t types.Type, u *types.Universe) (Value, error) {
	switch t := t.(type) {
	case types.Primitive:
		switch t.Name {
		case "int":
			num, ok := raw.(json.Number)
			if !ok {
				return nil, fmt.Errorf("expected a number, got %s", jsonKind(raw))
			}
			n, err := jsonInt(string(num))
			if err != nil {
				return nil, err
			}
			return Int(n), nil
		case "float":
			num, ok := raw.(json.Number)
			if !ok {
				return nil, fmt.Errorf("expected a number, got %s", jsonKind(raw))
			}
			return jsonFloat(string(num))
		case "decimal":
			num, ok := raw.(json.Number)
			if !ok {
				return nil, fmt.Errorf("expected a number, got %s", jsonKind(raw))
			}
			return jsonDecimal(string(num))
		case "string":
			s, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("expected a string, got %s", jsonKind(raw))
			}
			return Str(s), nil
		case "bool":
			b, ok := raw.(bool)
			if !ok {
				return nil, fmt.Errorf("expected a boolean, got %s", jsonKind(raw))
			}
			return Bool(b), nil
		}
		return nil, fmt.Errorf("unsupported primitive %q", t.Name)
	case types.NoneType:
		if raw != nil {
			return nil, fmt.Errorf("expected null for None, got %s", jsonKind(raw))
		}
		return None{}, nil
	case types.Union:
		if raw == nil {
			for _, m := range t.Members {
				if _, isNone := m.(types.NoneType); isNone {
					return None{}, nil
				}
			}
			return nil, fmt.Errorf("null given but %s has no None member", t.String())
		}
		var lastErr error
		for _, m := range t.Members {
			if _, isNone := m.(types.NoneType); isNone {
				continue
			}
			v, err := DecodeJSON(raw, m, u)
			if err == nil {
				return v, nil
			}
			lastErr = err
		}
		return nil, fmt.Errorf("no member of %s matched: %w", t.String(), lastErr)
	case types.Generic:
		if t.Name == "List" && len(t.Args) == 1 {
			arr, ok := raw.([]any)
			if !ok {
				return nil, fmt.Errorf("expected an array, got %s", jsonKind(raw))
			}
			elems := make([]Value, len(arr))
			for i, e := range arr {
				v, err := DecodeJSON(e, t.Args[0], u)
				if err != nil {
					return nil, fmt.Errorf("[%d]: %w", i, err)
				}
				elems[i] = v
			}
			return List{Elems: elems}, nil
		}
		if t.Name == "Map" && len(t.Args) == 2 && t.Args[0].String() == "string" {
			// The parser doesn't keep member order, so members are taken in
			// name order; keeping arrival order is ADR 0062's.
			obj, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("expected an object, got %s", jsonKind(raw))
			}
			names := make([]string, 0, len(obj))
			for n := range obj {
				names = append(names, n)
			}
			sort.Strings(names)
			m := Map{}
			for _, n := range names {
				v, err := DecodeJSON(obj[n], t.Args[1], u)
				if err != nil {
					return nil, fmt.Errorf("%q: %w", n, err)
				}
				m = m.Put(Str(n), v)
			}
			return m, nil
		}
		return nil, fmt.Errorf("%s cannot be decoded from a plain JSON value", t.String())
	case types.Named:
		switch decl := t.Decl.(type) {
		case *syntax.ModelDecl:
			obj, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("expected an object for %s, got %s", decl.Name, jsonKind(raw))
			}
			all := decl.AllFields()
			fields := make(map[string]Value, len(all))
			for _, f := range all {
				ft, ok := types.Resolve(f.Type, u)
				if !ok {
					return nil, fmt.Errorf("%s.%s: unresolvable field type", decl.Name, f.Name)
				}
				raw, present := obj[f.Name]
				if !present {
					if IsOptional(ft) {
						fields[f.Name] = None{}
						continue
					}
					return nil, fmt.Errorf("%s missing required field %q", decl.Name, f.Name)
				}
				v, err := DecodeJSON(raw, ft, u)
				if err != nil {
					return nil, fmt.Errorf("%s.%s: %w", decl.Name, f.Name, err)
				}
				fields[f.Name] = v
			}
			return Record{TypeName: decl.Name, Fields: fields}, nil
		default:
			return nil, fmt.Errorf("%s cannot be decoded from a plain JSON value", t.String())
		}
	default:
		return nil, fmt.Errorf("%s cannot be decoded from a plain JSON value", t.String())
	}
}

// IsOptional reports whether t is a union including None (§3.5: "model
// fields are required unless their type includes None") — used to default
// a missing JSON/body field to None instead of erroring.
func IsOptional(t types.Type) bool {
	u, ok := t.(types.Union)
	if !ok {
		return false
	}
	for _, m := range u.Members {
		if _, isNone := m.(types.NoneType); isNone {
			return true
		}
	}
	return false
}

// DecodeUntypedJSON decodes raw JSON into a Value with no type guidance at
// all — used where there's no checked type to decode against, e.g. a
// scripted capability's canned responses (tooling/run-cli/caps.go): no
// capability-contract system exists (Milestone 5's own scoping note) to
// say what type a capability call is supposed to return. A number becomes
// an int by ADR 0062 §2's value rule; any other number is an error until
// float and decimal are implemented (ADR 0062 §6), never a truncation.
func DecodeUntypedJSON(raw any) (Value, error) {
	switch raw := raw.(type) {
	case nil:
		return None{}, nil
	case bool:
		return Bool(raw), nil
	case json.Number:
		n, err := jsonInt(string(raw))
		if err != nil {
			return nil, fmt.Errorf("%w (only whole numbers are supported until float and decimal are implemented)", err)
		}
		return Int(n), nil
	case string:
		return Str(raw), nil
	case []any:
		elems := make([]Value, len(raw))
		for i, e := range raw {
			v, err := DecodeUntypedJSON(e)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			elems[i] = v
		}
		return List{Elems: elems}, nil
	case map[string]any:
		fields := make(map[string]Value, len(raw))
		for k, v := range raw {
			fv, err := DecodeUntypedJSON(v)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			fields[k] = fv
		}
		return Record{Fields: fields}, nil
	default:
		return nil, fmt.Errorf("unsupported JSON value %T (decode with ParseJSON)", raw)
	}
}

// jsonKind names a ParseJSON value's JSON type for an error message.
func jsonKind(raw any) string {
	switch raw.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case json.Number:
		return "a number"
	case string:
		return "a string"
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	}
	return fmt.Sprintf("%T", raw)
}

// EncodeJSON turns a Value into a plain JSON-able Go value, for anything
// that needs to hand a Value to encoding/json (tooling/run-cli's -json
// output, compiler/adapter's HTTP response bodies). Tagged shapes (model
// instances, error variants, Result, BatchResult) keep a "$kind"
// marker so the output stays unambiguous instead of silently flattening
// into plain objects/arrays.
func EncodeJSON(v Value) any { return encodeJSON(v, false) }

// EncodeBodyJSON is EncodeJSON for an HTTP body: a model instance is a
// plain object of its fields, the shape the adapter's OpenAPI document
// describes and the shape a request body is decoded from (ADR 0021). The
// other tagged shapes are as EncodeJSON writes them.
func EncodeBodyJSON(v Value) any { return encodeJSON(v, true) }

func encodeJSON(v Value, body bool) any {
	switch v := v.(type) {
	case Int:
		return int64(v)
	case Float:
		// Written as fiig prints it, so a whole float stays `3.0`.
		return json.Number(v.String())
	case Decimal:
		// Plain notation with the value's scale, so `1.50` stays `1.50`.
		return json.Number(v.String())
	case Str:
		return string(v)
	case Bool:
		return bool(v)
	case None:
		return nil
	case List:
		arr := make([]any, len(v.Elems))
		for i, e := range v.Elems {
			arr[i] = encodeJSON(e, body)
		}
		return arr
	case Map:
		// String keys make a JSON object, its members in the map's order;
		// any other key type is a list of [key, value] pairs.
		stringKeys := true
		v.Each(func(k, _ Value) bool {
			_, stringKeys = k.(Str)
			return stringKeys
		})
		if stringKeys {
			obj := orderedObject{}
			v.Each(func(k, val Value) bool {
				obj = append(obj, objectMember{string(k.(Str)), encodeJSON(val, body)})
				return true
			})
			return obj
		}
		pairs := []any{}
		v.Each(func(k, val Value) bool {
			pairs = append(pairs, []any{encodeJSON(k, body), encodeJSON(val, body)})
			return true
		})
		return pairs
	case Record:
		fields := make(map[string]any, len(v.Fields))
		for k, fv := range v.Fields {
			fields[k] = encodeJSON(fv, body)
		}
		if v.TypeName == "" || body {
			return fields
		}
		return map[string]any{"$type": v.TypeName, "fields": fields}
	case EnumValue:
		fields := make([]any, len(v.Fields))
		for i, f := range v.Fields {
			fields[i] = encodeJSON(f, body)
		}
		return map[string]any{"$enum": v.TypeName, "variant": v.Variant, "fields": fields}
	case Result:
		tag := "Err"
		if v.Ok {
			tag = "Ok"
		}
		return map[string]any{"$result": tag, "value": encodeJSON(v.Value, body)}
	case BatchResult:
		items := make([]any, len(v.Items))
		for i, it := range v.Items {
			items[i] = map[string]any{"key": encodeJSON(it.Key, body), "outcome": encodeJSON(it.Outcome, body)}
		}
		return items
	case Secret:
		// The checker keeps a Secret out of anything sent (ADR 0071 §4);
		// what `fiig run -json` prints is its name, never its value.
		return v.String()
	default:
		return nil
	}
}

// orderedObject is a JSON object whose members keep their order, which
// encoding/json's maps don't (it sorts their keys).
type orderedObject []objectMember

type objectMember struct {
	name  string
	value any
}

func (o orderedObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		name, err := json.Marshal(m.name)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(m.value)
		if err != nil {
			return nil, err
		}
		b.Write(name)
		b.WriteByte(':')
		b.Write(value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}
