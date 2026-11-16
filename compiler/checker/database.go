package checker

import (
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0082: Database's calls, typed from types.DatabaseOps, and Q1-Q3.

// databaseCall is e when it calls Database's own operations: a receiver
// named Database that no local or declaration takes (§1).
func (c *checker) databaseCall(e syntax.Expr, env map[string]types.Type) (*syntax.CallExpr, string, bool) {
	call, ok := e.(*syntax.CallExpr)
	if !ok {
		return nil, "", false
	}
	fa, ok := call.Callee.(*syntax.FieldAccess)
	if !ok {
		return nil, "", false
	}
	target, ok := fa.Target.(*syntax.Ident)
	if !ok || target.Name != types.DatabaseName || !c.isNativeCapability(target.Name, env) {
		return nil, "", false
	}
	return call, fa.Field, true
}

// isNativeCapability reports whether name, as a call's receiver, is a
// capability with no declaration in fiig, which ADR 0082 §2 holds to its
// gateway's requires: capitalized (ADR 0043 §6), and not a local, a
// primitive, a declaration, a group, or a package alias.
func (c *checker) isNativeCapability(name string, env map[string]types.Type) bool {
	if !isUpper(name) {
		return false
	}
	if _, isLocal := env[name]; isLocal {
		return false
	}
	if _, isGroup := c.groups[name]; isGroup {
		return false
	}
	if _, isImport := c.imports[name]; isImport {
		return false
	}
	if _, isAlias := c.aliases[name]; isAlias {
		return false
	}
	if _, found := c.lookup(name); found {
		return false
	}
	return true
}

// checkExpectedQuery handles a Database query where context gives the row
// type (§5): `row: Rating | None = Database.queryOne(...)` reads Rating,
// and `rows: List<Rating> = Database.queryMany(...)` does too. It reports
// whether e was such a call.
func (c *checker) checkExpectedQuery(e syntax.Expr, expected types.Type, env map[string]types.Type) bool {
	if expected == nil || isUnknown(expected) {
		return false
	}
	call, op, ok := c.databaseCall(e, env)
	if !ok || (op != "queryOne" && op != "queryMany") {
		return false
	}
	start, _ := call.Bounds()
	c.checkPrimitiveCaller(types.DatabaseName, op, start)
	var row types.Type
	switch op {
	case "queryOne":
		var rest []types.Type
		hasNone := false
		if u, isUnion := expected.(types.Union); isUnion {
			for _, m := range u.Members {
				if _, isNone := m.(types.NoneType); isNone {
					hasNone = true
				} else {
					rest = append(rest, m)
				}
			}
		}
		switch {
		case !hasNone:
			c.report(start, "A1", "Database.queryOne returns a row or None when no row matched; read it as `%s | None`", expected)
		case len(rest) == 1:
			row = rest[0]
		default:
			c.report(start, "Q3", "Database.queryOne reads one row type, not %s; name one type beside None", types.Union{Members: rest})
		}
	case "queryMany":
		if g, isList := expected.(types.Generic); isList && g.Name == "List" && len(g.Args) == 1 {
			row = g.Args[0]
		} else {
			c.report(start, "A1", "Database.queryMany returns a List of rows, not a %s", expected)
		}
	}
	if row == nil {
		row = types.Unknown{} // reported above; not A12 as well
	}
	c.callDatabase(call, op, row, start, env)
	return true
}

// callDatabase checks a Database call's arguments and, for a query, the
// row type it reads into, which it records on the call for the runtime.
// row is nil when the context gives none (A12). The caller rule (B10,
// P7) is checked by the caller.
func (c *checker) callDatabase(call *syntax.CallExpr, op string, row types.Type, start lexer.Pos, env map[string]types.Type) types.Type {
	ops := types.DatabaseOps()
	m, ok := ops[op]
	if !ok {
		c.report(start, "K1", "Database has no operation %q; its operations are %s", op, methodList(ops))
		for _, a := range call.Args {
			c.inferExpr(a.Value, env)
		}
		return types.Unknown{}
	}
	c.checkDatabaseArgs(call, op, start, env)
	if m.Result != nil {
		return m.Result
	}
	if row == nil {
		example := "row: Account | None = Database.queryOne(...)"
		if op == "queryMany" {
			example = "rows: List<Account> = Database.queryMany(...)"
		}
		c.report(start, "A12", "Database.%s has no row type to read into; give it one with a typed declaration, e.g. `%s` (ADR 0082 §5)", op, example)
		return types.Unknown{}
	}
	if why := c.rowRefusal(row); why != "" {
		c.report(start, "Q3", "Database.%s can't read a row into %s: %s, and one column holds one value (ADR 0082 §5)", op, row, why)
	} else if !isUnknown(row) {
		call.DecodeInto = row
	}
	if op == "queryOne" {
		return types.Union{Members: []types.Type{row, types.NoneType{}}}
	}
	return types.Generic{Name: "List", Args: []types.Type{row}}
}

// checkDatabaseArgs checks the SQL text (Q1) and the values to bind (Q2).
// They are positional, since SQL placeholders are.
func (c *checker) checkDatabaseArgs(call *syntax.CallExpr, op string, start lexer.Pos, env map[string]types.Type) {
	if len(call.Args) == 0 {
		c.report(start, "", "Database.%s takes the SQL text, then the values to bind", op)
		return
	}
	for i, a := range call.Args {
		at := start
		if sp, ok := a.Value.(syntax.Spanned); ok {
			at, _ = sp.Bounds()
		}
		if a.Name != "" && !(i == 0 && a.Name == "sql") {
			c.report(at, "", "Database.%s's values to bind are positional, as SQL placeholders are; drop the name %s:", op, a.Name)
		}
		if i == 0 {
			c.checkExpected(a.Value, types.Primitive{Name: "string"}, env)
			if !isSQLLiteral(a.Value) {
				c.report(at, "Q1", "Database.%s's SQL text must be a string literal with no ${...}; pass each value as an argument after it, bound to a placeholder (ADR 0082 §3)", op)
			}
			continue
		}
		t := c.inferExpr(a.Value, env)
		if !isUnknown(t) && !bindable(t) {
			c.report(at, "Q2", "argument %d of Database.%s is %s, which SQL can't bind; bind an int, float, string, bool, decimal, bytes, payload-free enum, or None (ADR 0082 §4)", i, op, t)
		}
	}
}

// isSQLLiteral is Q1: a string literal with no interpolated part.
func isSQLLiteral(e syntax.Expr) bool {
	lit, ok := e.(*syntax.StringLit)
	if !ok {
		return false
	}
	for _, part := range lit.Parts {
		if part.Interp != nil {
			return false
		}
	}
	return true
}

// columnScalar is a type one column holds a value of (ADR 0082 §4, §7).
func columnScalar(t types.Type) bool {
	switch t := t.(type) {
	case types.Primitive:
		switch t.Name {
		case "int", "float", "decimal", "string", "bytes", "bool":
			return true
		}
	case types.Named:
		if e, ok := t.Decl.(*syntax.EnumDecl); ok {
			return !e.HasPayload()
		}
	}
	return false
}

// bindable is Q2: a value SQL can bind, a column scalar or None, or a
// union of them.
func bindable(t types.Type) bool {
	switch t := t.(type) {
	case types.NoneType:
		return true
	case types.Union:
		for _, m := range t.Members {
			if !bindable(m) && !isUnknown(m) {
				return false
			}
		}
		return true
	}
	return columnScalar(t)
}

// columnType is a column scalar, or one beside None for a nullable column.
func columnType(t types.Type) bool {
	if u, ok := t.(types.Union); ok {
		scalars := 0
		for _, m := range u.Members {
			if _, isNone := m.(types.NoneType); isNone {
				continue
			}
			if !columnScalar(m) {
				return false
			}
			scalars++
		}
		return scalars == 1
	}
	return columnScalar(t)
}

// rowRefusal says why a query can't read rows into t (Q3), naming the
// field at fault, or "" when it can: a model whose every field is a
// column type, or one column type.
func (c *checker) rowRefusal(t types.Type) string {
	if isUnknown(t) {
		return ""
	}
	if named, ok := t.(types.Named); ok {
		if model, ok := named.Decl.(*syntax.ModelDecl); ok {
			for _, f := range model.AllFields() {
				ft := c.resolveField(model, f)
				if !isUnknown(ft) && !columnType(ft) {
					return "field " + f.Name + " is " + ft.String()
				}
			}
			return ""
		}
	}
	if columnType(t) {
		return ""
	}
	return "it is " + t.String()
}
