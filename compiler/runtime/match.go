package runtime

import "fiig-lang/compiler/syntax"

// MatchPattern reports whether p matches v, binding any names p introduces
// into env as a side effect — mirrors compiler/checker/expr.go's
// bindPattern/bindConstructorPattern, but as a real runtime test instead of
// a static type-compatibility check. Exported for compiler/adapter, which
// matches a workflow's actual Result against a route's response-mapping
// arms (ADR 0017) the same way a `match` statement does.
func MatchPattern(p syntax.Pattern, v Value, env *Env) bool {
	return matchPattern(p, v, env, true)
}

// matchPattern is MatchPattern; top is false for a pattern nested inside
// another (`reply` in `Ok(reply)`), which binds the value it is given even
// when that value is itself a Result.
func matchPattern(p syntax.Pattern, v Value, env *Env, top bool) bool {
	switch p := p.(type) {
	case *syntax.WildcardPattern:
		return true
	case *syntax.NamePattern:
		if len(p.Path) == 2 {
			// A zero-field enum variant (`Region.US`) parses with no call
			// parens at all, so it's a NamePattern, not a ConstructorPattern —
			// the same distinction compiler/checker/expr.go's inferFieldAccess
			// draws for constructing one. Matching it is otherwise identical
			// to matchConstructorPattern's 2-segment EnumValue case, just
			// without an Args loop since there are no fields to bind.
			ev, ok := v.(EnumValue)
			return ok && ev.TypeName == p.Path[0] && ev.Variant == p.Path[1]
		}
		if len(p.Path) == 3 {
			// Another package's variant, named with its alias
			// (http.Method.Get).
			ev, ok := v.(EnumValue)
			return ok && ev.TypeName == p.Path[1] && ev.Variant == p.Path[2]
		}
		if len(p.Path) != 1 {
			return false
		}
		if p.Path[0] == "None" {
			_, isNone := v.(None)
			return isNone
		}
		// A bare name is a catch-all binding, but never for None — compiler/
		// checker/stmt.go's patternCoverage draws exactly this line for a
		// `T | None` union (a bare name covers only "the rest", never the
		// None tag; None needs its own arm), and it holds regardless of
		// arm order here: get_customer.fiig writes `found -> ...` *before*
		// `None -> ...`, so without this check a None subject would
		// wrongly match the catch-all first.
		if _, isNone := v.(None); isNone {
			return false
		}
		if ev, ok := v.(EnumValue); ok {
			for _, d := range p.Excludes {
				if ev.Decl == d {
					return false
				}
			}
		}
		// Against a Result specifically, a catch-all is always paired with
		// an explicit Ok(...)/Err(...)/tag arm earlier in the same match
		// (checkout.fiig's `Err(error) -> ...; found -> ...`) — by the time
		// a catch-all name arm is even reached, the Result's payload
		// (whichever case remains) is what the corpus actually means by
		// that name, not the Result wrapper itself.
		// Only at the top: inside Ok(...) the name is the payload already,
		// and a payload that is a Result stays one (Ok(Ok(5)) binds Ok(5)).
		if rv, ok := v.(Result); ok && top {
			env.Bind(p.Path[0], rv.Value)
			return true
		}
		env.Bind(p.Path[0], v)
		return true
	case *syntax.ConstructorPattern:
		return matchConstructorPattern(p, v, env)
	case *syntax.BytesPattern:
		return matchBytesPattern(p, v, env)
	default:
		return false
	}
}

func matchConstructorPattern(p *syntax.ConstructorPattern, v Value, env *Env) bool {
	if len(p.Path) == 1 && (p.Path[0] == "Ok" || p.Path[0] == "Err") {
		rv, ok := v.(Result)
		if !ok || rv.Ok != (p.Path[0] == "Ok") {
			return false
		}
		if len(p.Args) == 1 {
			return matchPattern(p.Args[0], rv.Value, env, false)
		}
		return true
	}
	if n := len(p.Path); n == 2 || n == 3 {
		ev, ok := v.(EnumValue)
		if !ok || ev.TypeName != p.Path[n-2] || ev.Variant != p.Path[n-1] {
			return false
		}
		for i, sub := range p.Args {
			if i >= len(ev.Fields) {
				break
			}
			if !matchPattern(sub, ev.Fields[i], env, false) {
				return false
			}
		}
		return true
	}
	return false
}
