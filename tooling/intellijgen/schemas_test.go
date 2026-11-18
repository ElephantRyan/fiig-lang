package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ADR 0090 §6: the JSON schemas the IntelliJ plugin registers accept every
// manifest and capabilities file the repository has, and refuse what the
// toolchain refuses, so they can't fall behind it.

// validate checks v against the JSON Schema subset the schemas use.
func validate(root, s map[string]any, v any, path string) []string {
	if ref, ok := s["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/definitions/")
		return validate(root, root["definitions"].(map[string]any)[name].(map[string]any), v, path)
	}
	var errs []string
	fail := func(format string, args ...any) { errs = append(errs, path+": "+fmt.Sprintf(format, args...)) }
	if t, ok := s["type"].(string); ok && !isType(v, t) {
		fail("want %s, got %T", t, v)
		return errs
	}
	if c, ok := s["const"]; ok && v != c {
		fail("want %v", c)
	}
	if e, ok := s["enum"].([]any); ok {
		found := false
		for _, x := range e {
			found = found || x == v
		}
		if !found {
			fail("%v is not one of %v", v, e)
		}
	}
	if p, ok := s["pattern"].(string); ok {
		if str, isStr := v.(string); isStr && !regexp.MustCompile(p).MatchString(str) {
			fail("%q doesn't match %s", str, p)
		}
	}
	if m, ok := s["minimum"].(float64); ok {
		if n, isNum := v.(float64); isNum && n < m {
			fail("%v is below %v", n, m)
		}
	}
	if obj, isObj := v.(map[string]any); isObj {
		for _, r := range anySlice(s["required"]) {
			if _, ok := obj[r.(string)]; !ok {
				fail("missing %q", r)
			}
		}
		props, _ := s["properties"].(map[string]any)
		for k, x := range obj {
			if ps, ok := props[k].(map[string]any); ok {
				errs = append(errs, validate(root, ps, x, path+"."+k)...)
				continue
			}
			switch ap := s["additionalProperties"].(type) {
			case bool:
				if !ap {
					fail("unexpected %q", k)
				}
			case map[string]any:
				errs = append(errs, validate(root, ap, x, path+"."+k)...)
			}
		}
	}
	if items, ok := s["items"].(map[string]any); ok {
		for i, x := range anySlice(v) {
			errs = append(errs, validate(root, items, x, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	if alts, ok := s["anyOf"].([]any); ok {
		matched := false
		for _, alt := range alts {
			if len(validate(root, alt.(map[string]any), v, path)) == 0 {
				matched = true
				break
			}
		}
		if !matched {
			fail("matches none of its forms")
		}
	}
	if not, ok := s["not"].(map[string]any); ok && len(validate(root, not, v, path)) == 0 {
		fail("matches a form it may not")
	}
	return errs
}

func anySlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func isType(v any, t string) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "integer":
		n, ok := v.(float64)
		return ok && n == math.Trunc(n)
	case "number":
		_, ok := v.(float64)
		return ok
	}
	return true
}

func loadJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return v
}

func schema(t *testing.T, name string) map[string]any {
	return loadJSON(t, filepath.Join("..", "..", "docs", "schemas", name))
}

func TestSchemasAcceptTheRepository(t *testing.T) {
	pkg, caps := schema(t, "fiig.pkg.schema.json"), schema(t, "capabilities.schema.json")
	manifests, capsFiles := 0, 0
	for _, dir := range []string{"examples", filepath.Join("compiler", "stdlib")} {
		filepath.WalkDir(filepath.Join("..", "..", dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			name := d.Name()
			switch {
			case name == "fiig.pkg.json":
				manifests++
				for _, e := range validate(pkg, pkg, loadJSON(t, path), "") {
					t.Errorf("%s: %s", path, e)
				}
			case capsFileName(name):
				capsFiles++
				for _, e := range validate(caps, caps, loadJSON(t, path), "") {
					t.Errorf("%s: %s", path, e)
				}
			}
			return nil
		})
	}
	if manifests < 15 || capsFiles < 5 {
		t.Fatalf("checked %d manifests and %d capabilities files; the walk found too few", manifests, capsFiles)
	}
}

// capsFileName is the plugin's rule for which JSON files are capabilities
// files (ADR 0090 §6), kept in step with FiigJsonSchemas.kt.
func capsFileName(name string) bool {
	return regexp.MustCompile(`^(caps|capabilities)(\.[^.]+)*\.json$`).MatchString(name)
}

func TestSchemasRefuseWhatTheToolchainRefuses(t *testing.T) {
	pkg, caps := schema(t, "fiig.pkg.schema.json"), schema(t, "capabilities.schema.json")
	bad := []struct {
		schema map[string]any
		doc    string
	}{
		{pkg, `{"path": "a.b", "kind": "service", "version": "1.0.0"}`},
		{pkg, `{"path": "a.b", "kind": "library"}`},
		{pkg, `{"path": "a.b", "kind": "library", "version": "1.0"}`},
		{pkg, `{"path": "a.b", "kind": "library", "version": "1.0.0", "dependancies": {}}`},
		{caps, `{"Database": {"type": "sql", "driver": "sqlite"}}`},
		{caps, `{"Database": {"type": "driver", "driver": "fiig.postgres", "maxConnections": 0}}`},
		{caps, `{"Socket": {"type": "socket", "trust": "one.pem"}}`},
		{caps, `{"Socket": {"type": "socket", "clientCertificates": [{"hosts": ["db"], "certificate": "c.pem"}]}}`},
		{caps, `{"Random": {"type": "random-seeded"}}`},
		{caps, `{"Database": {"type": "mysql"}}`},
	}
	for _, tc := range bad {
		var doc any
		if err := json.Unmarshal([]byte(tc.doc), &doc); err != nil {
			t.Fatal(err)
		}
		if errs := validate(tc.schema, tc.schema, doc, ""); len(errs) == 0 {
			t.Errorf("schema accepted %s", tc.doc)
		}
	}
	good := `{"Database": {"type": "driver", "driver": "fiig.postgres", "settings": {"host": "${PGHOST}"}, "maxConnections": 4},
	          "Socket": {"type": "socket", "trust": ["/etc/ssl/rds.pem"], "systemRoots": false,
	                     "clientCertificates": [{"hosts": ["db.internal"], "certificate": "c.pem", "key": "k.pem"}]},
	          "Random": {"type": "random-seeded", "seed": 7},
	          "EmailSender": {"send": [{"ok": true}]}}`
	var doc any
	json.Unmarshal([]byte(good), &doc)
	if errs := validate(caps, caps, doc, ""); len(errs) > 0 {
		t.Errorf("schema refused a valid file: %v", errs)
	}
	if !capsFileName("caps.postgres.json") || !capsFileName("capabilities.json") || capsFileName("package.json") {
		t.Error("capsFileName")
	}
}
