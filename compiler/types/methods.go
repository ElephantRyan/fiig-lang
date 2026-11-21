package types

import "sort"

// Method is one entry of a builtin table: a receiver type's methods (ADR
// 0049 §2.6) or a primitive's operations (ADR 0060 §3, ADR 0068 §5). A call
// may pass its arguments by position or by these names (ADR 0068 §5).
type Method struct {
	Params []Param
	Result Type
}

// Param is one parameter of a builtin. An Optional one may be left out
// of a call, and only trailing parameters are (ADR 0074 §5); a call that
// leaves it out passes None.
type Param struct {
	Name     string
	Type     Type
	Optional bool
}

// ParamNames is m's parameter names, in order, for BindArgs.
func (m Method) ParamNames() []string {
	names := make([]string, len(m.Params))
	for i, p := range m.Params {
		names[i] = p.Name
	}
	return names
}

// Required is how many of m's parameters a call must supply: all but the
// trailing optional ones.
func (m Method) Required() int {
	n := len(m.Params)
	for n > 0 && m.Params[n-1].Optional {
		n--
	}
	return n
}

// p is a Param, to keep the tables below one entry per line.
func p(name string, t Type) Param { return Param{Name: name, Type: t} }

// deadline is the optional last parameter of every Socket operation that
// can wait: a point in time, Unix milliseconds as Clock.now() returns
// them, after which it returns Err(SocketError.TimedOut) (ADR 0074 §5).
var deadline = Param{Name: "deadline", Type: Union{Members: []Type{intType, NoneType{}}}, Optional: true}

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
		// ADR 0085 §7: two strings of the same length, XORed.
		"xor": {[]Param{p("other", bytesType)}, bytesType},
	},
	"string": {
		"utf8": {nil, bytesType},
		// ADR 0069 §7: the inverses of bytes's toHex, toBase64, and
		// toBase64Url; None when the text isn't in that alphabet.
		"fromHex":       {nil, Union{Members: []Type{bytesType, NoneType{}}}},
		"fromBase64":    {nil, Union{Members: []Type{bytesType, NoneType{}}}},
		"fromBase64Url": {nil, Union{Members: []Type{bytesType, NoneType{}}}},
		"isBlank":       {nil, boolType},
		"contains":      {[]Param{p("text", stringType)}, boolType},
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
		// ADR 0129: width counts grapheme clusters, as length does; the fill
		// repeats and its last copy is cut to fit.
		"padLeft":  {[]Param{p("width", intType), p("fill", stringType)}, stringType},
		"padRight": {[]Param{p("width", intType), p("fill", stringType)}, stringType},
		// ADR 0126: a value a gateway reads from outside, such as a stored
		// token, marked as a Secret from there on; legal only in a gateway (S3).
		"toSecret": {[]Param{p("name", stringType)}, SecretType},
	},
}

// AppendParams is the append builtin's parameters (ADR 0057), which a
// call may also name (ADR 0068 §5).
var AppendParams = []string{"list", "element"}

// primitiveOps is the operations of each primitive whose calls the checker
// types (ADR 0060 §3, ADR 0068 §5). They come from this table, not from
// fiig source. FileSystem isn't typed yet.
var primitiveOps = map[string]map[string]Method{
	"Socket": {
		"connect":    {[]Param{p("host", stringType), p("port", intType), deadline}, socketResult(ConnectionType)},
		"connectTls": {[]Param{p("host", stringType), p("port", intType), deadline}, socketResult(ConnectionType)},
		"startTls":   {[]Param{p("connection", ConnectionType), p("serverName", stringType), deadline}, socketResult(ConnectionType)},
		"read":       {[]Param{p("connection", ConnectionType), deadline}, socketResult(bytesType)},
		"write":      {[]Param{p("connection", ConnectionType), p("data", bytesType), deadline}, socketResult(NoneType{})},
		"close":      {[]Param{p("connection", ConnectionType)}, NoneType{}},
		// ADR 0109: connections kept between calls.
		"reuse": {[]Param{p("host", stringType), p("port", intType), p("tls", boolType), p("session", stringType)}, Union{Members: []Type{ConnectionType, NoneType{}}}},
		"keep":  {[]Param{p("connection", ConnectionType), p("session", stringType)}, NoneType{}},
	},
	// ADR 0114 §3, ADR 0117 §2: a message to the session settings names,
	// running the calling package's own protocol; its reply's parts.
	"Session": {
		"call": {[]Param{p("settings", Generic{Name: "Map", Args: []Type{stringType, stringType}}), p("message", bytesType), deadline}, Generic{Name: "Result", Args: []Type{Generic{Name: "List", Args: []Type{bytesType}}, SessionErrorType}}},
	},
	// ADR 0068 §5 (amended 2026-11-20): Clock.now() is Unix milliseconds as an int, so a moment
	// can't be added to a string or returned as one.
	"Clock": {
		"now": {nil, intType},
		// ADR 0091 §5: in a test, moves the test clock forward and returns at
		// once. Only a test may call it (T2): program code has no sleep.
		"sleep": {[]Param{p("ms", intType)}, NoneType{}},
	},
	"Random": { // ADR 0068 §2
		"bytes": {[]Param{p("count", intType)}, bytesType},
		"int":   {[]Param{p("min", intType), p("max", intType)}, intType},
		"float": {nil, Primitive{Name: "float"}},
		"uuid":  {nil, stringType},
	},
}

// DatabaseName is the native capability whose calls ADR 0082 types. It
// stays on ADR 0042's grandfather list, not a primitive; the table is
// what the name means when no declaration takes it.
const DatabaseName = "Database"

// databaseOps is Database's operations (ADR 0082 §1). Each takes the SQL
// text, then the values to bind, by position, which a Method can't
// describe; the checker types those (Q2). A query's Result is nil: its
// row type comes from the call's context (§5).
var databaseOps = map[string]Method{
	"execute":   {[]Param{p("sql", stringType)}, intType},
	"queryOne":  {[]Param{p("sql", stringType)}, nil},
	"queryMany": {[]Param{p("sql", stringType)}, nil},
}

// DatabaseOps is Database's operation table.
func DatabaseOps() map[string]Method { return databaseOps }

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
		// ADR 0129: a new list, never the receiver changed. take and drop
		// clamp to the length; distinct keeps the first of equal elements
		// and, like contains, compares with ==.
		"reverse":       {nil, list},
		"take":          {[]Param{p("count", intType)}, list},
		"drop":          {[]Param{p("count", intType)}, list},
		"distinct":      {nil, list},
		"indexOfOrNone": {[]Param{p("element", elem)}, Union{Members: []Type{intType, NoneType{}}}},
		// ADR 0067 §5: on List<string> only, which the checker enforces.
		"join": {[]Param{p("separator", stringType)}, stringType},
	}
}

// mapMethods is Map<K, V>'s table (ADR 0065 §6, and atOr, ADR 0093 §5). put, putIfAbsent, and
// remove return a new map; a map is never changed in place.
func mapMethods(key, val Type) map[string]Method {
	m := Generic{Name: "Map", Args: []Type{key, val}}
	return map[string]Method{
		"at":          {[]Param{p("key", key)}, val},
		"atOrNone":    {[]Param{p("key", key)}, Union{Members: []Type{val, NoneType{}}}},
		"atOr":        {[]Param{p("key", key), p("default", val)}, val},
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
	case Opaque:
		if t == SecretType {
			return "Secret", secretMethods, true
		}
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
	kinds := []string{"List", "Map", "Secret"}
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
	if kind == "Secret" {
		return secretMethods, true
	}
	table, ok := primitiveMethods[kind]
	return table, ok
}

// secretMethods is ADR 0071 §4: reveal, legal only in a gateway (S2), and
// a constant-time comparison legal anywhere.
var secretMethods = map[string]Method{
	"reveal":  {nil, stringType},
	"matches": {[]Param{p("candidate", stringType)}, boolType},
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
