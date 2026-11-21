package runtime

import (
	"context"
	"hash/fnv"
	"sync"
	"time"

	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
)

// ADR 0091: running a test. A test runs on an Interp of its own, whose
// Capabilities registry is the test's alone (§5), so nothing a test does
// reaches another.

// TestClockStart is where a test's clock starts unless it fakes Clock:
// 2026-01-01T00:00:00Z (§5).
var TestClockStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// TestFailure is one way a test failed: an `expect` that was false, with
// the values on each side of a comparison or the value an `is` didn't
// match, or the fault that stopped it.
type TestFailure struct {
	At lexer.Pos
	// Expect marks a false `expect`; Left and Right are its comparison's
	// operands, "" when it wasn't one, and Value is what an `is` was given.
	Expect      bool
	Left, Right string
	Value       string
	// Message is the fault, or the Err a `?` stopped the test with.
	Message string
}

type testKey struct{}

// testRun is one test's state while it runs.
type testRun struct {
	mu       sync.Mutex
	scope    scope
	fakes    map[*syntax.CapabilityDecl]*syntax.FakeDecl
	byName   map[string]*syntax.CapabilityDecl // a fake's target as written
	counts   map[*syntax.CapabilityDecl]map[string]int
	config   map[*syntax.ConfigDecl]map[string]Value
	failures []TestFailure
	// stopped is set by a failed `is` that binds a name: the test ends
	// there, and the fault that ends it isn't a failure of its own.
	stopped bool
}

func testRunOf(ctx context.Context) *testRun {
	r, _ := ctx.Value(testKey{}).(*testRun)
	return r
}

func (r *testRun) fail(f TestFailure) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = append(r.failures, f)
}

// RunTest runs t: it binds the test's fakes, its clock, and its random,
// runs its body, and returns every failure. in must be the test's own, its
// Capabilities the test's; RunTest adds a test Clock and a seeded Random
// to it unless it has them (§5), and a fake of either wins.
func (in *Interp) RunTest(ctx context.Context, t *syntax.TestDecl) []TestFailure {
	sc := scope{universe: in.Universe.ScopeOf(t), imports: in.Imports[t.Name], groups: in.Groups[t.Name]}
	run := &testRun{
		scope:  sc,
		fakes:  map[*syntax.CapabilityDecl]*syntax.FakeDecl{},
		byName: map[string]*syntax.CapabilityDecl{},
		counts: map[*syntax.CapabilityDecl]map[string]int{},
		config: map[*syntax.ConfigDecl]map[string]Value{},
	}
	ctx = context.WithValue(ctx, testKey{}, run)
	ctx = withScope(ctx, sc)
	ctx, endOwner := withResourceOwner(ctx)
	defer endOwner()

	clock := TestClockStart
	h := fnv.New64a()
	h.Write([]byte(t.Name))
	seed := h.Sum64()
	_, hasClock := in.Capabilities.Lookup("Clock")
	_, hasRandom := in.Capabilities.Lookup("Random")
	for _, f := range t.Fakes {
		switch f.TargetName() {
		case "Clock":
			if v, fault := in.fakeInt(ctx, f, "now"); fault != nil {
				return []TestFailure{{At: f.Start, Message: fault.String()}}
			} else if v != nil {
				clock = time.UnixMilli(*v).UTC()
			}
			hasClock = false
			continue
		case "Random":
			if v, fault := in.fakeInt(ctx, f, "seed"); fault != nil {
				return []TestFailure{{At: f.Start, Message: fault.String()}}
			} else if v != nil {
				seed = uint64(*v)
			}
			hasRandom = false
			continue
		}
		group, config := in.fakeTarget(sc, f.Target)
		switch {
		case group != nil:
			run.fakes[group] = f
			run.byName[f.TargetName()] = group
		case config != nil:
			values := map[string]Value{}
			for _, a := range f.Values {
				v, fault := in.EvalExpr(ctx, a.Value, NewEnv())
				if fault != nil {
					return []TestFailure{{At: f.Start, Message: fault.String()}}
				}
				if field, ok := config.Field(a.Name); ok && isSecretType(field.Type) {
					if s, ok := v.(Str); ok {
						v = NewSecret(field.Env, string(s))
					}
				}
				values[a.Name] = v
			}
			run.config[config] = values
		}
	}
	if !hasClock {
		manual := NewManualClock(clock)
		in.Clock = manual
		in.Capabilities.Register("Clock", ClockProvider{Clock: manual})
	}
	if !hasRandom {
		in.Capabilities.Register("Random", NewSeededRandomProvider(seed))
	}

	val, returned, fault := in.evalBlock(ctx, t.Body, NewEnv(), false)
	run.mu.Lock()
	stopped := run.stopped
	run.mu.Unlock()
	switch {
	case stopped:
	case fault != nil:
		run.fail(TestFailure{At: t.Start, Message: fault.String()})
	case returned:
		if r, ok := val.(Result); ok && !r.Ok {
			run.fail(TestFailure{At: t.Start, Message: "`?` stopped the test with " + r.String()})
		}
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	return append([]TestFailure(nil), run.failures...)
}

func isSecretType(t syntax.Type) bool {
	q, ok := t.(*syntax.QualifiedType)
	return ok && len(q.Path) == 1 && q.Path[0] == "Secret"
}

// fakeInt is a Clock's or Random's one value, nil when the fake leaves it
// out.
func (in *Interp) fakeInt(ctx context.Context, f *syntax.FakeDecl, name string) (*int64, *Fault) {
	for _, a := range f.Values {
		if a.Name != name {
			continue
		}
		v, fault := in.EvalExpr(ctx, a.Value, NewEnv())
		if fault != nil {
			return nil, fault
		}
		i, ok := v.(Int)
		if !ok {
			return nil, Faultf("fake %s's %s is %s, not an int", f.TargetName(), name, v)
		}
		n := int64(i)
		return &n, nil
	}
	return nil, nil
}

// fakeTarget resolves a fake's target in the test's scope, as the checker
// does (compiler/checker.ResolveFake).
func (in *Interp) fakeTarget(sc scope, target []string) (*syntax.CapabilityDecl, *syntax.ConfigDecl) {
	var d syntax.Decl
	switch len(target) {
	case 1:
		if p, ok := in.groupValue(sc, target[0]); ok {
			return p.Group, nil
		}
		if g, ok := sc.groups[target[0]]; ok && g.Config != nil {
			return nil, g.Config
		}
		d, _ = sc.universe.Lookup(target[0])
	case 2:
		if p, ok := sc.imports[target[0]]; ok && p.Universe != nil {
			d, _ = p.Universe.Lookup(target[1])
		}
	}
	switch d := d.(type) {
	case *syntax.CapabilityDecl:
		return d, nil
	case *syntax.ConfigDecl:
		return nil, d
	}
	return nil, nil
}

// fakeCall is a call to a faked group's operation: the fake's gateway runs
// in the test's scope, and the call is counted. ok is false when the group
// isn't faked in this test.
func (in *Interp) fakeCall(ctx context.Context, p Provider, member string, args []syntax.Arg, env *Env) (Value, bool, *Fault, bool) {
	run := testRunOf(ctx)
	if run == nil {
		return nil, false, nil, false
	}
	f, faked := run.fakes[p.Group]
	if !faked {
		return nil, false, nil, false
	}
	gw := f.Gateway(member)
	if gw == nil {
		return nil, false, Faultf("this test reaches %s.%s, which fake %s doesn't have (ADR 0091 §4)", p.Group.Name, member, f.TargetName()), true
	}
	fr, vals := newCallFrame(len(gw.Params))
	supplied, early, earlyReturn, fault := in.evalParamArgs(ctx, env, args, gw.Params, vals)
	if fault != nil || earlyReturn {
		return early, earlyReturn, fault, true
	}
	run.mu.Lock()
	if run.counts[p.Group] == nil {
		run.counts[p.Group] = map[string]int{}
	}
	run.counts[p.Group][member]++
	run.mu.Unlock()
	v, f2 := in.callDeclValues(ctx, run.scope, "fake "+f.TargetName()+"."+member, gw, supplied, fr)
	return v, false, f2, true
}

// evalCalls is `calls(Fake.op)`: how many times this test has called that
// faked operation.
func (in *Interp) evalCalls(ctx context.Context, e *syntax.CallExpr) (Value, bool, *Fault) {
	run := testRunOf(ctx)
	if run == nil || len(e.Args) != 1 {
		return nil, false, Faultf("calls(...) counts a faked operation, in a test")
	}
	target, op, ok := fakedOperation(e.Args[0].Value)
	group := run.byName[target]
	if !ok || group == nil {
		return nil, false, Faultf("calls(...) takes an operation this test fakes")
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	return Int(run.counts[group][op]), false, nil
}

func fakedOperation(e syntax.Expr) (target, op string, ok bool) {
	fa, isAccess := e.(*syntax.FieldAccess)
	if !isAccess {
		return "", "", false
	}
	switch t := fa.Target.(type) {
	case *syntax.Ident:
		return t.Name, fa.Field, true
	case *syntax.FieldAccess:
		if pkg, isIdent := t.Target.(*syntax.Ident); isIdent {
			return pkg.Name + "." + t.Field, fa.Field, true
		}
	}
	return "", "", false
}

// evalExpect is `expect cond` (§3): a false one is recorded and the test
// goes on. A comparison's operands are each evaluated once and reported.
func (in *Interp) evalExpect(ctx context.Context, s *syntax.ExpectStmt, env *Env) (Value, bool, *Fault) {
	run := testRunOf(ctx)
	if run == nil {
		return nil, false, Faultf("expect runs only in a test")
	}
	if s.Pattern != nil {
		return in.evalExpectIs(ctx, run, s, env)
	}
	cond, isBinary := s.Cond.(*syntax.BinaryExpr)
	if isBinary {
		switch cond.Op {
		case syntax.OpEq, syntax.OpNe, syntax.OpLt, syntax.OpLe, syntax.OpGt, syntax.OpGe:
		default:
			isBinary = false
		}
	}
	if !isBinary {
		v, earlyReturn, fault := in.evalExpr(ctx, s.Cond, env)
		if fault != nil || earlyReturn {
			return v, earlyReturn, fault
		}
		if b, ok := v.(Bool); ok && !bool(b) {
			run.fail(TestFailure{At: s.Start, Expect: true})
		}
		return nil, false, nil
	}
	left, earlyReturn, fault := in.evalExpr(ctx, cond.Left, env)
	if fault != nil || earlyReturn {
		return left, earlyReturn, fault
	}
	right, earlyReturn, fault := in.evalExpr(ctx, cond.Right, env)
	if fault != nil || earlyReturn {
		return right, earlyReturn, fault
	}
	v, _, fault := in.applyBinary(cond, left, right)
	if fault != nil {
		return nil, false, fault
	}
	if b, ok := v.(Bool); ok && !bool(b) {
		run.fail(TestFailure{At: s.Start, Expect: true, Left: left.String(), Right: right.String()})
	}
	return nil, false, nil
}

// evalExpectIs is `expect value is Pattern`: the pattern's names are bound
// in env for the rest of the block. When it doesn't match, the value is
// reported, and a pattern that binds a name stops the test, since the
// lines after it would read names with no value.
func (in *Interp) evalExpectIs(ctx context.Context, run *testRun, s *syntax.ExpectStmt, env *Env) (Value, bool, *Fault) {
	v, earlyReturn, fault := in.evalExpr(ctx, s.Cond, env)
	if fault != nil || earlyReturn {
		return v, earlyReturn, fault
	}
	if matchPattern(s.Pattern, v, env, true) {
		return nil, false, nil
	}
	run.fail(TestFailure{At: s.Start, Expect: true, Value: v.String()})
	if !syntax.PatternBinds(s.Pattern) {
		return nil, false, nil
	}
	run.mu.Lock()
	run.stopped = true
	run.mu.Unlock()
	return nil, false, Faultf("the test stopped at a failed `expect ... is`")
}

// testConfigValue is a config read in a test: the test's fake, else what
// -env-file gave (in.Config), else a failure naming the value (§5).
func (in *Interp) testConfigValue(ctx context.Context, d *syntax.ConfigDecl, field string) (Value, *Fault, bool) {
	run := testRunOf(ctx)
	if run == nil {
		return nil, nil, false
	}
	if v, ok := run.config[d][field]; ok {
		return v, nil, true
	}
	if v, ok := in.Config[d].Fields[field]; ok {
		return v, nil, true
	}
	env := ""
	if f, ok := d.Field(field); ok {
		env = f.Env
	}
	return nil, Faultf("this test reads %s.%s (%s), which it doesn't fake; add `fake %s { %s: ... }`, or run fiig test with -env-file (ADR 0091 §5)", d.Name, field, env, d.Name, field), true
}

// unprovided is the fault for a capability nothing provides in a test.
func unprovided(ctx context.Context, capability string) (*Fault, bool) {
	if testRunOf(ctx) == nil {
		return nil, false
	}
	return Faultf("this test reaches %s, which nothing provides: fake it, or give it with fiig test -caps (ADR 0091 §5)", capability), true
}
