package runtime

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// DecodeJSON turns a JSON value from ReadJSON or ParseJSON into a Value,
// guided by t, a checked type resolved against the program's Universe:
// without it there'd be no way to tell which model an object decodes into,
// or which member of a union a value belongs to. It is the one decoder
// (ADR 0073 §3): json.decode, an HTTP request body, and `fiig run -args`
// all read through it. A value that doesn't fit t is a
// *JsonMismatchError naming where.
func DecodeJSON(raw any, t types.Type, u *types.Universe) (Value, error) {
	return decodeJSON(raw, t, universeFields(u), "")
}

// fieldTypes resolves a model's or variant's field type: in the declaring
// package's scope for json.decode (Interp.fieldType), or in one Universe.
type fieldTypes func(owner syntax.Decl, t syntax.Type) (types.Type, bool)

func universeFields(u *types.Universe) fieldTypes {
	return func(_ syntax.Decl, t syntax.Type) (types.Type, bool) { return types.Resolve(t, u) }
}

func mismatch(path, format string, args ...any) error {
	return &JsonMismatchError{Path: path, Reason: fmt.Sprintf(format, args...)}
}

// memberPath is path's member name, written `a.b`, or `a["content-type"]`
// for a name that isn't an identifier.
func memberPath(path, name string) string {
	ident := name != ""
	for i, c := range name {
		if !(c == '_' || unicode.IsLetter(c) || i > 0 && unicode.IsDigit(c)) {
			ident = false
		}
	}
	switch {
	case !ident:
		return path + "[" + strconv.Quote(name) + "]"
	case path == "":
		return name
	}
	return path + "." + name
}

// asObject is raw as an ordered object: ReadJSON's own, or ParseJSON's map
// with its members in name order.
func asObject(raw any) (*jsonObject, bool) {
	switch raw := raw.(type) {
	case *jsonObject:
		return raw, true
	case map[string]any:
		obj := &jsonObject{members: raw}
		for n := range raw {
			obj.names = append(obj.names, n)
		}
		sort.Strings(obj.names)
		return obj, true
	}
	return nil, false
}

func decodeJSON(raw any, t types.Type, u fieldTypes, path string) (Value, error) {
	switch t := t.(type) {
	case types.Primitive:
		switch t.Name {
		case "int", "float", "decimal":
			num, ok := raw.(json.Number)
			if !ok {
				return nil, mismatch(path, "expected a number, got %s", jsonKind(raw))
			}
			var v Value
			var err error
			switch t.Name {
			case "int":
				var n int64
				n, err = jsonInt(string(num))
				v = Int(n)
			case "float":
				v, err = jsonFloat(string(num))
			default:
				v, err = jsonDecimal(string(num))
			}
			if err != nil {
				return nil, mismatch(path, "%v", err)
			}
			return v, nil
		case "string":
			s, ok := raw.(string)
			if !ok {
				return nil, mismatch(path, "expected a string, got %s", jsonKind(raw))
			}
			return Str(s), nil
		case "bool":
			b, ok := raw.(bool)
			if !ok {
				return nil, mismatch(path, "expected a boolean, got %s", jsonKind(raw))
			}
			return Bool(b), nil
		case "bytes":
			s, ok := raw.(string)
			if !ok {
				return nil, mismatch(path, "expected a base64 string, got %s", jsonKind(raw))
			}
			b, err := decodeJSONBase64(s)
			if err != nil {
				return nil, mismatch(path, "%v", err)
			}
			return Bytes{B: b}, nil
		}
	case types.NoneType:
		if raw != nil {
			return nil, mismatch(path, "expected null for None, got %s", jsonKind(raw))
		}
		return None{}, nil
	case types.Union:
		if raw == nil {
			for _, m := range t.Members {
				if _, isNone := m.(types.NoneType); isNone {
					return None{}, nil
				}
			}
			return nil, mismatch(path, "null given but %s has no None member", t.String())
		}
		var lastErr error
		for _, m := range t.Members {
			if _, isNone := m.(types.NoneType); isNone {
				continue
			}
			v, err := decodeJSON(raw, m, u, path)
			if err == nil {
				return v, nil
			}
			lastErr = err
		}
		return nil, mismatch(path, "no member of %s matched: %v", t.String(), lastErr)
	case types.Generic:
		switch {
		case t.Name == "List" && len(t.Args) == 1:
			arr, ok := raw.([]any)
			if !ok {
				return nil, mismatch(path, "expected an array, got %s", jsonKind(raw))
			}
			elems := make([]Value, len(arr))
			for i, e := range arr {
				v, err := decodeJSON(e, t.Args[0], u, fmt.Sprintf("%s[%d]", path, i))
				if err != nil {
					return nil, err
				}
				elems[i] = v
			}
			return List{Elems: elems}, nil
		case t.Name == "Map" && len(t.Args) == 2 && t.Args[0].String() == "string":
			// Members in the order they arrived (ADR 0062 §1).
			obj, ok := asObject(raw)
			if !ok {
				return nil, mismatch(path, "expected an object, got %s", jsonKind(raw))
			}
			m := Map{}
			for _, n := range obj.names {
				v, err := decodeJSON(obj.members[n], t.Args[1], u, memberPath(path, n))
				if err != nil {
					return nil, err
				}
				m = m.Put(Str(n), v)
			}
			return m, nil
		case t.Name == "Result" && len(t.Args) == 2:
			return decodeResult(raw, t, u, path)
		}
	case types.Record:
		obj, ok := asObject(raw)
		if !ok {
			return nil, mismatch(path, "expected an object, got %s", jsonKind(raw))
		}
		fields := make(map[string]Value, len(t.Fields))
		for n, ft := range t.Fields {
			fraw, present := obj.get(n)
			if !present && IsOptional(ft) {
				fields[n] = None{}
				continue
			}
			if !present {
				return nil, mismatch(path, "missing required field %q", n)
			}
			v, err := decodeJSON(fraw, ft, u, memberPath(path, n))
			if err != nil {
				return nil, err
			}
			fields[n] = v
		}
		return Record{Fields: fields}, nil
	case types.Named:
		switch decl := t.Decl.(type) {
		case *syntax.ModelDecl:
			obj, ok := asObject(raw)
			if !ok {
				return nil, mismatch(path, "expected an object for %s, got %s", decl.Name, jsonKind(raw))
			}
			all := decl.AllFields()
			fields := make(map[string]Value, len(all))
			for _, f := range all {
				v, err := decodeField(obj, f, decl, decl.Name, u, path)
				if err != nil {
					return nil, err
				}
				fields[f.Name] = v
			}
			return Record{TypeName: decl.Name, Fields: fields, Decl: decl}, nil
		case *syntax.EnumDecl:
			if decl == types.JsonValueDecl {
				return jsonTree(raw), nil
			}
			return decodeEnum(raw, decl, u, path)
		}
	}
	return nil, mismatch(path, "%s can't be decoded from JSON", t.String())
}

// decodeField reads one model or payload field from obj, by its JSON name
// (ADR 0073 §7b). A missing `T | None` field is None; any other is an
// error naming owner, the model or variant.
func decodeField(obj *jsonObject, f syntax.Field, decl syntax.Decl, owner string, u fieldTypes, path string) (Value, error) {
	ft, ok := u(decl, f.Type)
	if !ok {
		return nil, mismatch(path, "%s.%s has a type that doesn't resolve", owner, f.Name)
	}
	raw, present := obj.get(f.JSONName())
	if !present {
		if IsOptional(ft) {
			return None{}, nil
		}
		return nil, mismatch(path, "%s missing required field %q", owner, f.JSONName())
	}
	return decodeJSON(raw, ft, u, memberPath(path, f.JSONName()))
}

// decodeEnum reads the shapes the body encoder writes (ADR 0072 §5): a
// variant's name for an enum with no payloads, and an object whose
// `variant` member names one, its payload fields beside it, for an enum
// with any. Members the variant doesn't declare are ignored.
func decodeEnum(raw any, decl *syntax.EnumDecl, u fieldTypes, path string) (Value, error) {
	if !decl.HasPayload() {
		name, ok := raw.(string)
		if !ok {
			return nil, mismatch(path, "expected a string naming a variant of %s (%s), got %s", decl.Name, variantNames(decl), jsonKind(raw))
		}
		if _, ok := decl.Variant(name); !ok {
			return nil, mismatch(path, "%q is not a variant of %s (%s)", name, decl.Name, variantNames(decl))
		}
		return EnumValue{TypeName: decl.Name, Variant: name, Decl: decl}, nil
	}
	obj, ok := asObject(raw)
	if !ok {
		return nil, mismatch(path, "expected an object with a \"variant\" member for %s, got %s", decl.Name, jsonKind(raw))
	}
	tag, _ := obj.get("variant")
	name, ok := tag.(string)
	if !ok {
		return nil, mismatch(path, "%s needs a \"variant\" member naming one of %s", decl.Name, variantNames(decl))
	}
	variant, ok := decl.Variant(name)
	if !ok {
		return nil, mismatch(memberPath(path, "variant"), "%q is not a variant of %s (%s)", name, decl.Name, variantNames(decl))
	}
	fields := make([]Value, len(variant.Fields))
	for i, f := range variant.Fields {
		v, err := decodeField(obj, f, decl, decl.Name+"."+name, u, path)
		if err != nil {
			return nil, err
		}
		fields[i] = v
	}
	return EnumValue{TypeName: decl.Name, Variant: name, Fields: fields, Decl: decl}, nil
}

// variantNames lists d's variants for an error message: "US, EU, APAC".
func variantNames(d *syntax.EnumDecl) string {
	names := make([]string, len(d.Variants))
	for i, v := range d.Variants {
		names[i] = v.Name
	}
	return strings.Join(names, ", ")
}

// decodeResult reads `{"variant": "Ok", "value": ...}` or
// `{"variant": "Err", "error": ...}` (ADR 0072 §4).
func decodeResult(raw any, t types.Generic, u fieldTypes, path string) (Value, error) {
	obj, ok := asObject(raw)
	if !ok {
		return nil, mismatch(path, "expected an object with a \"variant\" member for %s, got %s", t.String(), jsonKind(raw))
	}
	var isOk bool
	var member string
	var mt types.Type
	tag, _ := obj.get("variant")
	switch tag {
	case "Ok":
		isOk, member, mt = true, "value", t.Args[0]
	case "Err":
		member, mt = "error", t.Args[1]
	default:
		return nil, mismatch(path, "%s needs a \"variant\" member naming Ok or Err", t.String())
	}
	mraw, present := obj.get(member)
	if !present {
		if _, isNone := mt.(types.NoneType); !isNone && !IsOptional(mt) {
			return nil, mismatch(path, "%s %s missing %q", t.String(), tag, member)
		}
	}
	v, err := decodeJSON(mraw, mt, u, memberPath(path, member))
	if err != nil {
		return nil, err
	}
	return Result{Ok: isOk, Value: v}, nil
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

// jsonValue builds one JsonValue variant.
func jsonValue(variant string, fields ...Value) EnumValue {
	return EnumValue{TypeName: "JsonValue", Variant: variant, Fields: fields, Decl: types.JsonValueDecl}
}

// jsonTree is raw as a JsonValue (ADR 0073 §4): numbers keep their text,
// and members the order they arrived in.
func jsonTree(raw any) Value {
	switch raw := raw.(type) {
	case nil:
		return jsonValue("Null")
	case bool:
		return jsonValue("Bool", Bool(raw))
	case json.Number:
		return jsonValue("Number", Str(raw))
	case string:
		return jsonValue("Text", Str(raw))
	case []any:
		items := make([]Value, len(raw))
		for i, e := range raw {
			items[i] = jsonTree(e)
		}
		return jsonValue("Array", List{Elems: items})
	}
	obj, _ := asObject(raw)
	m := Map{}
	for _, n := range obj.names {
		m = m.Put(Str(n), jsonTree(obj.members[n]))
	}
	return jsonValue("Object", m)
}

// jsonRaw is a JsonValue as the raw shape DecodeJSON reads, so a subtree
// decodes by the same rules bytes do (ADR 0073 §4). A Number whose text
// isn't a JSON number, which only the program's own code can build, is an
// error (ADR 0062 §5).
func jsonRaw(v Value) (any, error) {
	ev, ok := v.(EnumValue)
	if !ok || ev.Decl != types.JsonValueDecl {
		return nil, fmt.Errorf("%s is not a JsonValue", v)
	}
	switch ev.Variant {
	case "Null":
		return nil, nil
	case "Bool":
		return bool(ev.Fields[0].(Bool)), nil
	case "Number":
		text := string(ev.Fields[0].(Str))
		if !IsJSONNumber(text) {
			return nil, fmt.Errorf("JsonValue.Number(text: %q) is not a JSON number", text)
		}
		return json.Number(text), nil
	case "Text":
		return string(ev.Fields[0].(Str)), nil
	case "Array":
		items := ev.Fields[0].(List).Elems
		arr := make([]any, len(items))
		for i, e := range items {
			r, err := jsonRaw(e)
			if err != nil {
				return nil, err
			}
			arr[i] = r
		}
		return arr, nil
	}
	obj := &jsonObject{members: map[string]any{}}
	var err error
	ev.Fields[0].(Map).Each(func(k, val Value) bool {
		var r any
		if r, err = jsonRaw(val); err != nil {
			return false
		}
		n := string(k.(Str))
		obj.names = append(obj.names, n)
		obj.members[n] = r
		return true
	})
	return obj, err
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

// jsonKind names a parsed JSON value's type for an error message.
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
	case map[string]any, *jsonObject:
		return "an object"
	}
	return fmt.Sprintf("%T", raw)
}

// EncodeJSON turns a Value into a plain JSON-able Go value for
// `fiig run -json`, a tool's view of fiig values (ADR 0072 §7): models
// carry `$type`, enums `$enum`, and Results `$result`, so the output says
// what each value is. EncodeBodyJSON is the wire form.
func EncodeJSON(v Value) any {
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
	case Bytes:
		return base64.StdEncoding.EncodeToString(v.B) // ADR 0078 §1; was null
	case Bool:
		return bool(v)
	case None:
		return nil
	case List:
		arr := make([]any, len(v.Elems))
		for i, e := range v.Elems {
			arr[i] = EncodeJSON(e)
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
				obj = append(obj, objectMember{string(k.(Str)), EncodeJSON(val)})
				return true
			})
			return obj
		}
		pairs := []any{}
		v.Each(func(k, val Value) bool {
			pairs = append(pairs, []any{EncodeJSON(k), EncodeJSON(val)})
			return true
		})
		return pairs
	case Record:
		fields := make(map[string]any, len(v.Fields))
		for k, fv := range v.Fields {
			fields[k] = EncodeJSON(fv)
		}
		if v.TypeName == "" {
			return fields
		}
		return map[string]any{"$type": v.TypeName, "fields": fields}
	case EnumValue:
		fields := make([]any, len(v.Fields))
		for i, f := range v.Fields {
			fields[i] = EncodeJSON(f)
		}
		return map[string]any{"$enum": v.TypeName, "variant": v.Variant, "fields": fields}
	case Result:
		tag := "Err"
		if v.Ok {
			tag = "Ok"
		}
		return map[string]any{"$result": tag, "value": EncodeJSON(v.Value)}
	case BatchResult:
		items := make([]any, len(v.Items))
		for i, it := range v.Items {
			items[i] = map[string]any{"key": EncodeJSON(it.Key), "outcome": EncodeJSON(it.Outcome)}
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

// EncodeBodyJSON writes v as JSON, with no whitespace, in the shapes a
// request body is decoded from (ADR 0073 §6): what json.encode returns and
// an HTTP response body sends, byte for byte. A model is an object of its
// fields in declared order, by their JSON names; an enum and a Result take
// ADR 0072's shapes; a JsonValue is the JSON it holds; a Map with string
// keys is an object in its order, and with any other key a list of
// [key, value] pairs. A JsonValue.Number whose text isn't a JSON number
// (ADR 0062 §5) is an error, as is a value J1 keeps from reaching here.
func EncodeBodyJSON(v Value) ([]byte, error) {
	var b bytes.Buffer
	if err := writeBodyJSON(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeBodyJSON(b *bytes.Buffer, v Value) error {
	switch v := v.(type) {
	case Int:
		b.WriteString(strconv.FormatInt(int64(v), 10))
	case Float:
		b.WriteString(v.String())
	case Decimal:
		b.WriteString(v.String())
	case Str:
		writeJSONString(b, string(v))
	case Bytes:
		writeJSONString(b, base64.StdEncoding.EncodeToString(v.B)) // ADR 0078 §1
	case Bool:
		b.WriteString(strconv.FormatBool(bool(v)))
	case None:
		b.WriteString("null")
	case List:
		return writeJSONArray(b, len(v.Elems), func(i int) error { return writeBodyJSON(b, v.Elems[i]) })
	case Map:
		return writeJSONMap(b, v)
	case Record:
		var members []bodyMember
		if v.Decl != nil {
			for _, f := range v.Decl.AllFields() {
				members = append(members, bodyMember{f.JSONName(), v.Fields[f.Name]})
			}
		} else {
			names := make([]string, 0, len(v.Fields))
			for n := range v.Fields {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				members = append(members, bodyMember{n, v.Fields[n]})
			}
		}
		return writeJSONObject(b, members)
	case EnumValue:
		return writeEnumJSON(b, v)
	case Result:
		tag, name := "Err", "error"
		if v.Ok {
			tag, name = "Ok", "value"
		}
		return writeJSONObject(b, []bodyMember{{"variant", Str(tag)}, {name, v.Value}})
	case BatchResult:
		return writeJSONArray(b, len(v.Items), func(i int) error {
			return writeJSONObject(b, []bodyMember{{"key", v.Items[i].Key}, {"outcome", v.Items[i].Outcome}})
		})
	case Secret:
		return fmt.Errorf("%s can't be encoded as JSON", v)
	default:
		return fmt.Errorf("a %T value can't be encoded as JSON", v)
	}
	return nil
}

// bodyMember is one member of an object the body encoder writes.
type bodyMember struct {
	name  string
	value Value
}

// writeEnumJSON writes v as ADR 0072 sends it: `"US"` for an enum with no
// payloads, `{"variant": "NotFound", "id": "o1"}` for one with any, and a
// JsonValue as the JSON it holds (ADR 0073 §4).
func writeEnumJSON(b *bytes.Buffer, v EnumValue) error {
	switch {
	case v.Decl == types.JsonValueDecl:
		switch v.Variant {
		case "Null":
			b.WriteString("null")
			return nil
		case "Number":
			text := string(v.Fields[0].(Str))
			if !IsJSONNumber(text) {
				return fmt.Errorf("JsonValue.Number(text: %q) is not a JSON number", text)
			}
			b.WriteString(text)
			return nil
		}
		return writeBodyJSON(b, v.Fields[0])
	case v.Decl == nil || !v.Decl.HasPayload():
		writeJSONString(b, v.Variant)
		return nil
	}
	members := []bodyMember{{"variant", Str(v.Variant)}}
	variant, _ := v.Decl.Variant(v.Variant)
	for i, f := range variant.Fields {
		if i < len(v.Fields) {
			members = append(members, bodyMember{f.JSONName(), v.Fields[i]})
		}
	}
	return writeJSONObject(b, members)
}

func writeJSONMap(b *bytes.Buffer, m Map) error {
	stringKeys := true
	m.Each(func(k, _ Value) bool {
		_, stringKeys = k.(Str)
		return stringKeys
	})
	if stringKeys {
		var members []bodyMember
		m.Each(func(k, val Value) bool {
			members = append(members, bodyMember{string(k.(Str)), val})
			return true
		})
		return writeJSONObject(b, members)
	}
	var pairs [][2]Value
	m.Each(func(k, val Value) bool {
		pairs = append(pairs, [2]Value{k, val})
		return true
	})
	return writeJSONArray(b, len(pairs), func(i int) error {
		return writeJSONArray(b, 2, func(j int) error { return writeBodyJSON(b, pairs[i][j]) })
	})
}

func writeJSONObject(b *bytes.Buffer, members []bodyMember) error {
	b.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			b.WriteByte(',')
		}
		writeJSONString(b, m.name)
		b.WriteByte(':')
		if err := writeBodyJSON(b, m.value); err != nil {
			return err
		}
	}
	b.WriteByte('}')
	return nil
}

func writeJSONArray(b *bytes.Buffer, n int, elem func(i int) error) error {
	b.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		if err := elem(i); err != nil {
			return err
		}
	}
	b.WriteByte(']')
	return nil
}

// writeJSONString writes s quoted, escaping what RFC 8259 requires (`"`,
// a backslash, and control characters) plus U+2028 and U+2029, which
// JavaScript source can't hold raw. A byte that isn't UTF-8 is written as
// U+FFFD.
func writeJSONString(b *bytes.Buffer, s string) {
	const hex = "0123456789abcdef"
	b.WriteByte('"')
	for i := 0; i < len(s); {
		c, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(byte(c))
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xf])
		case c == '\u2028' || c == '\u2029':
			fmt.Fprintf(b, `\u%04x`, c)
		case c == utf8.RuneError && size == 1:
			b.WriteString(`\ufffd`)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	b.WriteByte('"')
}

// decodeJSONBase64 reads a JSON string as bytes (ADR 0078 §2): ADR 0069
// §7's base64 rules, in whichever alphabet the text is written, since a
// JSON member names none. Both alphabets in one string is an error.
func decodeJSONBase64(s string) ([]byte, error) {
	std := strings.ContainsAny(s, "+/")
	url := strings.ContainsAny(s, "-_")
	if std && url {
		return nil, fmt.Errorf("base64 mixes the standard alphabet (+ /) with the URL-safe one (- _)")
	}
	enc := base64.StdEncoding
	if url {
		enc = base64.URLEncoding
	}
	b, err := decodeBase64(s, enc)
	if err != nil {
		return nil, fmt.Errorf("isn't base64: %v", err)
	}
	return b, nil
}
