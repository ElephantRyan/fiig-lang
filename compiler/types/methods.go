package types

import "sort"

// Method is one entry of a receiver type's builtin method table (ADR 0049
// §2.6): the parameter types, positional, and the result.
type Method struct {
	Params []Type
	Result Type
}

var (
	intType    = Primitive{Name: "int"}
	stringType = Primitive{Name: "string"}
	boolType   = Primitive{Name: "bool"}
	bytesType  = Primitive{Name: "bytes"}
)

// primitiveMethods is the table for the primitive receivers. The table is
// closed: a model or enum declares no methods (ADR 0049 §2.6), so a
// receiver with no entry here or in listMethods has none.
var primitiveMethods = map[string]map[string]Method{
	"bytes": { // ADR 0058 §1
		"length": {nil, intType},
		"at":     {[]Type{intType}, intType},
		"slice":  {[]Type{intType, intType}, bytesType},
		"utf8":   {nil, Union{Members: []Type{stringType, NoneType{}}}},
	},
	"string": {
		"utf8":     {nil, bytesType},
		"isBlank":  {nil, boolType},
		"contains": {[]Type{stringType}, boolType},
		// ADR 0064 §8: JSON's number grammar, with §7's scale.
		"toDecimalOrNone": {nil, Union{Members: []Type{Primitive{Name: "decimal"}, NoneType{}}}},
	},
}

// listMethods is List<T>'s table (ADR 0049 §2.6, ADR 0065 §1-§2), for
// element type elem. contains needs a comparable element and the sorts an
// ordered one; the checker enforces both, since the table has no
// constraints.
func listMethods(elem Type) map[string]Method {
	list := Generic{Name: "List", Args: []Type{elem}}
	orNone := Union{Members: []Type{elem, NoneType{}}}
	return map[string]Method{
		"length":           {nil, intType},
		"at":               {[]Type{intType}, elem},
		"atOrNone":         {[]Type{intType}, orNone},
		"slice":            {[]Type{intType, intType}, list},
		"first":            {nil, elem},
		"firstOrNone":      {nil, orNone},
		"last":             {nil, elem},
		"lastOrNone":       {nil, orNone},
		"contains":         {[]Type{elem}, boolType},
		"sorted":           {nil, list},
		"sortedDescending": {nil, list},
	}
}

// MethodsOf is the method table for a receiver of type recv, and the name
// diagnostics call the receiver kind by ("string", "List"). ok is false for
// a type with no table, which has no methods at all. This one table is what
// compiler/checker types a method call against and what compiler/runtime
// dispatches through (ADR 0049 §2.8).
func MethodsOf(recv Type) (kind string, table map[string]Method, ok bool) {
	switch t := recv.(type) {
	case Primitive:
		table, ok = primitiveMethods[t.Name]
		return t.Name, table, ok
	case Generic:
		if t.Name == "List" && len(t.Args) == 1 {
			return "List", listMethods(t.Args[0]), true
		}
	}
	return "", nil, false
}

// MethodKinds lists every receiver kind with a table, for the runtime's
// check that it implements exactly the methods declared here.
func MethodKinds() []string {
	kinds := []string{"List"}
	for k := range primitiveMethods {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}

// MethodsOfKind is MethodsOf by kind name, with element types unknown: the
// runtime, which has values rather than types, needs only the names and
// arities.
func MethodsOfKind(kind string) (map[string]Method, bool) {
	if kind == "List" {
		return listMethods(Unknown{}), true
	}
	table, ok := primitiveMethods[kind]
	return table, ok
}

// MethodNames is a table's method names, sorted, for diagnostics.
func MethodNames(table map[string]Method) []string {
	names := make([]string, 0, len(table))
	for n := range table {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
