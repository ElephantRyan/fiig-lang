package runtime

import (
	"context"
	"errors"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0073: the prelude json group's two members.

// callJson runs json.decode or json.encode. decode reads its input with
// the reader request bodies use and decodes it into the type the checker
// recorded on the call (§2); input that isn't JSON or doesn't fit is an
// Err(JsonError), not a fault.
func (in *Interp) callJson(ctx context.Context, e *syntax.CallExpr, member string, env *Env) (Value, bool, *Fault) {
	vals, early, earlyReturn, fault := in.evalArgsPositional(ctx, e.Args, env)
	if fault != nil || earlyReturn {
		return early, earlyReturn, fault
	}
	if len(vals) != 1 {
		return nil, false, Faultf("json.%s takes one argument", member)
	}
	switch member {
	case "encode":
		b, err := EncodeBodyJSON(vals[0])
		if err != nil {
			return nil, false, Faultf("json.encode: %v", err)
		}
		return Bytes{B: b}, false, nil
	case "encodePretty":
		b, err := EncodePrettyJSON(vals[0])
		if err != nil {
			return nil, false, Faultf("json.encodePretty: %v", err)
		}
		return Bytes{B: b}, false, nil
	case "decode":
		into, ok := e.DecodeInto.(types.Type)
		if !ok {
			return nil, false, Faultf("json.decode has no type to decode into; the program wasn't checked")
		}
		var raw any
		var err error
		switch v := vals[0].(type) {
		case Str:
			raw, err = readJSON([]byte(v))
		case Bytes:
			raw, err = readJSON(v.B)
		default:
			// A JsonValue (§4). One whose Number text isn't a number was
			// built by the program, so it is a defect, as encoding it is.
			if raw, err = jsonRaw(v); err != nil {
				return nil, false, Faultf("json.decode: %v", err)
			}
		}
		if err == nil {
			var decoded Value
			if decoded, err = decodeJSON(raw, into, in.fieldType, ""); err == nil {
				return Result{Ok: true, Value: decoded}, false, nil
			}
		}
		return Result{Ok: false, Value: jsonError(err)}, false, nil
	}
	return nil, false, Faultf("the prelude json group has no member %q", member)
}

// ParamType resolves one of d's parameter types where d is declared: its
// package and its file's imports, as the checker did. compiler/adapter
// plans a route with it, so a body typed `kernel.Money` binds.
func (in *Interp) ParamType(d syntax.Decl, t syntax.Type) (types.Type, bool) {
	return in.fieldType(d, t)
}

// DecodeBodyJSON is DecodeJSON for a request body, each model's fields
// resolved in the package that declares it, as json.decode does.
func (in *Interp) DecodeBodyJSON(raw any, t types.Type) (Value, error) {
	return decodeJSON(raw, t, in.fieldType, "")
}

// fieldType resolves a field's type in the package that declares owner,
// so a model from a dependency decodes by that dependency's names.
func (in *Interp) fieldType(owner syntax.Decl, t syntax.Type) (types.Type, bool) {
	sc := in.declScope(owner)
	var imports map[string]*types.Universe
	for alias, pkg := range sc.imports {
		if imports == nil {
			imports = map[string]*types.Universe{}
		}
		imports[alias] = pkg.Universe
	}
	return types.ResolveWithImports(t, sc.universe, imports)
}

// jsonError is err as a JsonError value (§5).
func jsonError(err error) Value {
	var syn *JsonSyntaxError
	if errors.As(err, &syn) {
		return EnumValue{TypeName: "JsonError", Variant: "Syntax", Fields: []Value{Int(syn.Offset), Str(syn.Reason)}, Decl: types.JsonErrorDecl}
	}
	var mis *JsonMismatchError
	if errors.As(err, &mis) {
		return EnumValue{TypeName: "JsonError", Variant: "Mismatch", Fields: []Value{Str(mis.Path), Str(mis.Reason)}, Decl: types.JsonErrorDecl}
	}
	return EnumValue{TypeName: "JsonError", Variant: "Mismatch", Fields: []Value{Str(""), Str(err.Error())}, Decl: types.JsonErrorDecl}
}
