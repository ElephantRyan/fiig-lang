package runtime

import (
	"context"
	"strings"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// evalCall dispatches a CallExpr. The callee shape decides what kind of
// call this is, mirroring compiler/checker/expr.go's inferCall: a
// FieldAccess callee is either an enum-variant constructor, a capability
// call, or a builtin method call (evalQualifiedCall); a bare Ident callee
// is Ok/Err/copy, a model constructor, or an ordinary declared call.
func (in *Interp) evalCall(ctx context.Context, e *syntax.CallExpr, env *Env) (Value, bool, *Fault) {
	if fa, ok := e.Callee.(*syntax.FieldAccess); ok {
		if target, ok := fa.Target.(*syntax.Ident); ok && target.Name == types.JsonGroup {
			if _, isLocal := env.Get(target.Name); !isLocal {
				return in.callJson(ctx, e, fa.Field, env)
			}
		}
		if e.HandlesConflicts {
			return in.evalHandlingConflicts(ctx, e, fa, env)
		}
		v, earlyReturn, f := in.evalQualifiedCall(ctx, fa, e.Args, env)
		// ADR 0082 §5: a Database query the checker typed reads its rows
		// into the row type it recorded.
		if into, typed := e.DecodeInto.(types.Type); typed && f == nil && !earlyReturn {
			return in.readQueryResult(fa.Field, v, into)
		}
		return v, earlyReturn, f
	}
	ident, ok := e.Callee.(*syntax.Ident)
	if !ok {
		return nil, false, Faultf("unsupported call target %T", e.Callee)
	}
	return in.evalIdentCall(ctx, e, ident, env)
}

func (in *Interp) evalIdentCall(ctx context.Context, e *syntax.CallExpr, ident *syntax.Ident, env *Env) (Value, bool, *Fault) {
	switch ident.Name {
	case types.HttpResponseName:
		return in.evalHttpResponse(ctx, e, env)
	case "Ok", "Err":
		if len(e.Args) != 1 {
			return nil, false, Faultf("%s(...) takes exactly one argument", ident.Name)
		}
		v, earlyReturn, fault := in.evalExpr(ctx, e.Args[0].Value, env)
		if fault != nil || earlyReturn {
			return v, earlyReturn, fault
		}
		return Result{Ok: ident.Name == "Ok", Value: v}, false, nil
	case "copy":
		return in.evalCopy(ctx, e, env)
	case "append":
		return in.evalAppend(ctx, e, env)
	case "calls":
		if testRunOf(ctx) != nil {
			return in.evalCalls(ctx, e)
		}
	}

	sc := in.scope(ctx)
	if sc.group != nil {
		if f := sc.group.Member(ident.Name); f != nil {
			return in.evalDeclCall(ctx, sc, sc.group.Name+"."+ident.Name, f, e.Args, env)
		}
	}
	decl, found := sc.universe.Lookup(ident.Name)
	if !found {
		return nil, false, Faultf("undefined name %q", ident.Name)
	}
	if constructible(decl) {
		return in.constructDecl(ctx, decl, ident.Name, e.Args, env)
	}
	if enumDecl, ok := decl.(*syntax.EnumDecl); ok {
		return in.lookupByKeyIn(ctx, ctx, enumDecl, e.Args, env)
	}
	// An unqualified call never crosses a package boundary (fiig has no
	// implicit cross-package reference), so decl's body runs in the
	// caller's package. It does run with decl's own file's imports, though,
	// not the caller's: in the root package two files may import different
	// things (localScope).
	return in.evalDeclCall(ctx, in.localScope(sc, ident.Name), ident.Name, decl, e.Args, env)
}

// evalHttpResponse builds the prelude's HttpResponse<B> (ADR 0069 §3): a
// record whose left-out fields are None.
func (in *Interp) evalHttpResponse(ctx context.Context, e *syntax.CallExpr, env *Env) (Value, bool, *Fault) {
	supplied, earlyReturn, fault := in.evalArgs(ctx, env, e.Args, types.HttpResponseFieldNames)
	if fault != nil || earlyReturn {
		return nil, earlyReturn, fault
	}
	fields := make(map[string]Value, len(types.HttpResponseFieldNames))
	for _, n := range types.HttpResponseFieldNames {
		v, ok := supplied[n]
		if !ok {
			if n == "status" {
				return nil, false, Faultf("HttpResponse requires field \"status\"")
			}
			v = None{}
		}
		fields[n] = v
	}
	return Record{TypeName: types.HttpResponseName, Fields: fields}, false, nil
}

// constructible reports whether decl is a model, constructed
// by calling its own name — the one shape decl.(syntax.Decl) can be
// that callableShape doesn't cover.
func constructible(decl syntax.Decl) bool {
	switch decl.(type) {
	case *syntax.ModelDecl:
		return true
	default:
		return false
	}
}

// constructDecl builds a Record from a model construction call —
// shared by an unqualified construction (evalIdentCall) and a qualified
// one (callDeclInPackage, e.g. `time.Instant(...)` inside
// company.time's own addBusinessDays, constructing standard.time's
// model under its own alias for it).
func (in *Interp) constructDecl(ctx context.Context, decl syntax.Decl, displayName string, args []syntax.Arg, env *Env) (Value, bool, *Fault) {
	switch decl := decl.(type) {
	case *syntax.ModelDecl:
		names := fieldNames(decl.Fields)
		supplied, earlyReturn, fault := in.evalArgs(ctx, env, args, names)
		if fault != nil || earlyReturn {
			return nil, earlyReturn, fault
		}
		fields := make(map[string]Value, len(decl.Fields))
		// A record is flat (ADR 0045): an embedded model's fields are merged
		// in first, so an explicit field of the same name — which shadows
		// the promoted one — is written over it.
		for _, f := range decl.Fields {
			if !f.Embedded {
				continue
			}
			v, ok := supplied[f.Name]
			if !ok {
				return nil, false, Faultf("%s requires field %q", decl.Name, f.Name)
			}
			inner, isRec := v.(Record)
			if !isRec {
				return nil, false, Faultf("%s: embedded %q must be given a %s value", decl.Name, f.Name, f.Name)
			}
			for k, fv := range inner.Fields {
				fields[k] = fv
			}
		}
		for _, f := range decl.Fields {
			if f.Embedded {
				continue
			}
			v, ok := supplied[f.Name]
			if !ok {
				// The checker lets an optional field be left out (A3 is
				// only for required ones); it is None.
				if !syntax.AdmitsNone(f.Type) {
					return nil, false, Faultf("%s requires field %q", decl.Name, f.Name)
				}
				v = None{}
			}
			fields[f.Name] = v
		}
		return Record{TypeName: decl.Name, Fields: fields, Decl: decl}, false, nil
	default:
		return nil, false, Faultf("%q is not constructible", displayName)
	}
}

// callDeclInPackage looks up field (a bare name) in pkg's own Universe
// and calls it with a fresh scope built from pkg's own Universe/Imports
// — the runtime counterpart of compiler/checker's callInScope. pkg.
// Imports is already flattened across every file in that package
// (compiler/checker.BuildPackage / compiler/loader), the same
// simplification the checker itself already accepts once a call
// crosses into an imported package (only the direct entry point —
// Call, below — gets full per-file alias precision); nothing here is
// less precise than the checker already is for the identical program.
func (in *Interp) callDeclInPackage(ctx context.Context, pkg *types.Package, alias, field string, args []syntax.Arg, env *Env) (Value, bool, *Fault) {
	decl, found := pkg.Universe.Lookup(field)
	if !found {
		return nil, false, Faultf("package %q has no declaration %q", alias, field)
	}
	if constructible(decl) {
		return in.constructDecl(ctx, decl, alias+"."+field, args, env)
	}
	if enumDecl, ok := decl.(*syntax.EnumDecl); ok {
		return in.lookupByKeyIn(ctx, withScope(ctx, packageScope(pkg)), enumDecl, args, env)
	}
	params, _, _, ok := callableShape(decl)
	if !ok {
		return nil, false, Faultf("%q is not callable", field)
	}
	supplied, earlyReturn, fault := in.evalArgs(ctx, env, args, paramNames(params))
	if fault != nil || earlyReturn {
		return nil, earlyReturn, fault
	}
	if _, isWorkflow := decl.(*syntax.WorkflowDecl); isWorkflow {
		// A gateway calling another package's workflow (ADR 0079 §1): the
		// workflow runs for its own package, whose bindings its gateways
		// use (§6a).
		ctx = WithDomain(ctx, pkg.Path)
	}
	v, f := in.callDecl(ctx, packageScope(pkg), alias+"."+field, decl, supplied)
	if f != nil {
		return nil, false, f
	}
	return v, false, nil
}

func (in *Interp) evalDeclCall(ctx context.Context, sc scope, name string, decl syntax.Decl, args []syntax.Arg, env *Env) (Value, bool, *Fault) {
	params, _, _, ok := callableShape(decl)
	if !ok {
		return nil, false, Faultf("%q is not callable", name)
	}
	supplied, earlyReturn, fault := in.evalArgs(ctx, env, args, paramNames(params))
	if fault != nil || earlyReturn {
		return nil, earlyReturn, fault
	}
	v, f := in.callDecl(ctx, sc, name, decl, supplied)
	if f != nil {
		return nil, false, f
	}
	return v, false, nil
}

// evalQualifiedCall handles `Target.Field(args)`.
func (in *Interp) evalQualifiedCall(ctx context.Context, fa *syntax.FieldAccess, args []syntax.Arg, env *Env) (Value, bool, *Fault) {
	if enumDecl, ok := in.importedEnum(ctx, fa.Target, env); ok {
		return in.constructVariant(ctx, enumDecl, fa.Field, args, env)
	}
	targetIdent, ok := fa.Target.(*syntax.Ident)
	if ok {
		// A local binding (`email.contains("@")`) is a builtin method call
		// on that value, handled below — never a capability call — unless it
		// holds a provider (ADR 0043 §4).
		if v, isLocal := env.Get(targetIdent.Name); isLocal {
			if p, isProvider := v.(Provider); isProvider {
				return in.callProvider(ctx, p, fa.Field, args, env)
			}
			ok = false
		}
	}
	if ok {
		if targetIdent.Name == types.MathGroup {
			return in.callMath(ctx, fa, args, env)
		}
		if targetIdent.Name == types.CryptoGroup {
			return in.callCrypto(ctx, fa, args, env)
		}
		if targetIdent.Name == types.CompressGroup {
			return in.callCompress(ctx, fa, args, env)
		}
		sc := in.scope(ctx)
		if g, gsc, isFormulas := in.formulasGroup(sc, targetIdent.Name); isFormulas {
			return in.callFormula(ctx, g, gsc, targetIdent.Name, fa.Field, args, env)
		}
		if p, isGroup := in.groupValue(sc, targetIdent.Name); isGroup {
			return in.callProvider(ctx, p, fa.Field, args, env)
		}
		if decl, found := sc.universe.Lookup(targetIdent.Name); found {
			enumDecl, ok := decl.(*syntax.EnumDecl)
			if !ok {
				return nil, false, Faultf("%q is not an enum type", targetIdent.Name)
			}
			for _, v := range enumDecl.Variants {
				if v.Name == fa.Field {
					return in.constructEnum(ctx, enumDecl, v, args, env)
				}
			}
			return nil, false, Faultf("%s has no variant %q", targetIdent.Name, fa.Field)
		}
		// targetIdent.Name is a package alias (Milestone 6, ADR 0032/
		// 0038) — a call into that package's own declaration, executed
		// under its own Universe/Imports (callDeclInPackage), not the
		// caller's.
		if pkg, found := sc.imports[targetIdent.Name]; found {
			return in.callDeclInPackage(ctx, pkg, targetIdent.Name, fa.Field, args, env)
		}
		// Neither a declared name nor a known import alias — a
		// capability call (Database.queryOne(...)), the only other shape
		// this grammar position is attested to mean.
		argVals, earlyReturn, fault := in.evalArgsPositional(ctx, args, env)
		if fault != nil || earlyReturn {
			return nil, earlyReturn, fault
		}
		// ADR 0015: implicit transaction binding — if this call is running
		// inside a `transaction <targetIdent.Name> { ... }` block, it joins
		// that transaction instead of going through the plain provider, so
		// it sees the transaction's own uncommitted writes.
		if ops, typed := types.PrimitiveOps(targetIdent.Name); typed {
			if m, found := ops[fa.Field]; found {
				if argVals, fault = placeArgs(targetIdent.Name+"."+fa.Field, m.ParamNames(), m.Required(), args, argVals); fault != nil {
					return nil, false, fault
				}
			}
		}
		var v Value
		var f *Fault
		if tx, ok := lookupActiveTx(ctx, targetIdent.Name); ok {
			v, f = tx.Call(ctx, fa.Field, argVals)
		} else {
			v, f = in.Capabilities.Call(ctx, targetIdent.Name, fa.Field, argVals)
		}
		if f != nil {
			return nil, false, f
		}
		return v, false, nil
	}

	// Target isn't a bare identifier (e.g. `input.name.isBlank()`) — a
	// builtin method call on the evaluated target value.
	target, earlyReturn, fault := in.evalExpr(ctx, fa.Target, env)
	if fault != nil || earlyReturn {
		return target, earlyReturn, fault
	}
	if p, isProvider := target.(Provider); isProvider {
		return in.callProvider(ctx, p, fa.Field, args, env)
	}
	argVals, earlyReturn, fault := in.evalArgsPositional(ctx, args, env)
	if fault != nil || earlyReturn {
		return nil, earlyReturn, fault
	}
	if sigs, ok := types.MethodsOfKind(methodKind(target)); ok {
		if m, found := sigs[fa.Field]; found {
			if argVals, fault = placeArgs(methodKind(target)+"."+fa.Field, m.ParamNames(), m.Required(), args, argVals); fault != nil {
				return nil, false, fault
			}
		}
	}
	v, f := evalBuiltinMethod(target, fa.Field, argVals)
	if f != nil {
		return nil, false, f
	}
	return v, false, nil
}

func (in *Interp) constructEnum(ctx context.Context, enumDecl *syntax.EnumDecl, variant syntax.EnumVariant, args []syntax.Arg, env *Env) (Value, bool, *Fault) {
	enumName := enumDecl.Name
	if enumDecl.Header != nil {
		consts, fault := in.headerConstants(ctx, enumDecl, variant)
		if fault != nil {
			return nil, false, fault
		}
		return EnumValue{TypeName: enumName, Variant: variant.Name, Consts: consts, Decl: enumDecl}, false, nil
	}
	supplied, earlyReturn, fault := in.evalArgs(ctx, env, args, fieldNames(variant.Fields))
	if fault != nil || earlyReturn {
		return nil, earlyReturn, fault
	}
	fields := make([]Value, len(variant.Fields))
	for i, f := range variant.Fields {
		v, ok := supplied[f.Name]
		if !ok {
			return nil, false, Faultf("%s.%s requires field %q", enumName, variant.Name, f.Name)
		}
		fields[i] = v
	}
	return EnumValue{TypeName: enumName, Variant: variant.Name, Fields: fields, Decl: enumDecl}, false, nil
}

// evalCopy implements `copy(base, field: value, ...)` (docs/specification/
// ownership.md, ADR 0023): a new Record sharing base's fields except for
// the explicitly named overrides.
func (in *Interp) evalCopy(ctx context.Context, e *syntax.CallExpr, env *Env) (Value, bool, *Fault) {
	if len(e.Args) == 0 {
		return nil, false, Faultf("copy(...) requires a base value as its first argument")
	}
	base, earlyReturn, fault := in.evalExpr(ctx, e.Args[0].Value, env)
	if fault != nil || earlyReturn {
		return base, earlyReturn, fault
	}
	rec, ok := base.(Record)
	if !ok {
		return nil, false, Faultf("copy(...) base is not a record (got %s)", base)
	}
	fields := make(map[string]Value, len(rec.Fields))
	for k, v := range rec.Fields {
		fields[k] = v
	}
	for _, a := range e.Args[1:] {
		if a.Name == "" {
			return nil, false, Faultf("copy(...) overrides must be named")
		}
		if _, exists := fields[a.Name]; !exists {
			return nil, false, Faultf("%s has no field %q", rec.TypeName, a.Name)
		}
		v, earlyReturn, fault := in.evalExpr(ctx, a.Value, env)
		if fault != nil || earlyReturn {
			return v, earlyReturn, fault
		}
		fields[a.Name] = v
	}
	return Record{TypeName: rec.TypeName, Fields: fields, Decl: rec.Decl}, false, nil
}

// evalArgs matches args against names by name or position (mirroring
// compiler/checker/expr.go's matchArgs), evaluating each argument
// expression left to right as written.
func (in *Interp) evalArgs(ctx context.Context, env *Env, args []syntax.Arg, names []string) (map[string]Value, bool, *Fault) {
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	supplied := map[string]Value{}
	posIdx := 0
	for _, a := range args {
		v, earlyReturn, fault := in.evalExpr(ctx, a.Value, env)
		if fault != nil || earlyReturn {
			return nil, earlyReturn, fault
		}
		if a.Name != "" {
			if !nameSet[a.Name] {
				return nil, false, Faultf("no parameter named %q", a.Name)
			}
			supplied[a.Name] = v
			continue
		}
		for posIdx < len(names) {
			if _, taken := supplied[names[posIdx]]; !taken {
				break
			}
			posIdx++
		}
		if posIdx >= len(names) {
			return nil, false, Faultf("too many arguments")
		}
		supplied[names[posIdx]] = v
		posIdx++
	}
	return supplied, false, nil
}

// evalArgsPositional evaluates args in order with no name matching — used
// for capability/builtin-method calls, neither of which has a declared
// parameter list to match against.
func (in *Interp) evalArgsPositional(ctx context.Context, args []syntax.Arg, env *Env) ([]Value, bool, *Fault) {
	vals := make([]Value, len(args))
	for i, a := range args {
		v, earlyReturn, fault := in.evalExpr(ctx, a.Value, env)
		if fault != nil || earlyReturn {
			return nil, earlyReturn, fault
		}
		vals[i] = v
	}
	return vals, false, nil
}

// placeArgs orders a builtin call's argument values, which are in source
// order, by the builtin's parameters when any argument is named (ADR 0068
// §5), and passes None for each optional parameter left out (ADR 0074 §5),
// so a builtin always receives one value per parameter. The checker
// reports arguments that don't bind before a run.
func placeArgs(subject string, params []string, required int, args []syntax.Arg, vals []Value) ([]Value, *Fault) {
	argNames := make([]string, len(args))
	named := false
	for i, a := range args {
		argNames[i] = a.Name
		named = named || a.Name != ""
	}
	if !named {
		for len(vals) < len(params) && len(vals) >= required {
			vals = append(vals, None{})
		}
		return vals, nil
	}
	at, ok := types.BindArgsOptional(params, required, argNames)
	if !ok {
		return nil, Faultf("%s's arguments don't bind to its parameters (%s)", subject, strings.Join(params, ", "))
	}
	placed := make([]Value, len(at))
	for i, ai := range at {
		if ai < 0 {
			placed[i] = None{}
			continue
		}
		placed[i] = vals[ai]
	}
	return placed, nil
}

func fieldNames(fields []syntax.Field) []string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	return names
}

func paramNames(params []syntax.Param) []string {
	names := make([]string, len(params))
	for i, p := range params {
		names[i] = p.Name
	}
	return names
}
