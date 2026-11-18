package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"fiig-lang/compiler/syntax"
)

// ADR 0084: a Database bound to a driver written in fiig. The runtime owns
// the pool (§3) and the transaction's connection (§5); the driver's
// capability group answers connect, run, and close (§1) on connections the
// pool owns (§4).

const (
	// SqlDriverInterface is the interface a driver package provides.
	SqlDriverInterface = "fiig.sql.SqlDriver"
	sqlPackage         = "fiig.sql"

	defaultMaxConnections = 10
	defaultIdleTimeout    = 5 * time.Minute
	defaultMaxLifetime    = 30 * time.Minute
	// closeTimeout bounds a driver's close when no call is waiting on it
	// (eviction, shutdown).
	closeTimeout = 5 * time.Second
)

// DriverFactory is the "driver" capability type (§2):
// {"type": "driver", "driver": "<package path>", "settings": {...},
// "maxConnections": 10, "idleTimeout": "5m", "maxLifetime": "30m"}.
func DriverFactory(cfg map[string]any) (CapabilityProvider, error) {
	path, _ := cfg["driver"].(string)
	if path == "" {
		return nil, fmt.Errorf(`driver config requires "driver", the package path of a driver (ADR 0084)`)
	}
	settings := map[string]string{}
	if raw, ok := cfg["settings"]; ok {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf(`driver "settings" must be an object of strings`)
		}
		for k, v := range m {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("driver setting %q must be a string", k)
			}
			settings[k] = s
		}
	}
	p := &DriverProvider{path: path, settings: settings, max: defaultMaxConnections, idle: defaultIdleTimeout, life: defaultMaxLifetime}
	var err error
	if p.max, err = intSetting(cfg, "maxConnections", defaultMaxConnections); err != nil {
		return nil, err
	}
	if p.max < 1 {
		return nil, fmt.Errorf("driver maxConnections must be at least 1, got %d", p.max)
	}
	if p.idle, err = durationSetting(cfg, "idleTimeout", defaultIdleTimeout); err != nil {
		return nil, err
	}
	if p.life, err = durationSetting(cfg, "maxLifetime", defaultMaxLifetime); err != nil {
		return nil, err
	}
	p.slots = make(chan struct{}, p.max)
	return p, nil
}

func intSetting(cfg map[string]any, name string, def int) (int, error) {
	raw, ok := cfg[name]
	if !ok {
		return def, nil
	}
	var text string
	switch v := raw.(type) {
	case json.Number:
		text = string(v)
	case float64:
		text = strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return v, nil
	case string:
		text = v
	default:
		return 0, fmt.Errorf("driver %s must be a whole number", name)
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("driver %s must be a whole number, got %s", name, text)
	}
	return n, nil
}

func durationSetting(cfg map[string]any, name string, def time.Duration) (time.Duration, error) {
	raw, ok := cfg[name]
	if !ok {
		return def, nil
	}
	s, ok := raw.(string)
	if !ok {
		return 0, fmt.Errorf(`driver %s must be a duration such as "5m"`, name)
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf(`driver %s must be a positive duration such as "5m", got %q`, name, s)
	}
	return d, nil
}

// DriverProvider is a pool of connections opened by a fiig driver.
type DriverProvider struct {
	path     string
	settings map[string]string
	max      int
	idle     time.Duration
	life     time.Duration

	in *Interp // set by New (bindInterp)

	resolveOnce sync.Once
	resolveErr  *Fault
	group       *syntax.CapabilityDecl
	sc          scope
	sqlValue    *syntax.EnumDecl

	// slots holds one token per connection checked out or being opened,
	// so at most max exist at once.
	slots   chan struct{}
	mu      sync.Mutex
	idleSet []*pooledConn
	closed  bool
	stop    chan struct{}
	started bool
}

// pooledConn is one connection the pool owns. owner closes its sockets
// when the pool discards it, however the driver left them (§4).
type pooledConn struct {
	conn     Value
	owner    *resourceOwner
	created  time.Time
	lastUsed time.Time
}

func (p *DriverProvider) bindInterp(in *Interp) { p.in = in }

// resolve finds the driver's capability group and fiig.sql's SqlValue in
// the program's packages, once.
func (p *DriverProvider) resolve() *Fault {
	p.resolveOnce.Do(func() {
		if p.in == nil {
			p.resolveErr = Faultf("Database: driver %s has no interpreter", p.path)
			return
		}
		pkg, ok := p.in.Packages[p.path]
		if !ok {
			p.resolveErr = Faultf("Database: driver package %s isn't part of this program; add it to the application manifest's dependencies (ADR 0084 §2)", p.path)
			return
		}
		name := pkg.Provides[SqlDriverInterface]
		d, found := pkg.Universe.Lookup(name)
		group, isGroup := d.(*syntax.CapabilityDecl)
		if name == "" || !found || !isGroup {
			p.resolveErr = Faultf("Database: package %s doesn't provide %s; its manifest names no capability group for it (ADR 0084 §1)", p.path, SqlDriverInterface)
			return
		}
		sqlPkg := pkg.Dependencies[sqlPackage]
		if sqlPkg == nil {
			sqlPkg = p.in.Packages[sqlPackage]
		}
		if sqlPkg == nil {
			p.resolveErr = Faultf("Database: driver package %s doesn't depend on %s", p.path, sqlPackage)
			return
		}
		sv, _ := sqlPkg.Universe.Lookup("SqlValue")
		p.sqlValue, _ = sv.(*syntax.EnumDecl)
		p.group, p.sc = group, packageScope(pkg)
	})
	return p.resolveErr
}

// gateway calls one of the driver's gateways with args by position, on
// behalf of owner: a socket the driver opens belongs to the connection,
// not to the program call that needed it (§4).
func (p *DriverProvider) gateway(ctx context.Context, owner *resourceOwner, name string, args ...Value) (Value, *Fault) {
	gw := p.group.Gateway(name)
	if gw == nil {
		return nil, Faultf("Database: driver %s has no gateway %q", p.path, name)
	}
	if len(gw.Params) != len(args) {
		return nil, Faultf("Database: driver %s's %s takes %d parameters, not %d", p.path, name, len(gw.Params), len(args))
	}
	named := make(map[string]Value, len(args))
	for i, prm := range gw.Params {
		named[prm.Name] = args[i]
	}
	ctx = context.WithValue(ctx, resourceOwnerKey{}, owner)
	return p.in.callDecl(ctx, p.sc, p.group.Name+"."+name, gw, named)
}

func (p *DriverProvider) Call(ctx context.Context, method string, args []Value) (Value, *Fault) {
	if f := p.resolve(); f != nil {
		return nil, f
	}
	if method != "execute" && method != "queryOne" && method != "queryMany" {
		return nil, Faultf("Database has no operation %q", method)
	}
	pc, f := p.checkout(ctx)
	if f != nil {
		return nil, f
	}
	v, keep, f := p.statement(ctx, pc, method, args)
	p.finish(ctx, pc, keep)
	return v, f
}

// statement runs one Database call on pc. keep is false when the
// connection can't be trusted afterwards (§3).
func (p *DriverProvider) statement(ctx context.Context, pc *pooledConn, method string, args []Value) (Value, bool, *Fault) {
	if len(args) == 0 {
		return nil, true, Faultf("Database.%s requires the SQL text as its first argument", method)
	}
	text, ok := args[0].(Str)
	if !ok {
		return nil, true, Faultf("Database.%s: the SQL text must be a string, got %s", method, args[0])
	}
	values := make([]Value, len(args)-1)
	for i, a := range args[1:] {
		v, f := p.toSqlValue(a)
		if f != nil {
			return nil, true, Faultf("Database.%s: argument %d: %s", method, i+1, f.Message)
		}
		values[i] = v
	}
	res, keep, f := p.run(ctx, pc, "Database."+method, string(text), values)
	if f != nil {
		return nil, keep, f
	}
	v, f := resultValue(method, res)
	return v, true, f
}

// run calls the driver's run and turns a DatabaseError into a fault (§6).
func (p *DriverProvider) run(ctx context.Context, pc *pooledConn, what, sql string, values []Value) (Record, bool, *Fault) {
	out, f := p.gateway(ctx, pc.owner, "run", pc.conn, Str(sql), List{Elems: values})
	if f != nil {
		return Record{}, false, f
	}
	if ctx.Err() != nil {
		return Record{}, false, Faultf("%s: cancelled", what)
	}
	r, ok := out.(Result)
	if !ok {
		return Record{}, false, Faultf("%s: driver %s's run returned %s, not a Result", what, p.path, out)
	}
	if !r.Ok {
		keep, msg := describeDatabaseError(r.Value)
		f := Faultf("%s: %s", what, msg)
		f.Conflict = serverConflict(r.Value)
		return Record{}, keep, f
	}
	rec, ok := r.Value.(Record)
	if !ok {
		return Record{}, false, Faultf("%s: driver %s's run returned %s, not a SqlResult", what, p.path, r.Value)
	}
	return rec, true, nil
}

// describeDatabaseError is the fault message for a DatabaseError, and
// whether the connection survives it: only a server's error leaves it in
// a known state (§3).
func describeDatabaseError(v Value) (bool, string) {
	e, ok := v.(EnumValue)
	if !ok {
		return false, fmt.Sprintf("driver error %s", v)
	}
	switch {
	case e.Variant == "Server" && len(e.Fields) >= 2:
		return true, fmt.Sprintf("%s %s", plain(e.Fields[0]), plain(e.Fields[1]))
	case e.Variant == "Broken" && len(e.Fields) == 1:
		return false, fmt.Sprintf("the connection failed: %s", e.Fields[0])
	case e.Variant == "Protocol" && len(e.Fields) == 1:
		// The driver's reason says what went wrong: a reply it couldn't
		// read, a setting, or a server it won't talk to.
		return false, plain(e.Fields[0])
	}
	return false, e.String()
}

// serverConflict is the conflict a Server error reports (ADR 0087 §4):
// Server(code, message, constraint, table, column), the last three
// string | None.
func serverConflict(v Value) *Conflict {
	e, ok := v.(EnumValue)
	if !ok || e.Variant != "Server" || len(e.Fields) < 2 {
		return nil
	}
	field := func(i int) string {
		if i < len(e.Fields) {
			if s, ok := e.Fields[i].(Str); ok {
				return string(s)
			}
		}
		return ""
	}
	return SQLStateConflict(field(0), field(2), field(4), field(1))
}

func plain(v Value) string {
	if s, ok := v.(Str); ok {
		return string(s)
	}
	return v.String()
}

// resultValue turns a SqlResult into what the Database operation returns
// before ADR 0082's reader types it: rows affected, or rows as records
// keyed by column name.
func resultValue(method string, res Record) (Value, *Fault) {
	if method == "execute" {
		n, ok := res.Fields["affected"].(Int)
		if !ok {
			return nil, Faultf("Database.execute: the driver's result has no affected count")
		}
		return n, nil
	}
	cols, _ := res.Fields["columns"].(List)
	rows, _ := res.Fields["rows"].(List)
	names := make([]string, len(cols.Elems))
	seen := map[string]bool{}
	for i, c := range cols.Elems {
		names[i] = plain(c)
		if seen[names[i]] {
			return nil, Faultf("Database.%s: two columns are named %q; rename one with AS (ADR 0082 §6)", method, names[i])
		}
		seen[names[i]] = true
	}
	records := make([]Value, len(rows.Elems))
	for i, r := range rows.Elems {
		row, _ := r.(List)
		if len(row.Elems) != len(names) {
			return nil, Faultf("Database.%s: row %d has %d values for %d columns", method, i+1, len(row.Elems), len(names))
		}
		fields := make(map[string]Value, len(names))
		for j, v := range row.Elems {
			fields[names[j]] = fromSqlValue(v)
		}
		records[i] = Record{Fields: fields}
	}
	if method == "queryOne" {
		if len(records) == 0 {
			return None{}, nil
		}
		return records[0], nil
	}
	return List{Elems: records}, nil
}

// toSqlValue binds one argument (ADR 0082 §4) as a fiig.sql SqlValue.
func (p *DriverProvider) toSqlValue(v Value) (Value, *Fault) {
	variant := func(name string, fields ...Value) Value {
		return EnumValue{TypeName: "SqlValue", Variant: name, Fields: fields, Decl: p.sqlValue}
	}
	switch v := v.(type) {
	case None:
		return variant("Null"), nil
	case Int:
		return variant("Int", v), nil
	case Float:
		return variant("Float", v), nil
	case Str:
		return variant("Text", v), nil
	case Bool:
		return variant("Bool", v), nil
	case Bytes:
		return variant("Bytes", v), nil
	case Decimal:
		return variant("Text", Str(v.String())), nil
	case EnumValue:
		if len(v.Fields) == 0 && v.Decl != nil {
			if vr, ok := v.Decl.Variant(v.Variant); ok {
				return variant("Text", Str(vr.Text())), nil
			}
		}
	}
	return nil, Faultf("cannot bind %s as a SQL parameter", v)
}

func fromSqlValue(v Value) Value {
	e, ok := v.(EnumValue)
	if !ok || len(e.Fields) == 0 {
		return None{}
	}
	return e.Fields[0]
}

// checkout takes an idle connection, or opens one, waiting for a free
// slot no longer than ctx allows (§3).
func (p *DriverProvider) checkout(ctx context.Context) (*pooledConn, *Fault) {
	select {
	case p.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, Faultf("Database: all %d connections of driver %s's pool stayed busy until the call's deadline", p.max, p.path)
	}
	now := time.Now()
	for {
		p.mu.Lock()
		n := len(p.idleSet)
		if n == 0 {
			p.mu.Unlock()
			break
		}
		pc := p.idleSet[n-1]
		p.idleSet = p.idleSet[:n-1]
		p.mu.Unlock()
		if p.expired(pc, now) {
			p.closeConn(pc)
			continue
		}
		return pc, nil
	}
	pc, f := p.open(ctx)
	if f != nil {
		<-p.slots
		return nil, f
	}
	return pc, nil
}

func (p *DriverProvider) expired(pc *pooledConn, now time.Time) bool {
	return now.Sub(pc.lastUsed) >= p.idle || now.Sub(pc.created) >= p.life
}

// open runs the driver's connect for a new pooled connection.
func (p *DriverProvider) open(ctx context.Context) (*pooledConn, *Fault) {
	keys := make([]string, 0, len(p.settings))
	for k := range p.settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	settings := Map{}
	for _, k := range keys {
		settings = settings.Put(Str(k), Str(p.settings[k]))
	}
	owner := &resourceOwner{}
	out, f := p.gateway(ctx, owner, "connect", settings)
	if f == nil {
		if r, ok := out.(Result); !ok {
			f = Faultf("Database: driver %s's connect returned %s, not a Result", p.path, out)
		} else if !r.Ok {
			_, msg := describeDatabaseError(r.Value)
			f = Faultf("Database: connecting with driver %s: %s", p.path, msg)
		} else if _, ok := r.Value.(Connection); !ok {
			f = Faultf("Database: driver %s's connect returned %s, not a Connection", p.path, r.Value)
		} else {
			now := time.Now()
			p.startJanitor()
			return &pooledConn{conn: r.Value, owner: owner, created: now, lastUsed: now}, nil
		}
	}
	owner.end()
	return nil, f
}

// finish returns pc to the pool, or discards it (§3).
func (p *DriverProvider) finish(ctx context.Context, pc *pooledConn, keep bool) {
	if keep && ctx.Err() == nil {
		pc.lastUsed = time.Now()
		p.mu.Lock()
		if !p.closed {
			p.idleSet = append(p.idleSet, pc)
			p.mu.Unlock()
			<-p.slots
			return
		}
		p.mu.Unlock()
		p.closeConn(pc)
	} else {
		pc.owner.end()
	}
	<-p.slots
}

// closeConn asks the driver to close pc politely, then closes whatever
// sockets it left open.
func (p *DriverProvider) closeConn(pc *pooledConn) {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	p.gateway(ctx, pc.owner, "close", pc.conn)
	pc.owner.end()
}

// startJanitor closes connections idle or old past their limits while
// nothing asks for one (§3).
func (p *DriverProvider) startJanitor() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started || p.closed {
		return
	}
	p.started, p.stop = true, make(chan struct{})
	every := min(p.idle, p.life) / 2
	every = max(min(every, 30*time.Second), 10*time.Millisecond)
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-p.stop:
				return
			case now := <-t.C:
				p.evict(now)
			}
		}
	}()
}

func (p *DriverProvider) evict(now time.Time) {
	p.mu.Lock()
	var keep, gone []*pooledConn
	for _, pc := range p.idleSet {
		if p.expired(pc, now) {
			gone = append(gone, pc)
		} else {
			keep = append(keep, pc)
		}
	}
	p.idleSet = keep
	p.mu.Unlock()
	for _, pc := range gone {
		p.closeConn(pc)
	}
}

// Idle is how many connections the pool holds unused, for tests.
func (p *DriverProvider) Idle() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.idleSet)
}

// Close closes every idle connection and stops the pool; connections in
// use close when their call returns them.
func (p *DriverProvider) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	idle := p.idleSet
	p.idleSet = nil
	if p.started {
		close(p.stop)
	}
	p.mu.Unlock()
	for _, pc := range idle {
		p.closeConn(pc)
	}
}

// Begin pins one connection for a transaction block and starts it with
// BEGIN (§5).
func (p *DriverProvider) Begin(ctx context.Context) (Tx, *Fault) {
	if f := p.resolve(); f != nil {
		return nil, f
	}
	pc, f := p.checkout(ctx)
	if f != nil {
		return nil, f
	}
	if _, keep, f := p.run(ctx, pc, "transaction Database", "BEGIN", nil); f != nil {
		p.finish(ctx, pc, keep)
		return nil, f
	}
	return &driverTx{p: p, pc: pc, ctx: ctx, keep: true}, nil
}

// driverTx is a transaction on its one pinned connection. Calls in the
// block take turns on it, since a connection carries one statement at a
// time.
type driverTx struct {
	p    *DriverProvider
	pc   *pooledConn
	ctx  context.Context
	mu   sync.Mutex
	keep bool
	done bool
}

func (t *driverTx) Call(ctx context.Context, method string, args []Value) (Value, *Fault) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.keep {
		return nil, Faultf("Database.%s: this transaction's connection already failed", method)
	}
	v, keep, f := t.p.statement(ctx, t.pc, method, args)
	t.keep = t.keep && keep
	return v, f
}

func (t *driverTx) Commit() *Fault { return t.end("COMMIT") }

func (t *driverTx) Rollback() *Fault { return t.end("ROLLBACK") }

func (t *driverTx) end(sql string) *Fault {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return nil
	}
	t.done = true
	var f *Fault
	if t.keep {
		var keep bool
		_, keep, f = t.p.run(t.ctx, t.pc, "transaction Database: "+sql, sql, nil)
		t.keep = keep
	}
	t.p.finish(t.ctx, t.pc, t.keep)
	return f
}
