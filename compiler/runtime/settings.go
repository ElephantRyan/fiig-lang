package runtime

import (
	"context"
	"sync"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0140: config fields that read a setting saved in the app. A soft
// setting is read live, one snapshot per run; a hard one (`on restart`) is
// read once, at start, with the environment.

// SettingsStore is where saved settings live (§2): the application's
// Database in `fiig serve` and `fiig run`, memory in `fiig test`. Settings
// are keyed by package path, "" for the application, so renaming the
// application leaves its settings where they are.
type SettingsStore interface {
	// Version changes on every Save and Clear, so a process knows when its
	// snapshot is stale without reading every setting (§3).
	Version(ctx context.Context) (int64, *Fault)
	// Load reads every saved setting: package path, then name, then text.
	Load(ctx context.Context) (map[string]map[string]string, int64, *Fault)
	Save(ctx context.Context, pkg, name, text, by string) *Fault
	Clear(ctx context.Context, pkg, name, by string) *Fault
}

// MemorySettings is a SettingsStore in memory: each test gets its own, so a
// test can save a setting and read it back (ADR 0091).
type MemorySettings struct {
	mu      sync.Mutex
	values  map[string]map[string]string
	version int64
}

func NewMemorySettings() *MemorySettings {
	return &MemorySettings{values: map[string]map[string]string{}}
}

func (m *MemorySettings) Version(context.Context) (int64, *Fault) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.version, nil
}

func (m *MemorySettings) Load(context.Context) (map[string]map[string]string, int64, *Fault) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]map[string]string, len(m.values))
	for pkg, names := range m.values {
		copied := make(map[string]string, len(names))
		for k, v := range names {
			copied[k] = v
		}
		out[pkg] = copied
	}
	return out, m.version, nil
}

func (m *MemorySettings) Save(_ context.Context, pkg, name, text, _ string) *Fault {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.values[pkg] == nil {
		m.values[pkg] = map[string]string{}
	}
	m.values[pkg][name] = text
	m.version++
	return nil
}

func (m *MemorySettings) Clear(_ context.Context, pkg, name, _ string) *Fault {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values[pkg], name)
	m.version++
	return nil
}

// settingRef is one config field that reads a setting, with what reading
// it needs and what the environment gave at start.
type settingRef struct {
	src   ConfigSource
	field syntax.ConfigField
	t     types.Type
	env   Value // what the env source gave at start; nil when unset
	def   Value // the default; nil when there is none
}

// settingsSnapshot is every saved setting at one version.
type settingsSnapshot struct {
	version int64
	values  map[string]map[string]string
}

func (s *settingsSnapshot) text(pkg, name string) (string, bool) {
	if s == nil {
		return "", false
	}
	v, ok := s.values[pkg][name]
	return v, ok
}

type runSettingsKey struct{}

// runSettings is the snapshot one run reads (§3), taken at its first read.
type runSettings struct {
	once  sync.Once
	snap  *settingsSnapshot
	fault *Fault
}

// UseSettings makes store where this interpreter's settings live, and
// provides the Settings capability over it. It comes before LoadConfig, so
// hard settings are read at start.
func (in *Interp) UseSettings(store SettingsStore) {
	in.Settings = store
	in.settingsCache.Store(nil)
	in.Capabilities.Register(types.SettingsName, &SettingsProvider{in: in})
}

// ShareSettingFields gives in the setting fields from, which loaded the
// config: `fiig test` loads config once and runs each test on its own
// interpreter.
func (in *Interp) ShareSettingFields(from *Interp) {
	if in.settingRefs == nil {
		in.settingRefs = map[*types.Universe]map[string]*settingRef{}
		in.settingFields = map[*syntax.ConfigDecl]map[string]*settingRef{}
	}
	// Merged, not replaced: each test package loads the config its tests
	// reach, and a package's settings may be reached from several.
	for u, refs := range from.settingRefs {
		if in.settingRefs[u] == nil {
			in.settingRefs[u] = map[string]*settingRef{}
		}
		for name, ref := range refs {
			in.settingRefs[u][name] = ref
		}
	}
	for d, refs := range from.settingFields {
		if in.settingFields[d] == nil {
			in.settingFields[d] = map[string]*settingRef{}
		}
		for name, ref := range refs {
			in.settingFields[d][name] = ref
		}
	}
}

// withRun gives ctx a run's snapshot holder unless it already has one, so a
// call nested in a run reads the same snapshot.
func (in *Interp) withRun(ctx context.Context) context.Context {
	if in.Settings == nil || ctx.Value(runSettingsKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, runSettingsKey{}, &runSettings{})
}

// snapshot is the run's snapshot, or the current one outside a run.
func (in *Interp) snapshot(ctx context.Context) (*settingsSnapshot, *Fault) {
	if in.Settings == nil {
		return nil, nil
	}
	run, _ := ctx.Value(runSettingsKey{}).(*runSettings)
	if run == nil {
		return in.currentSettings(ctx)
	}
	run.once.Do(func() { run.snap, run.fault = in.currentSettings(ctx) })
	return run.snap, run.fault
}

// currentSettings is the latest snapshot: the cached one while the store's
// version is unchanged, so a run costs one small read (§3).
func (in *Interp) currentSettings(ctx context.Context) (*settingsSnapshot, *Fault) {
	v, f := in.Settings.Version(ctx)
	if f != nil {
		return nil, Faultf("reading settings: %s", f.Message)
	}
	if cached := in.settingsCache.Load(); cached != nil && cached.version == v {
		return cached, nil
	}
	values, v, f := in.Settings.Load(ctx)
	if f != nil {
		return nil, Faultf("reading settings: %s", f.Message)
	}
	snap := &settingsSnapshot{version: v, values: values}
	in.settingsCache.Store(snap)
	return snap, nil
}

// softValue is a soft setting's value for this run: its sources in the
// order written, then its default, then None.
func (in *Interp) softValue(ctx context.Context, ref *settingRef) (Value, *Fault) {
	snap, f := in.snapshot(ctx)
	if f != nil {
		return nil, f
	}
	return in.settingValue(ref, in.envOf(ctx, ref), snap)
}

// envOf is what ref's env source gives the run in ctx: a test's fake of
// the field stands in for it (ADR 0140 §1), so the declared order still
// decides, and a fake of None is an unset variable. nil when unset.
func (in *Interp) envOf(ctx context.Context, ref *settingRef) Value {
	if run := testRunOf(ctx); run != nil {
		if v, faked := run.config[ref.src.Decl][ref.field.Name]; faked {
			if _, none := v.(None); none {
				return nil
			}
			return v
		}
	}
	return ref.env
}

// settingValue resolves ref from env, its env source's value, and snap.
func (in *Interp) settingValue(ref *settingRef, env Value, snap *settingsSnapshot) (Value, *Fault) {
	if env != nil && !ref.field.SettingFirst {
		return env, nil
	}
	if text, saved := snap.text(ref.src.Package, ref.field.Setting); saved {
		v, problem := in.readConfigValue(ref.src, text, ref.field.Setting, ref.t)
		if problem != "" {
			return nil, Faultf("setting %s is saved as text that isn't %s; %s (ADR 0140 §4)", ref.field.Setting, problem, ref.src.Where(ref.field))
		}
		return v, nil
	}
	if env != nil {
		return env, nil
	}
	if ref.def != nil {
		return ref.def, nil
	}
	return None{}, nil
}

// softField is the soft setting d.field reads, if it reads one.
func (in *Interp) softField(d *syntax.ConfigDecl, field string) (*settingRef, bool) {
	ref, ok := in.settingField(d, field)
	return ref, ok && ref.field.SoftSetting()
}

// settingField is the setting d.field reads, soft or hard, if it reads one.
func (in *Interp) settingField(d *syntax.ConfigDecl, field string) (*settingRef, bool) {
	ref, ok := in.settingFields[d][field]
	return ref, ok
}

// settingNamed is the setting name of the package running ctx.
func (in *Interp) settingNamed(ctx context.Context, name string) (*settingRef, *Fault) {
	u := in.scope(ctx).universe
	for u != nil && u.Parent != nil {
		u = u.Parent
	}
	if ref, ok := in.settingRefs[u][name]; ok {
		return ref, nil
	}
	return nil, Faultf("no config field of this package reads setting %q (ADR 0140 §4)", name)
}

// SettingsProvider is the Settings capability (ADR 0140 §4).
type SettingsProvider struct{ in *Interp }

func (p *SettingsProvider) Call(ctx context.Context, method string, args []Value) (Value, *Fault) {
	in := p.in
	if in.Settings == nil {
		return nil, Faultf("Settings has nowhere to save: no Database is bound for this program (ADR 0140 §2)")
	}
	if len(args) == 0 {
		return nil, Faultf("Settings.%s takes a setting's name", method)
	}
	name, ok := args[0].(Str)
	if !ok {
		return nil, Faultf("Settings.%s takes a setting's name as a string", method)
	}
	ref, f := in.settingNamed(ctx, string(name))
	if f != nil {
		return nil, f
	}
	fixed := in.envOf(ctx, ref) != nil && !ref.field.SettingFirst
	switch method {
	case "save":
		if len(args) != 3 {
			return nil, Faultf("Settings.save takes a name, the text and who saved it")
		}
		text, _ := args[1].(Str)
		by, _ := args[2].(Str)
		if fixed {
			return settingErr("SetByDeployment"), nil
		}
		if _, problem := in.readConfigValue(ref.src, string(text), ref.field.Setting, ref.t); problem != "" || text == "" {
			if problem == "" {
				problem = "empty; clear the setting instead"
			} else {
				problem = "not " + problem
			}
			return settingErr("Invalid", Str(problem)), nil
		}
		if f := in.Settings.Save(ctx, ref.src.Package, ref.field.Setting, string(text), string(by)); f != nil {
			return nil, f
		}
		return Result{Ok: true, Value: None{}}, nil
	case "clear":
		if len(args) != 2 {
			return nil, Faultf("Settings.clear takes a name and who cleared it")
		}
		by, _ := args[1].(Str)
		if fixed {
			return settingErr("SetByDeployment"), nil
		}
		if f := in.Settings.Clear(ctx, ref.src.Package, ref.field.Setting, string(by)); f != nil {
			return nil, f
		}
		return Result{Ok: true, Value: None{}}, nil
	case "source":
		return in.settingSource(ctx, ref, fixed)
	}
	return nil, Faultf("Settings has no operation %q", method)
}

// settingSource is where ref's value comes from now. A hard setting saved
// since the start is Pending: it applies on the next start (§1a).
func (in *Interp) settingSource(ctx context.Context, ref *settingRef, fixed bool) (Value, *Fault) {
	source := func(variant string) (Value, *Fault) {
		return EnumValue{TypeName: "SettingSource", Variant: variant, Decl: types.SettingSourceDecl}, nil
	}
	if fixed {
		return source("Deployment")
	}
	snap, f := in.currentSettings(ctx)
	if f != nil {
		return nil, f
	}
	now, saved := snap.text(ref.src.Package, ref.field.Setting)
	if ref.field.OnRestart {
		atStart, wasSaved := in.startSettings.text(ref.src.Package, ref.field.Setting)
		if saved != wasSaved || now != atStart {
			return source("Pending")
		}
	}
	switch {
	case saved:
		return source("Saved")
	case in.envOf(ctx, ref) != nil:
		return source("Deployment")
	case ref.def != nil:
		return source("Default")
	}
	return source("Unset")
}

func settingErr(variant string, fields ...Value) Value {
	return Result{Ok: false, Value: EnumValue{TypeName: "SettingError", Variant: variant, Fields: fields, Decl: types.SettingErrorDecl}}
}
