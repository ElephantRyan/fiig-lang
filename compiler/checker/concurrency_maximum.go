package checker

import (
	"fmt"

	"fiig-lang/compiler/syntax"
)

// ApplicationMaximum is D3 (ADR 0149 §3): a literal `limit:` above the
// application maximum, in the application's own file, says something the
// manifest makes false. A dependency's blocks are clamped at run time
// instead, so the loader runs this only over the root module's files. A
// limit that isn't a literal is clamped wherever it is, as D2 compares
// only literals.
func ApplicationMaximum(file *syntax.File, maximum int) []Diagnostic {
	var diags []Diagnostic
	visit := func(x syntax.Expr) {
		e, ok := x.(*syntax.ConcurrentExpr)
		if !ok || e.Limit == nil || e.Limit.Value == nil {
			return
		}
		if n, ok := limitAsInt(e.Limit.Value); ok && n > maximum {
			diags = append(diags, Diagnostic{Rule: "D3", At: e.Start,
				Msg: fmt.Sprintf("concurrent limit %d exceeds the application maximum of %d (fiig.pkg.json concurrency.maximum); lower the limit or raise the maximum", n, maximum)})
		}
	}
	decls := file.Decls
	if file.Private != nil {
		decls = append(append([]syntax.Decl{}, decls...), file.Private.Members...)
	}
	// Each body by itself, not syntax.Inspect: Inspect follows the checker's
	// pointers to other declarations, which may be a dependency's.
	for _, d := range decls {
		if body := callableBody(d); body != nil {
			syntax.WalkBlockExprs(body, visit)
		}
	}
	return diags
}
