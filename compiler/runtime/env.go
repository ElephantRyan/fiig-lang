package runtime

// Env is a lexical scope of local bindings. Declarations aren't closures —
// a rule/conversion/activity/gateway/workflow body only ever sees its own parameters and
// whatever it binds itself, never a caller's locals — so every call starts
// a brand new root Env (see Interp.Call in interp.go); Child is only used
// for nested scopes within one call (if/match-arm/transaction/for bodies),
// mirroring checker.childEnv's scoping rule but via parent-chained lookup
// instead of a copy. The only name a scope may change is a reassignable
// local (ADR 0053), and Reassign walks up to the scope that owns it.
type Env struct {
	vars   map[string]Value
	parent *Env
	// reassignable marks the names in vars declared with a type (ADR 0053).
	// Allocated on first use; a nil map reads as "none reassignable".
	reassignable map[string]bool
}

// NewEnv creates a fresh root scope.
func NewEnv() *Env {
	return &Env{vars: map[string]Value{}}
}

// Child creates a nested scope — bindings made in it don't leak back out
// once the block ends, since the parent's own map is never written to.
func (e *Env) Child() *Env {
	return &Env{vars: map[string]Value{}, parent: e}
}

// Get looks up name in e or any enclosing scope.
func (e *Env) Get(name string) (Value, bool) {
	for cur := e; cur != nil; cur = cur.parent {
		if v, ok := cur.vars[name]; ok {
			return v, true
		}
	}
	return nil, false
}

// Bind introduces or overwrites name in e's own scope (never an enclosing
// one) — what a `Binding` statement does.
func (e *Env) Bind(name string, v Value) {
	e.vars[name] = v
	delete(e.reassignable, name)
}

// BindReassignable introduces name in e's own scope as a reassignable local,
// the `name: Type = expr` form (ADR 0053).
func (e *Env) BindReassignable(name string, v Value) {
	e.Bind(name, v)
	if e.reassignable == nil {
		e.reassignable = map[string]bool{}
	}
	e.reassignable[name] = true
}

// Reassign sets name to v in the scope that owns it and reports true. It
// reports false, leaving everything unchanged, when that binding is immutable
// or no scope binds name. The checker rejects both cases before run time, so
// a false here means the caller should bind the name instead.
func (e *Env) Reassign(name string, v Value) bool {
	for cur := e; cur != nil; cur = cur.parent {
		if _, ok := cur.vars[name]; ok {
			if !cur.reassignable[name] {
				return false
			}
			cur.vars[name] = v
			return true
		}
	}
	return false
}
