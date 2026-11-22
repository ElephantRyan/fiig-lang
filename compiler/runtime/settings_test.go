package runtime

import (
	"context"
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0140: config fields that read a setting saved in the app.

var settingSources = []string{`wire Abs {
    url: string | None from env "ABS_URL" ?? setting "abs.url"
    name: string from setting "abs.name" ?? env "ABS_NAME" = "Hearsay"
    workers: int from setting "abs.workers" on restart = 4
}`, `gateway save(name: string, text: string) -> Result<None, SettingError>
    requires Settings
{
    return Settings.save(name, text, by: "admin");
}`, `gateway source(name: string) -> SettingSource
    requires Settings
{
    return Settings.source(name);
}`, `activity show() -> string {
    return "${Abs.url ?? "-"} ${Abs.name} ${Abs.workers}";
}`, `activity saveThenShow(url: string) -> string {
    before = show();
    saved = save("abs.url", url);
    return "${before} | ${show()}";
}`}

func settingsInterp(t *testing.T, store SettingsStore, env map[string]string) *Interp {
	t.Helper()
	u := universeFromSource(t, settingSources...)
	d, _ := u.Lookup("Abs")
	in := New(u, NewCapabilityRegistry())
	in.UseSettings(store)
	if problems := in.LoadConfig([]ConfigSource{{Decl: d.(*syntax.ConfigDecl), Universe: u}}, func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}); problems != nil {
		t.Fatal(problems)
	}
	return in
}

func callShow(t *testing.T, in *Interp, name string, args map[string]Value) string {
	t.Helper()
	v, f := in.Call(context.Background(), name, args)
	if f != nil {
		t.Fatalf("%s: %v", name, f)
	}
	return v.String()
}

func TestSoftSettingsAreLiveAndHardOnesWaitForARestart(t *testing.T) {
	store := NewMemorySettings()
	in := settingsInterp(t, store, nil)
	if got := callShow(t, in, "show", nil); got != "- Hearsay 4" {
		t.Fatalf("nothing saved: %s", got)
	}
	for _, s := range [][2]string{{"abs.url", "https://abs.test"}, {"abs.name", "Shelf"}, {"abs.workers", "8"}} {
		if got := callShow(t, in, "save", map[string]Value{"name": Str(s[0]), "text": Str(s[1])}); got != "Ok(None)" {
			t.Fatalf("save %s: %s", s[0], got)
		}
	}
	if got := callShow(t, in, "show", nil); got != "https://abs.test Shelf 4" {
		t.Fatalf("after saving: %s; want the soft ones live and workers still 4", got)
	}
	if got := callShow(t, in, "source", map[string]Value{"name": Str("abs.workers")}); got != "SettingSource.Pending" {
		t.Fatalf("a hard setting saved since the start: %s", got)
	}
	restarted := settingsInterp(t, store, nil)
	if got := callShow(t, restarted, "show", nil); got != "https://abs.test Shelf 8" {
		t.Fatalf("after a restart: %s", got)
	}
	if got := callShow(t, restarted, "source", map[string]Value{"name": Str("abs.workers")}); got != "SettingSource.Saved" {
		t.Fatalf("after a restart: %s", got)
	}
	if got := callShow(t, in, "save", map[string]Value{"name": Str("abs.workers"), "text": Str("eight")}); got != "Err(SettingError.Invalid(not an int))" {
		t.Fatalf("a save that doesn't parse: %s", got)
	}
}

// §3: a run reads one snapshot, so a save in the middle of it shows from
// the next run on.
func TestARunReadsOneSnapshot(t *testing.T) {
	in := settingsInterp(t, NewMemorySettings(), nil)
	if got := callShow(t, in, "saveThenShow", map[string]Value{"url": Str("https://a.test")}); got != "- Hearsay 4 | - Hearsay 4" {
		t.Fatalf("within the run: %s", got)
	}
	if got := callShow(t, in, "show", nil); got != "https://a.test Hearsay 4" {
		t.Fatalf("the next run: %s", got)
	}
}

// §1: sources are tried in the order written. Env first fixes the value
// and refuses a save; setting first lets a save win over env.
func TestTheOrderOfSourcesDecidesWhoWins(t *testing.T) {
	store := NewMemorySettings()
	in := settingsInterp(t, store, map[string]string{"ABS_URL": "https://env.test", "ABS_NAME": "FromEnv"})
	if got := callShow(t, in, "save", map[string]Value{"name": Str("abs.url"), "text": Str("https://x.test")}); got != "Err(SettingError.SetByDeployment)" {
		t.Fatalf("saving over env: %s", got)
	}
	if got := callShow(t, in, "source", map[string]Value{"name": Str("abs.url")}); got != "SettingSource.Deployment" {
		t.Fatalf("source: %s", got)
	}
	if got := callShow(t, in, "save", map[string]Value{"name": Str("abs.name"), "text": Str("Saved")}); got != "Ok(None)" {
		t.Fatalf("a setting written first: %s", got)
	}
	if got := callShow(t, in, "show", nil); got != "https://env.test Saved 4" {
		t.Fatalf("show: %s", got)
	}
}
