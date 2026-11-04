package types

import "sort"

// Method is one entry of a builtin table: a receiver type's methods (ADR
// 0049 §2.6) or a primitive's operations (ADR 0060 §3, ADR 0068 §5). A call
// may pass its arguments by position or by these names (ADR 0068 §5).
type Method struct {
	Params []Param
	Result Type
}

// Param is one parameter of a builtin.
type Param struct {
	Name string
	Type Type
}

// ParamNames is m's parameter names, in order, for BindArgs.
func (m Method) ParamNames() []string {
	names := make([]string, len(m.Params))
	for i, p := range m.Params {
		names[i] = p.Name
	}
	return names
}

// p is a Param, to keep the tables below one entry per line.
func p(name string, t Type) Param { return Param{Name: name, Type: t} }

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
		"at":     {[]Param{p("index", intType)}, intType},
		"slice":  {[]Param{p("from", intType), p("to", intType)}, bytesType},
		"utf8":   {nil, Union{Members: []Type{stringType, NoneType{}}}},
		// ADR 0068 §3: bytes written as text.
		"toHex":       {nil, stringType},
		"toBase64":    {nil, stringType},
		"toBase64Url": {nil, stringType},
	},
	"string": {
		"utf8":     {nil, bytesType},
		"isBlank":  {nil, boolType},
		"contains": {[]Param{p("text", stringType)}, boolType},
		// ADR 0064 §8, widened by ADR 0067 §7: the text number grammar,
		// with §7's scale.
		"toDecimalOrNone": {nil, Union{Members: []Type{Primitive{Name: "decimal"}, NoneType{}}}},
		// ADR 0067. length and slice count grapheme clusters; the bytes
		// are utf8().length().
		"length":             {nil, intType},
		"codePointCount":     {nil, intType},
		"slice":              {[]Param{p("from", intType), p("to", intType)}, stringType},
		"startsWith":         {[]Param{p("prefix", stringType)}, boolType},
		"endsWith":           {[]Param{p("suffix", stringType)}, boolType},
		"removePrefix":       {[]Param{p("prefix", stringType)}, stringType},
		"removeSuffix":       {[]Param{p("suffix", stringType)}, stringType},
		"trim":               {nil, stringType},
		"trimStart":          {nil, stringType},
		"trimEnd":            {nil, stringType},
		"lowercase":          {nil, stringType},
		"uppercase":          {nil, stringType},
		"equalsIgnoringCase": {[]Param{p("other", stringType)}, boolType},
		"split":              {[]Param{p("separator", stringType)}, Generic{Name: "List", Args: []Type{stringType}}},
		"replaceAll":         {[]Param{p("old", stringType), p("new", stringType)}, stringType},
		"toIntOrNone":        {nil, Union{Members: []Type{intType, NoneType{}}}},
		"toFloatOrNone":      {nil, Union{Members: []Type{Primitive{Name: "float"}, NoneType{}}}},
	},
}

// AppendParams is the append builtin's parameters (ADR 0057), which a
// call may also name (ADR 0068 §5).
var AppendParams = []string{"list", "element"}

// primitiveOps is the operations of each primitive whose calls the checker
// types (ADR 0060 §3, ADR 0068 §5). They come from this table, not from
// fiig source. Clock and FileSystem aren't typed yet.
var primitiveOps = map[string]map[string]Method{
	"Socket": {
		"connect":    {[]Param{p("host", stringType), p("port", intType)}, socketResult(ConnectionType)},
		"connectTls": {[]Param{p("host", stringType), p("port", intType)}, socketResult(ConnectionType)},
		"startTls":   {[]Param{p("connection", ConnectionType), p("serverName", stringType)}, socketResult(ConnectionType)},
		"read":       {[]Param{p("connection", ConnectionType)}, socketResult(bytesType)},
		"write":      {[]Param{p("connection", ConnectionType), p("data", bytesType)}, socketResult(NoneType{})},
		"close":      {[]Param{p("connection", ConnectionType)}, NoneType{}},
	},
	"Random": { // ADR 0068 §2
		"bytes": {[]Param{p("count", intType)}, bytesType},
		"int":   {[]Param{p("min", intType), p("max", intType)}, intType},
		"float": {nil, Primitive{Name: "float"}},
		"uuid":  {nil, stringType},
	},
}

func socketResult(ok Type) Type {
	return Generic{Name: "Result", Args: []Type{ok, SocketErrorType}}
}

// PrimitiveOps is the operation table of a typed primitive, by name; false
// for a primitive whose calls aren't typed yet.
func PrimitiveOps(name string) (map[string]Method, bool) {
	ops, ok := primitiveOps[name]
	return ops, ok
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
		"at":               {[]Param{p("index", intType)}, elem},
		"atOrNone":         {[]Param{p("index", intType)}, orNone},
		"slice":            {[]Param{p("from", intType), p("to", intType)}, list},
		"first":            {nil, elem},
		"firstOrNone":      {nil, orNone},
		"last":             {nil, elem},
		"lastOrNone":       {nil, orNone},
		"contains":         {[]Param{p("element", elem)}, boolType},
		"sorted":           {nil, list},
		"sortedDescending": {nil, list},
		// ADR 0067 §5: on List<string> only, which the checker enforces.
		"join": {[]Param{p("separator", stringType)}, stringType},
	}
}

// mapMethods is Map<K, V>'s table (ADR 0065 §6). put, putIfAbsent, and
// remove return a new map; a map is never changed in place.
func mapMethods(key, val Type) map[string]Method {
	m := Generic{Name: "Map", Args: []Type{key, val}}
	return map[string]Method{
		"at":          {[]Param{p("key", key)}, val},
		"atOrNone":    {[]Param{p("key", key)}, Union{Members: []Type{val, NoneType{}}}},
		"containsKey": {[]Param{p("key", key)}, boolType},
		"length":      {nil, intType},
		"keys":        {nil, Generic{Name: "List", Args: []Type{key}}},
		"values":      {nil, Generic{Name: "List", Args: []Type{val}}},
		"put":         {[]Param{p("key", key), p("value", val)}, m},
		"putIfAbsent": {[]Param{p("key", key), p("value", val)}, m},
		"remove":      {[]Param{p("key", key)}, m},
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
		if t.Name == "Map" && len(t.Args) == 2 {
			return "Map", mapMethods(t.Args[0], t.Args[1]), true
		}
	}
	return "", nil, false
}

// MethodKinds lists every receiver kind with a table, for the runtime's
// check that it implements exactly the methods declared here.
func MethodKinds() []string {
	kinds := []string{"List", "Map"}
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
	if kind == "Map" {
		return mapMethods(Unknown{}, Unknown{}), true
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
