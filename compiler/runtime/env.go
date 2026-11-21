package runtime

// Env is a lexical scope of local bindings. Declarations aren't closures —
// a rule/conversion/activity/gateway/workflow body only ever sees its own parameters and
// whatever it binds itself, never a caller's locals — so every call starts
// a brand new root Env (see Interp.Call in interp.go); Child is only used
// for nested scopes within one call (if/match-arm/transaction/for bodies),
// mirroring checker.childEnv's scoping rule but via parent-chained lookup
// instead of a copy. The only name a scope may change is a reassignable
// local (ADR 0053), and Reassign walks up to the scope that owns it.
//
// ADR 0115 §3: a scope's bindings are a short list, searched from the
// newest, not a hash map. Most scopes hold a few names, so a new scope is
// one allocation, with its first envInline bindings inside the Env itself,
// and a lookup compares a few strings rather than hashing one. A scope
// that grows past envIndexed names gets an index.
type Env struct {
	vars   []binding
	inline [envInline]binding
	// index is vars by name, built once vars passes envIndexed.
	index  map[string]int
	parent *Env
}

// binding is one name in a scope. reassignable marks one declared with a
// type (ADR 0053).
type binding struct {
	name         string
	v            Value
	reassignable bool
}

const (
	envInline  = 4
	envIndexed = 16
)

// NewEnv creates a fresh root scope.
func NewEnv() *Env {
	e := &Env{}
	e.vars = e.inline[:0]
	return e
}

// Child creates a nested scope — bindings made in it don't leak back out
// once the block ends, since the parent's own bindings are never written to.
func (e *Env) Child() *Env {
	c := &Env{parent: e}
	c.vars = c.inline[:0]
	return c
}

// reset empties e's own bindings, so a loop's next iteration reuses the
// scope its body ran in rather than allocating another (ADR 0115 §3).
// Nothing outlives the block a scope is for: fiig has no closures, and a
// concurrent expression waits for every body before it returns.
func (e *Env) reset() {
	clear(e.vars)
	e.vars = e.vars[:0]
	e.index = nil
}

// bindFresh is Bind in a scope reset has just emptied, where name can't
// be bound yet, so there is nothing to look for first.
func (e *Env) bindFresh(name string, v Value) {
	e.vars = append(e.vars, binding{name: name, v: v})
}

// find is name's position in e's own bindings, or -1.
func (e *Env) find(name string) int {
	if e.index != nil {
		if i, ok := e.index[name]; ok {
			return i
		}
		return -1
	}
	for i := len(e.vars) - 1; i >= 0; i-- {
		if e.vars[i].name == name {
			return i
		}
	}
	return -1
}

// Get looks up name in e or any enclosing scope.
func (e *Env) Get(name string) (Value, bool) {
	for cur := e; cur != nil; cur = cur.parent {
		if i := cur.find(name); i >= 0 {
			return cur.vars[i].v, true
		}
	}
	return nil, false
}

// Bind introduces or overwrites name in e's own scope (never an enclosing
// one) — what a `Binding` statement does.
func (e *Env) Bind(name string, v Value) {
	e.set(name, v, false)
}

// BindReassignable introduces name in e's own scope as a reassignable local,
// the `name: Type = expr` form (ADR 0053).
func (e *Env) BindReassignable(name string, v Value) {
	e.set(name, v, true)
}

func (e *Env) set(name string, v Value, reassignable bool) {
	if i := e.find(name); i >= 0 {
		e.vars[i] = binding{name, v, reassignable}
		return
	}
	e.vars = append(e.vars, binding{name, v, reassignable})
	if e.index != nil {
		e.index[name] = len(e.vars) - 1
	} else if len(e.vars) > envIndexed {
		e.index = make(map[string]int, 2*len(e.vars))
		for i, b := range e.vars {
			e.index[b.name] = i
		}
	}
}

// envSnapshot is the reassignable locals visible from an Env, by scope.
type envSnapshot []struct {
	scope *Env
	vars  []binding
}

// snapshot records every reassignable local visible from e, so a rerun
// transaction block starts from them (ADR 0088 §4). Values are immutable,
// so recording each binding is enough.
func (e *Env) snapshot() envSnapshot {
	var out envSnapshot
	for cur := e; cur != nil; cur = cur.parent {
		var vars []binding
		for _, b := range cur.vars {
			if b.reassignable {
				vars = append(vars, b)
			}
		}
		if len(vars) > 0 {
			out = append(out, struct {
				scope *Env
				vars  []binding
			}{cur, vars})
		}
	}
	return out
}

// restore puts back what snapshot recorded.
func (e *Env) restore(s envSnapshot) {
	for _, scope := range s {
		for _, b := range scope.vars {
			scope.scope.set(b.name, b.v, true)
		}
	}
}

// Reassign sets name to v in the scope that owns it and reports true. It
// reports false, leaving everything unchanged, when that binding is immutable
// or no scope binds name. The checker rejects both cases before run time, so
// a false here means the caller should bind the name instead.
func (e *Env) Reassign(name string, v Value) bool {
	for cur := e; cur != nil; cur = cur.parent {
		if i := cur.find(name); i >= 0 {
			if !cur.vars[i].reassignable {
				return false
			}
			cur.vars[i].v = v
			return true
		}
	}
	return false
}
