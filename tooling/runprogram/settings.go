package runprogram

import (
	"context"
	"time"

	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
	"fiig-lang/tooling/migrate"
)

// ADR 0140 §2: settings saved in the app live in the application's
// Database, in migrate.SettingsTables.

// DatabaseSettings is a runtime.SettingsStore in a Database.
type DatabaseSettings struct{ db runtime.CapabilityProvider }

// NewDatabaseSettings makes the settings tables in db if they aren't there.
func NewDatabaseSettings(ctx context.Context, db runtime.CapabilityProvider) (*DatabaseSettings, *runtime.Fault) {
	for _, stmt := range migrate.SettingsTables {
		if _, f := db.Call(ctx, "execute", []runtime.Value{runtime.Str(stmt)}); f != nil {
			return nil, runtime.Faultf("creating the settings tables: %s", f.Message)
		}
	}
	return &DatabaseSettings{db: db}, nil
}

func (s *DatabaseSettings) Version(ctx context.Context) (int64, *runtime.Fault) {
	v, f := s.db.Call(ctx, "queryOne", []runtime.Value{runtime.Str("SELECT version FROM fiig_settings_version WHERE id = 1")})
	if f != nil {
		return 0, f
	}
	rec, _ := v.(runtime.Record)
	n, _ := rec.Fields["version"].(runtime.Int)
	return int64(n), nil
}

func (s *DatabaseSettings) Load(ctx context.Context) (map[string]map[string]string, int64, *runtime.Fault) {
	// The version first: a save between the two reads makes the next run
	// load again rather than keep a snapshot newer than its number says.
	version, f := s.Version(ctx)
	if f != nil {
		return nil, 0, f
	}
	v, f := s.db.Call(ctx, "queryMany", []runtime.Value{runtime.Str("SELECT package, name, value FROM fiig_settings")})
	if f != nil {
		return nil, 0, f
	}
	rows, _ := v.(runtime.List)
	out := map[string]map[string]string{}
	for _, r := range rows.Elems {
		rec, _ := r.(runtime.Record)
		pkg, _ := rec.Fields["package"].(runtime.Str)
		name, _ := rec.Fields["name"].(runtime.Str)
		value, _ := rec.Fields["value"].(runtime.Str)
		if out[string(pkg)] == nil {
			out[string(pkg)] = map[string]string{}
		}
		out[string(pkg)][string(name)] = string(value)
	}
	return out, version, nil
}

func (s *DatabaseSettings) Save(ctx context.Context, pkg, name, text, by string) *runtime.Fault {
	return s.write(ctx, []runtime.Value{
		runtime.Str("INSERT INTO fiig_settings (package, name, value, updated_at, updated_by) VALUES (?, ?, ?, ?, ?) ON CONFLICT (package, name) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at, updated_by = excluded.updated_by"),
		runtime.Str(pkg), runtime.Str(name), runtime.Str(text), runtime.Str(time.Now().UTC().Format(time.RFC3339)), runtime.Str(by),
	})
}

func (s *DatabaseSettings) Clear(ctx context.Context, pkg, name, _ string) *runtime.Fault {
	return s.write(ctx, []runtime.Value{runtime.Str("DELETE FROM fiig_settings WHERE package = ? AND name = ?"), runtime.Str(pkg), runtime.Str(name)})
}

// write changes a setting and bumps the version in one transaction, so
// every replica's next run sees the change (§3).
func (s *DatabaseSettings) write(ctx context.Context, change []runtime.Value) *runtime.Fault {
	exec := s.db
	var tx runtime.Tx
	if t, ok := s.db.(runtime.TransactionalProvider); ok {
		var f *runtime.Fault
		if tx, f = t.Begin(ctx, ""); f != nil {
			return f
		}
		exec = tx
	}
	fail := func(f *runtime.Fault) *runtime.Fault {
		if tx != nil {
			tx.Rollback()
		}
		return f
	}
	if _, f := exec.Call(ctx, "execute", change); f != nil {
		return fail(f)
	}
	if _, f := exec.Call(ctx, "execute", []runtime.Value{runtime.Str("UPDATE fiig_settings_version SET version = version + 1 WHERE id = 1")}); f != nil {
		return fail(f)
	}
	if tx != nil {
		return tx.Commit()
	}
	return nil
}

// ReadsSettings reports whether any of sources has a field that names a
// setting.
func ReadsSettings(sources []runtime.ConfigSource) bool {
	for _, src := range sources {
		for _, f := range src.Decl.Fields {
			if f.Setting != "" {
				return true
			}
		}
	}
	return false
}

// UseDatabaseSettings gives in a settings store in the application's
// Database when sources read a setting (ADR 0140 §2), before LoadConfig.
// It is a problem to read settings with no Database bound.
func UseDatabaseSettings(ctx context.Context, in *runtime.Interp, caps *runtime.CapabilityRegistry, sources []runtime.ConfigSource) []string {
	if !ReadsSettings(sources) {
		return nil
	}
	db, ok := caps.Lookup(types.DatabaseName)
	if !ok {
		return []string{"config reads a setting (" + firstSetting(sources) + "), which is saved in the application's Database, and none is bound; add one to the capabilities file (ADR 0140 §2)"}
	}
	store, f := NewDatabaseSettings(ctx, db)
	if f != nil {
		return []string{f.Message}
	}
	in.UseSettings(store)
	return nil
}

func firstSetting(sources []runtime.ConfigSource) string {
	for _, src := range sources {
		for _, f := range src.Decl.Fields {
			if f.Setting != "" {
				return src.Where(syntax.ConfigField{Name: f.Name}) + ` reads "` + f.Setting + `"`
			}
		}
	}
	return ""
}
