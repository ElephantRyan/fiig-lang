package checker

import (
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// copy(...) of another package's model resolves the overridden field's
// type in that package, as reading the field does (resolveDeclType):
// fiig.postgres copying fiig.sql's SqlConnection with statements:
// List<SqlStatement> found no SqlStatement in its own names.
func TestCopyOfAnotherPackagesModelResolvesItsFieldThere(t *testing.T) {
	var files []*syntax.File
	for _, src := range []string{
		"model Item {\n    name: string\n}\n",
		"model Shelf {\n    items: List<Item>\n    count: int\n}\n",
	} {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatalf("parse: %v", errs)
		}
		files = append(files, f)
	}
	lib, diags := BuildPackage(files, nil)
	if len(diags) > 0 {
		t.Fatalf("lib: %+v", diags)
	}
	sources := map[string]string{
		"src/Restock.fiig": "use t.lib as lib\n\nconversion restock(shelf: lib.Shelf, item: lib.Item) -> lib.Shelf {\n    return copy(shelf, items: append(shelf.items, item), count: shelf.count + 1);\n}\n",
	}
	diags, _ = CheckSources(sources, types.Packages{"t.lib": lib}, "t.app", nil)
	if len(diags) > 0 {
		t.Fatalf("copy of another package's model: %+v", diags)
	}
}
