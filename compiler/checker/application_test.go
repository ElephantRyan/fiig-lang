package checker

import (
	"strings"
	"testing"

	"fiig-lang/compiler/types"
)

// ADR 0132: an application names what each process serves.

func checkApplication(t *testing.T, src string) []Diagnostic {
	t.Helper()
	ratings := buildDep(t, "t.app.ratings", nil, nil,
		"workflow hello() -> string {\n    return \"hi\";\n}",
		"adapter http ratingsApi {\n    GET \"/hello\" -> hello()\n        produces \"application/json\";\n}",
		"adapter http adminApi {\n    GET \"/admin\" -> hello()\n        produces \"application/json\";\n}",
	)
	sync := buildDep(t, "t.app.sync", nil, nil,
		"workflow tick() -> string {\n    return \"tick\";\n}",
		"adapter startup syncStartup {\n    run -> tick() {\n        _ -> proceed,\n    }\n}",
	)
	packages := types.Packages{"t.app.ratings": ratings, "t.app.sync": sync}
	diags, _ := CheckSourcesWithSiblings(map[string]string{"src/MyApp.fiig": src}, packages, packages, "t.app", nil)
	return diags
}

func applicationSource(body string) string {
	return "use t.app.ratings\nuse t.app.sync\n\napplication MyApp {\n" + body + "\n}\n"
}

func TestAnApplicationThatServesEveryAdapterChecksClean(t *testing.T) {
	good := applicationSource(`    serve web {
        ratings.ratingsApi
        ratings.adminApi
    }
    serve worker {
        sync.syncStartup
    }`)
	if diags := checkApplication(t, good); len(diags) != 0 {
		t.Fatalf("diags = %v", diags)
	}
	one := applicationSource(`    serve {
        ratings.ratingsApi, ratings.adminApi, sync.syncStartup
    }`)
	if diags := checkApplication(t, one); len(diags) != 0 {
		t.Fatalf("one unnamed block, adapters on one line: %v", diags)
	}
}

func TestApplicationMistakes(t *testing.T) {
	cases := []struct{ name, body, rule, fragment string }{
		{"an adapter nothing serves", "    serve {\n        ratings.ratingsApi\n        sync.syncStartup\n    }", "O2", "adapter adminApi (t.app.ratings) is named by no serve block"},
		{"no such adapter", "    serve {\n        ratings.nothing\n        ratings.ratingsApi\n        ratings.adminApi\n        sync.syncStartup\n    }", "O1", "package t.app.ratings has no adapter named nothing"},
		{"not imported", "    serve {\n        billing.api\n        ratings.ratingsApi\n        ratings.adminApi\n        sync.syncStartup\n    }", "O1", "billing isn't an imported package"},
		{"a workflow is not an adapter", "    serve {\n        ratings.hello\n        ratings.ratingsApi\n        ratings.adminApi\n        sync.syncStartup\n    }", "O1", "hello in package t.app.ratings is a workflow, not an adapter"},
		{"a bare name that isn't here", "    serve {\n        ratingsApi\n        ratings.ratingsApi\n        ratings.adminApi\n        sync.syncStartup\n    }", "O1", "no adapter named ratingsApi in this package"},
		{"too long a path", "    serve {\n        t.app.ratings.ratingsApi\n    }", "O1", "write an adapter as alias.adapter"},
		{"nothing at all", "", "O1", "serves nothing"},
		{"an empty block", "    serve web {\n    }", "O1", "names no adapter"},
		{"a role twice", "    serve web {\n        ratings.ratingsApi\n        ratings.adminApi\n    }\n    serve web {\n        sync.syncStartup\n    }", "O4", `two serve blocks for role "web"`},
		{"an adapter twice", "    serve {\n        ratings.ratingsApi\n        ratings.ratingsApi\n        ratings.adminApi\n        sync.syncStartup\n    }", "O4", "is named twice in this serve block"},
		{"a block with no role beside another", "    serve {\n        ratings.ratingsApi\n        ratings.adminApi\n    }\n    serve worker {\n        sync.syncStartup\n    }", "O4", "mixes a serve block with no role and others"},
	}
	for _, c := range cases {
		src := applicationSource(c.body)
		if c.body == "" {
			src = "use t.app.ratings;\n\napplication MyApp {\n}\n"
		}
		diags := checkApplication(t, src)
		found := false
		for _, d := range diags {
			found = found || d.Rule == c.rule && strings.Contains(d.Msg, c.fragment)
		}
		if !found {
			t.Errorf("%s: diags = %v, want %s containing %q", c.name, diags, c.rule, c.fragment)
		}
	}
}
