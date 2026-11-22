package adapter

import (
	"encoding/json"
	"strings"
	"testing"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0081 §3: Shade | string, served. A spelling is the enum wherever
// string is written in the union, the text arm takes only text whatever
// the arms' order, and OpenAPI says anyOf.
var shadeProgram = []string{
	"enum Shade {\n    Light from json \"light\",\n    Dark from json \"dark\",\n}",
	`model Paint {
    shade: string | Shade;
}`,
	`workflow describe(paint: Paint) -> string {
    return match (paint.shade) {
        text -> {
            return "text ${text}";
        },
        Shade.Light -> {
            return "the light shade";
        },
        Shade.Dark -> {
            return "the dark shade";
        },
    }
}`,
	`adapter http shades {
    POST "/describe" -> describe(paint: Paint)
        consumes "application/json"
        produces "application/json";
}`,
}

func TestEnumBesideStringServed(t *testing.T) {
	var files []*syntax.File
	var ad *syntax.AdapterDecl
	for _, src := range shadeProgram {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		files = append(files, f)
		if a, ok := f.Decls[0].(*syntax.AdapterDecl); ok {
			ad = a
		}
	}
	if diags := checker.Check(files, nil); len(diags) > 0 {
		t.Fatalf("checker: %+v", diags)
	}
	u, _ := types.BuildUniverse(files)
	in := runtime.New(u, runtime.NewCapabilityRegistry())
	for shade, want := range map[string]string{`"dark"`: `"the dark shade"`, `"light"`: `"the light shade"`, `"Dark"`: `"text Dark"`, `"teal"`: `"text teal"`} {
		args := map[string]runtime.Value{}
		raw, _ := runtime.ReadJSON([]byte(`{"shade": ` + shade + `}`))
		v, err := in.DecodeBodyJSON(raw, types.Named{Name: "Paint", Decl: u.Decls["Paint"]})
		if err != nil {
			t.Fatal(err)
		}
		args["paint"] = v
		got, fault := in.Call(t.Context(), "describe", args)
		if fault != nil {
			t.Fatal(fault)
		}
		if out, _ := runtime.EncodeBodyJSON(got); string(out) != want {
			t.Errorf("%s: got %s, want %s", shade, out, want)
		}
	}
	doc, err := GenerateOpenAPI(ad, u)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(doc)
	if !strings.Contains(string(body), `"anyOf"`) || strings.Contains(string(body), `"oneOf"`) {
		t.Errorf("string | Shade should be anyOf: %s", body)
	}
}
