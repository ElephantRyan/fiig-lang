package adapter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// HTTPHandler builds an http.Handler serving every route in d — one
// `adapter http { ... }` declaration, fully wired to in. Go 1.22+'s
// ServeMux already uses `{name}` for path segments, identical to this
// grammar's own placeholder syntax (grammar.md's own note: chosen to
// match OpenAPI's path-templating ahead of Milestone 5), so a route's path
// string needs no translation at all — it's passed straight through as
// the mux pattern.
func HTTPHandler(d *syntax.AdapterDecl, u *types.Universe, in *runtime.Interp) (http.Handler, error) {
	if d.Kind != syntax.AdapterHTTP {
		return nil, fmt.Errorf("adapter %q is not an http adapter", d.Name)
	}
	mux := http.NewServeMux()
	for _, route := range d.Routes {
		route := route
		plan, err := planRoute(route, true, u)
		if err != nil {
			return nil, fmt.Errorf("route %s %q: %w", route.Verb, route.Path, err)
		}
		pathPart, _, _ := strings.Cut(route.Path, "?")
		pattern := route.Verb + " " + pathPart
		mux.HandleFunc(pattern, httpRouteHandler(route, plan, u, in))
	}
	return mux, nil
}

func httpRouteHandler(route *syntax.Route, plan routePlan, u *types.Universe, in *runtime.Interp) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pathVal := func(name string) (string, bool) {
			v := r.PathValue(name)
			return v, v != ""
		}
		queryVal := func(name string) (string, bool) {
			q := r.URL.Query()
			if !q.Has(name) {
				return "", false
			}
			return q.Get(name), true
		}
		var bodyJSON any
		if plan.bodyArg != "" {
			parsed, err := runtime.ParseJSONFrom(r.Body)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("invalid request body: %v", err)})
				return
			}
			bodyJSON = parsed
		}

		args, err := buildArgs(route, plan, pathVal, queryVal, bodyJSON, u)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		resp, fault := callRoute(r.Context(), in, route, args)
		if fault != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"fault": fault.String()})
			return
		}
		writeJSON(w, resp.Status, runtime.EncodeBodyJSON(resp.Body))
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
