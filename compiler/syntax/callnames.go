package syntax

import "sort"

// PreludeCallForms are call-shaped names that never name a real
// declaration, matching checker.inferCall's own special-cased set (Ok,
// Err) plus the ownership copy(...) builtin
// (docs/specification/ownership.md) and ADR 0057's append — none of these are ever worth
// recording as a dependency candidate, since nothing in the universe is
// ever going to resolve to one regardless of what else exists.
var PreludeCallForms = map[string]bool{"Ok": true, "Err": true, "copy": true, "append": true}

// BodyCallNames collects the deduplicated, sorted set of declaration names
// a rule/conversion/activity/gateway/workflow's body calls — Milestone 3's "dependency
// ... graph" deliverable. Walks the same statement/expression shapes
// checker.checkBlock/checkStmt/inferExpr already walk for type-checking,
// but only to collect names, not to check anything.
func BodyCallNames(body *Block) []string {
	names := map[string]bool{}
	collectBlockCallNames(body, names)
	return sortedNames(names)
}

// FileCallNames is BodyCallNames for a whole file (ADR 0054): the calls in
// its public entry's body and in every private helper, less the calls to
// the file's own private members, which never leave the file. This is the
// dependency set of the file's one public entry.
func FileCallNames(f *File) []string {
	if len(f.Decls) == 0 {
		return nil
	}
	names := map[string]bool{}
	switch d := f.Decls[0].(type) {
	case *RuleDecl:
		collectBlockCallNames(d.Body, names)
	case *ConversionDecl:
		collectBlockCallNames(d.Body, names)
	case *ActivityDecl:
		collectBlockCallNames(d.Body, names)
	case *GatewayDecl:
		collectBlockCallNames(d.Body, names)
	case *WorkflowDecl:
		collectBlockCallNames(d.Body, names)
	}
	if f.Private != nil {
		for _, m := range f.Private.Members {
			if h, ok := m.(*HelperDecl); ok {
				collectBlockCallNames(h.Body, names)
			}
		}
		for _, m := range f.Private.Members {
			delete(names, DeclName(m))
		}
	}
	return sortedNames(names)
}

func collectBlockCallNames(b *Block, out map[string]bool) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		collectStmtCallNames(s, out)
	}
}

func collectStmtCallNames(s Stmt, out map[string]bool) {
	switch s := s.(type) {
	case *Binding:
		CollectExprCallNames(s.Value, out)
	case *ForStmt:
		CollectExprCallNames(s.Iterable, out)
		collectBlockCallNames(s.Body, out)
	case *WhileStmt:
		CollectExprCallNames(s.Cond, out)
		collectBlockCallNames(s.Body, out)
	case *ReturnStmt:
		if s.Value != nil {
			CollectExprCallNames(s.Value, out)
		}
	case *ExprStmt:
		CollectExprCallNames(s.Value, out)
	case *ExpectStmt:
		CollectExprCallNames(s.Cond, out)
	case *IfStmt:
		CollectExprCallNames(s.Cond, out)
		collectBlockCallNames(s.Then, out)
		for _, ei := range s.ElseIfs {
			CollectExprCallNames(ei.Cond, out)
			collectBlockCallNames(ei.Then, out)
		}
		if s.Else != nil {
			collectBlockCallNames(s.Else, out)
		}
	case *MatchStmt:
		CollectExprCallNames(s.Subject, out)
		for _, arm := range s.Arms {
			collectBodyCallNames(arm.Body, out)
		}
	case *TransactionStmt:
		collectBlockCallNames(s.Body, out)
	}
}

func collectBodyCallNames(b Body, out map[string]bool) {
	switch b := b.(type) {
	case ExprBody:
		CollectExprCallNames(b.Expr, out)
	case *Block:
		collectBlockCallNames(b, out)
	}
}

func CollectExprCallNames(e Expr, out map[string]bool) {
	if e == nil {
		return
	}
	switch e := e.(type) {
	case *CallExpr:
		if ident, ok := e.Callee.(*Ident); ok {
			if !PreludeCallForms[ident.Name] {
				out[ident.Name] = true
			}
		} else {
			CollectExprCallNames(e.Callee, out)
		}
		for _, a := range e.Args {
			CollectExprCallNames(a.Value, out)
		}
	case *FieldAccess:
		CollectExprCallNames(e.Target, out)
	case *NotExpr:
		CollectExprCallNames(e.Operand, out)
	case *NegExpr:
		CollectExprCallNames(e.Operand, out)
	case *BinaryExpr:
		CollectExprCallNames(e.Left, out)
		CollectExprCallNames(e.Right, out)
	case *TryExpr:
		CollectExprCallNames(e.Operand, out)
	case *ParenExpr:
		CollectExprCallNames(e.Inner, out)
	case *ListLit:
		for _, el := range e.Elems {
			CollectExprCallNames(el, out)
		}
	case *BytesLit:
		for _, seg := range e.Segments {
			CollectExprCallNames(seg.Value, out)
		}
	case *ConcurrentExpr:
		CollectExprCallNames(e.Iterable, out)
		collectBodyCallNames(e.Body, out)
	case *SortedExpr:
		CollectExprCallNames(e.Iterable, out)
		CollectExprCallNames(e.Key, out)
	case *MapLit:
		for _, en := range e.Entries {
			CollectExprCallNames(en.Key, out)
			CollectExprCallNames(en.Value, out)
		}
	case *RetryExpr:
		collectBodyCallNames(e.Body, out)
	case *StringLit:
		for _, part := range e.Parts {
			if part.Interp != nil {
				CollectExprCallNames(part.Interp, out)
			}
		}
	}
}

// BodyCapabilityCalls collects the deduplicated, sorted set of capability
// operations a body calls directly, as "Name.op": every call of the shape
// `Upper.lower(...)`. ADR 0043 §6 makes that shape mean exactly "may reach
// the outside world" — a capability group, a primitive, or a capitalized
// provider binding — while an enum variant is `Upper.Upper` and anything
// pure has a lowercase prefix, so no name resolution is needed to tell.
func BodyCapabilityCalls(body *Block) []string {
	names := map[string]bool{}
	walkBlockExprs(body, func(e Expr) {
		call, ok := e.(*CallExpr)
		if !ok {
			return
		}
		fa, ok := call.Callee.(*FieldAccess)
		if !ok {
			return
		}
		target, ok := fa.Target.(*Ident)
		if ok && startsUpper(target.Name) && !startsUpper(fa.Field) {
			names[target.Name+"."+fa.Field] = true
		}
	})
	return sortedNames(names)
}

func startsUpper(s string) bool {
	return s != "" && s[0] >= 'A' && s[0] <= 'Z'
}

// WalkBlockExprs calls visit on every expression in b, outermost first.
func WalkBlockExprs(b *Block, visit func(Expr)) { walkBlockExprs(b, visit) }

// WalkExpr calls visit on e and every expression inside it, outermost
// first.
func WalkExpr(e Expr, visit func(Expr)) { walkExpr(e, visit) }

// walkBlockExprs calls visit on every expression in b, outermost first.
func walkBlockExprs(b *Block, visit func(Expr)) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		walkStmtExprs(s, visit)
	}
}

func walkStmtExprs(s Stmt, visit func(Expr)) {
	switch s := s.(type) {
	case *Binding:
		walkExpr(s.Value, visit)
	case *ForStmt:
		walkExpr(s.Iterable, visit)
		walkBlockExprs(s.Body, visit)
	case *WhileStmt:
		walkExpr(s.Cond, visit)
		walkBlockExprs(s.Body, visit)
	case *ReturnStmt:
		walkExpr(s.Value, visit)
	case *ExprStmt:
		walkExpr(s.Value, visit)
	case *ExpectStmt:
		walkExpr(s.Cond, visit)
	case *IfStmt:
		walkExpr(s.Cond, visit)
		walkBlockExprs(s.Then, visit)
		for _, ei := range s.ElseIfs {
			walkExpr(ei.Cond, visit)
			walkBlockExprs(ei.Then, visit)
		}
		walkBlockExprs(s.Else, visit)
	case *MatchStmt:
		walkExpr(s.Subject, visit)
		for _, arm := range s.Arms {
			walkBodyExprs(arm.Body, visit)
		}
	case *TransactionStmt:
		walkBlockExprs(s.Body, visit)
	}
}

func walkBodyExprs(b Body, visit func(Expr)) {
	switch b := b.(type) {
	case ExprBody:
		walkExpr(b.Expr, visit)
	case *Block:
		walkBlockExprs(b, visit)
	}
}

func walkExpr(e Expr, visit func(Expr)) {
	if e == nil {
		return
	}
	visit(e)
	switch e := e.(type) {
	case *CallExpr:
		walkExpr(e.Callee, visit)
		for _, a := range e.Args {
			walkExpr(a.Value, visit)
		}
	case *FieldAccess:
		walkExpr(e.Target, visit)
	case *NotExpr:
		walkExpr(e.Operand, visit)
	case *NegExpr:
		walkExpr(e.Operand, visit)
	case *BinaryExpr:
		walkExpr(e.Left, visit)
		walkExpr(e.Right, visit)
	case *TryExpr:
		walkExpr(e.Operand, visit)
	case *ParenExpr:
		walkExpr(e.Inner, visit)
	case *ListLit:
		for _, el := range e.Elems {
			walkExpr(el, visit)
		}
	case *BytesLit:
		for _, seg := range e.Segments {
			walkExpr(seg.Value, visit)
		}
	case *ConcurrentExpr:
		walkExpr(e.Iterable, visit)
		walkBodyExprs(e.Body, visit)
	case *SortedExpr:
		walkExpr(e.Iterable, visit)
		walkExpr(e.Key, visit)
	case *MapLit:
		for _, en := range e.Entries {
			walkExpr(en.Key, visit)
			walkExpr(en.Value, visit)
		}
	case *RetryExpr:
		walkBodyExprs(e.Body, visit)
	case *StringLit:
		for _, part := range e.Parts {
			walkExpr(part.Interp, visit)
		}
	}
}

func sortedNames(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
