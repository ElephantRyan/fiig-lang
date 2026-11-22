package checker

import (
	"testing"

	"fiig-lang/compiler/format"
	"fiig-lang/compiler/parser"
)

// ADR 0140: config fields that read a setting saved in the app.

const settingsConfig = `wire Abs {
    url: string | None from env "ABS_URL" ?? setting "abs.url"
    workers: int from setting "abs.workers" on restart ?? env "WORKERS" = 4
}`

const saveUrl = `gateway saveUrl(url: string) -> Result<None, SettingError>
    requires Settings
{
    return Settings.save("abs.url", url, by: "admin")
}`

func TestSettingFieldsCheck(t *testing.T) {
	if diags := checkSrc(t, settingsConfig, saveUrl); len(diags) > 0 {
		t.Fatalf("a soft and a hard setting, and a save: %v", diags)
	}
	config := func(field string) []Diagnostic {
		return checkSrc(t, "wire Other {\n    "+field+"\n}")
	}
	cases := []struct{ field, fragment string }{
		{`port: int from setting "server.port"`, "is `| None` or has a default"},
		{`port: int | None from setting "9port"`, "starting with a letter"},
		{`port: int | None from setting "server port"`, "letters, digits"},
	}
	for _, c := range cases {
		wantDiag(t, config(c.field), "V1", c.fragment)
	}
	twice := checkSrc(t, settingsConfig, "wire Other {\n    again: string | None from setting \"abs.url\"\n}")
	if len(twice) != 2 || twice[0].Rule != "V1" || twice[1].Rule != "V1" {
		t.Fatalf("one setting read by two fields: %v", twice)
	}
}

func TestASettingsCallNamesADeclaredSetting(t *testing.T) {
	typo := `gateway saveUrl(url: string) -> Result<None, SettingError>
    requires Settings
{
    return Settings.save("abs.ulr", url, by: "admin")
}`
	wantDiag(t, checkSrc(t, settingsConfig, typo), "V1", `names setting "abs.ulr", which no config field of this package reads`)
	computed := `gateway saveAny(name: string, url: string) -> Result<None, SettingError>
    requires Settings
{
    return Settings.save(name, url, by: "admin")
}`
	wantDiag(t, checkSrc(t, settingsConfig, computed), "V1", "with a string literal")
	activity := `activity saveUrl(url: string) -> Result<None, SettingError> {
    return Settings.save("abs.url", url, by: "admin")
}`
	wantDiag(t, checkSrc(t, settingsConfig, activity), "B10", "Settings")
}

func TestAWhenReadsOnlyHardSettings(t *testing.T) {
	soft := "wire Mirror when Abs.url != \"\" {\n    on: bool from env \"MIRROR\" = false\n}"
	wantDiag(t, checkSrc(t, settingsConfig, soft), "V2", "a setting that changes while the process runs")
	hard := "wire Pool when Abs.workers != 0 {\n    size: int from env \"POOL\" = 1\n}"
	if diags := checkSrc(t, settingsConfig, hard); len(diags) > 0 {
		t.Fatalf("a when reading a hard setting: %v", diags)
	}
}

func TestSettingSourcesFormatAsWritten(t *testing.T) {
	got, errs := format.Format(settingsConfig + "\n")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if got != settingsConfig+"\n" {
		t.Fatalf("formatted:\n%s", got)
	}
	_, errs = parser.Parse("wire X {\n    a: string | None from env \"A\" ?? env \"B\"\n}")
	if len(errs) == 0 {
		t.Fatal("two env sources parsed")
	}
}
