package runtime

import (
	"context"
	"fmt"
	"sync"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0112 §4: the lock a schedule adapter claims each firing with, so one
// replica runs it. "lock" binds ScheduleLock to a package written in fiig
// that provides fiig.schedule.ScheduleLock, called as SourceProvider calls
// a stream source; "local" always claims, for a schedule one process runs.

const (
	// ScheduleLockInterface is the interface a lock package provides.
	ScheduleLockInterface = "fiig.schedule.ScheduleLock"
	// ScheduleLockCapability is the name a capabilities file binds.
	ScheduleLockCapability = "ScheduleLock"
)

// ScheduleLock claims a firing of a job for one holder.
type ScheduleLock interface {
	CapabilityProvider
	// Claim reports whether this holder runs job's firing at firedAt
	// (Unix ms); a non-nil error is Err(LockError...), logged and skipped.
	Claim(ctx context.Context, job string, firedAt int64, holder string, holdMillis int64) (bool, error, *Fault)
	// Complete records that holder finished job's firedAt, for retainMillis
	// (ADR 0124 §6); a schedule never calls it.
	Complete(ctx context.Context, job string, firedAt int64, holder string, retainMillis int64) (error, *Fault)
	// Completed reports whether anyone has completed job's firedAt, so a
	// replica that lost the claim can wait for the one that won it.
	Completed(ctx context.Context, job string, firedAt int64) (bool, error, *Fault)
	// Describe says where claims go, for fiig serve's log.
	Describe() string
}

// LockFactory is the "lock" capability type: {"type": "lock", "lock":
// "<package path>"}.
func LockFactory(cfg map[string]any) (CapabilityProvider, error) {
	path, _ := cfg["lock"].(string)
	if path == "" {
		return nil, fmt.Errorf(`lock config requires "lock", the package path of a schedule lock (ADR 0112 §4)`)
	}
	for k := range cfg {
		if k != "type" && k != "lock" {
			return nil, fmt.Errorf("lock config has no setting %q; a lock reads its own settings from its config declarations (ADR 0112 §4)", k)
		}
	}
	return &LockProvider{path: path}, nil
}

// LocalLockFactory is the "local" capability type: every claim succeeds.
func LocalLockFactory(cfg map[string]any) (CapabilityProvider, error) {
	for k := range cfg {
		if k != "type" {
			return nil, fmt.Errorf("a local lock has no settings; %q isn't one", k)
		}
	}
	return LocalLock{}, nil
}

// LocalLock is a schedule run by one process only.
type LocalLock struct{}

func (LocalLock) Call(context.Context, string, []Value) (Value, *Fault) {
	return nil, Faultf("ScheduleLock is claimed by schedule adapters, not by a program (ADR 0112 §4)")
}

func (LocalLock) Claim(context.Context, string, int64, string, int64) (bool, error, *Fault) {
	return true, nil, nil
}

// Complete and Completed: a local lock's claim always wins, so nothing
// waits on a completion.
func (LocalLock) Complete(context.Context, string, int64, string, int64) (error, *Fault) {
	return nil, nil
}

func (LocalLock) Completed(context.Context, string, int64) (bool, error, *Fault) {
	return false, nil, nil
}

func (LocalLock) Describe() string { return "this process alone (local)" }

// LockProvider is a schedule lock written in fiig.
type LockProvider struct {
	path string
	in   *Interp

	once  sync.Once
	err   *Fault
	pkg   *types.Package
	group *syntax.CapabilityDecl
	sc    scope
}

func (p *LockProvider) bindInterp(in *Interp) { p.in = in }

func (p *LockProvider) Call(context.Context, string, []Value) (Value, *Fault) {
	return nil, Faultf("ScheduleLock is claimed by schedule adapters, not by a program (ADR 0112 §4)")
}

func (p *LockProvider) Describe() string { return p.path }

// Resolve finds the lock's capability group, once, returning its package
// and the group's name so the caller can read the config it uses.
func (p *LockProvider) Resolve() (*types.Package, string, *Fault) {
	p.once.Do(func() {
		if p.in == nil {
			p.err = Faultf("ScheduleLock: lock %s has no interpreter", p.path)
			return
		}
		pkg, ok := p.in.Packages[p.path]
		if !ok {
			p.err = Faultf("ScheduleLock: package %s isn't part of this program; add it to the application manifest's dependencies (ADR 0112 §4)", p.path)
			return
		}
		name := pkg.Provides[ScheduleLockInterface]
		d, found := pkg.Universe.Lookup(name)
		group, isGroup := d.(*syntax.CapabilityDecl)
		if name == "" || !found || !isGroup {
			p.err = Faultf("ScheduleLock: package %s doesn't provide %s; its manifest names no capability group for it (ADR 0112 §4)", p.path, ScheduleLockInterface)
			return
		}
		p.pkg, p.group, p.sc = pkg, group, packageScope(pkg)
	})
	if p.err != nil {
		return nil, "", p.err
	}
	return p.pkg, p.group.Name, nil
}

func (p *LockProvider) Claim(ctx context.Context, job string, firedAt int64, holder string, holdMillis int64) (bool, error, *Fault) {
	return p.gatewayBool(ctx, "claim", Str(job), Int(firedAt), Str(holder), Int(holdMillis))
}

func (p *LockProvider) Complete(ctx context.Context, job string, firedAt int64, holder string, retainMillis int64) (error, *Fault) {
	_, failure, f := p.gatewayBool(ctx, "complete", Str(job), Int(firedAt), Str(holder), Int(retainMillis))
	return failure, f
}

func (p *LockProvider) Completed(ctx context.Context, job string, firedAt int64) (bool, error, *Fault) {
	return p.gatewayBool(ctx, "completed", Str(job), Int(firedAt))
}

// gatewayBool calls the lock's gateway name, which takes args and returns
// Result<bool, LockError>: the bool, or an Unavailable failure the caller
// reports.
func (p *LockProvider) gatewayBool(ctx context.Context, name string, args ...Value) (bool, error, *Fault) {
	if _, _, f := p.Resolve(); f != nil {
		return false, nil, f
	}
	gw := p.group.Gateway(name)
	if gw == nil || len(gw.Params) != len(args) {
		return false, nil, Faultf("ScheduleLock: lock %s has no %s gateway of %d parameters (ADR 0124 §6)", p.path, name, len(args))
	}
	named := make(map[string]Value, len(args))
	for i, prm := range gw.Params {
		named[prm.Name] = args[i]
	}
	ctx, end := withResourceOwner(context.WithValue(ctx, resourceOwnerKey{}, nil))
	defer end()
	out, f := p.in.callDecl(ctx, p.sc, p.group.Name+"."+name, gw, named)
	if f != nil {
		return false, nil, f
	}
	r, ok := out.(Result)
	if !ok {
		return false, nil, Faultf("ScheduleLock: %s's %s returned %s, not a Result", p.path, name, out)
	}
	if !r.Ok {
		reason := r.Value.String()
		if ev, ok := r.Value.(EnumValue); ok && len(ev.Fields) == 1 {
			if s, ok := ev.Fields[0].(Str); ok {
				reason = string(s)
			}
		}
		return false, &SourceFailure{Variant: "Unavailable", Reason: reason}, nil
	}
	b, ok := r.Value.(Bool)
	if !ok {
		return false, nil, Faultf("ScheduleLock: %s's %s returned Ok(%s), not a bool", p.path, name, r.Value)
	}
	return bool(b), nil, nil
}
