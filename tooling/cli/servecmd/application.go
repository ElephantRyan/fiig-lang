package servecmd

import (
	"fmt"
	"strings"

	"fiig-lang/compiler/syntax"
)

// ADR 0132: which adapters `fiig serve` runs. The program says so in its
// `application` declaration; the command line can narrow it to one role, or
// name adapters outright, as while developing.

// serveNames resolves the adapter names for this run:
//
//   - two or more names, or one that is no role: as given, whatever the
//     application says;
//   - one name that is a role of the application: that block's adapters;
//   - no names: the application's one serve block, or an error that says to
//     name a role when there are several, or that there is no application.
//
// defaulted is true when the names came from the application and not from
// the command line.
func serveNames(args []string, files []*syntax.File) (names []string, defaulted bool, err error) {
	app, file := findApplication(files)
	if len(args) == 1 && app != nil {
		for _, b := range app.Serves {
			if b.Role != "" && b.Role == args[0] {
				return blockNames(b, file), true, nil
			}
		}
	}
	if len(args) > 0 {
		return args, false, nil
	}
	if app == nil {
		return nil, true, fmt.Errorf("the program has no application declaration, so there is nothing to serve; declare one, as in\n\n    application MyApp {\n        serve {\n            sessions.sessionsApi\n        }\n    }\n\nor name the adapters on the command line (ADR 0132)")
	}
	switch len(app.Serves) {
	case 0:
		return nil, true, fmt.Errorf("application %s serves nothing", app.Name)
	case 1:
		return blockNames(app.Serves[0], file), true, nil
	}
	var roles []string
	for _, b := range app.Serves {
		roles = append(roles, b.Role)
	}
	return nil, true, fmt.Errorf("application %s runs as %s; name the one to run, as in fiig serve . %s", app.Name, strings.Join(roles, " or "), roles[0])
}

// findApplication is the application a program declares and the file it is
// in, nil when there is none.
func findApplication(files []*syntax.File) (*syntax.ApplicationDecl, *syntax.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			if app, ok := d.(*syntax.ApplicationDecl); ok {
				return app, f
			}
		}
	}
	return nil, nil
}

// blockNames are b's adapters as `fiig serve` names them: a bare adapter as
// it is, and `alias.adapter` as the imported package's path and the adapter.
func blockNames(b *syntax.ServeBlock, file *syntax.File) []string {
	var out []string
	for _, r := range b.Adapters {
		name := r.Path[len(r.Path)-1]
		if len(r.Path) == 2 {
			for _, imp := range file.Imports {
				alias := imp.Alias
				if alias == "" {
					alias = imp.Path[len(imp.Path)-1]
				}
				if alias == r.Path[0] {
					name = strings.Join(imp.Path, ".") + "." + name
				}
			}
		}
		out = append(out, name)
	}
	return out
}
