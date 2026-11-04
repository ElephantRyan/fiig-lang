package adapter

import (
	"context"
	"fmt"
	"strings"

	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// Dispatch matches args (a command line already split into words, e.g.
// `[]string{"customer", "get", "c1"}` for `customer get {id}`) against
// every route in d and runs the first one that matches. Unlike HTTP
// routes, CLI routes have no query region or body (grammar.md §... "every
// CLI route have no body — every argument must be claimed by a
// placeholder"), so matching is pure positional word-matching against the
// route's command pattern.
func Dispatch(ctx context.Context, d *syntax.AdapterDecl, u *types.Universe, in *runtime.Interp, args []string) (Response, error) {
	if d.Kind != syntax.AdapterCLI {
		return Response{}, fmt.Errorf("adapter %q is not a cli adapter", d.Name)
	}
	for _, route := range d.Routes {
		segments := strings.Fields(route.Path)
		captured, ok := matchCommand(segments, args)
		if !ok {
			continue
		}
		plan, err := planRoute(route, false, u)
		if err != nil {
			return Response{}, err
		}
		pathVal := func(name string) (string, bool) {
			v, ok := captured[name]
			return v, ok
		}
		req := request{path: pathVal, query: noValues, header: noValues, cookie: noValues}
		routeArgs, err := buildArgs(route, plan, req, u)
		if err != nil {
			return Response{}, err
		}
		resp, fault := callRoute(ctx, in, route, routeArgs)
		if fault != nil {
			return Response{}, fmt.Errorf("%s", fault)
		}
		return resp, nil
	}
	return Response{}, fmt.Errorf("no command matched: %s", strings.Join(args, " "))
}

// matchCommand matches args positionally against pattern (a route's
// command path split into words): a literal word must match exactly, a
// `{name}` word captures whatever word is in that position.
func matchCommand(pattern, args []string) (map[string]string, bool) {
	if len(pattern) != len(args) {
		return nil, false
	}
	captured := map[string]string{}
	for i, seg := range pattern {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			captured[seg[1:len(seg)-1]] = args[i]
			continue
		}
		if seg != args[i] {
			return nil, false
		}
	}
	return captured, true
}
