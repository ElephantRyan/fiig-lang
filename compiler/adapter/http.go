package adapter

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"fiig-lang/compiler/httpspec"
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
		req := request{
			path: func(name string) (string, bool) {
				v := r.PathValue(name)
				return v, v != ""
			},
			query: func(name string) (string, bool) {
				q := r.URL.Query()
				if !q.Has(name) {
					return "", false
				}
				return q.Get(name), true
			},
			// A header sent more than once is its values joined by ", "
			// (RFC 9110 §5.3); names match case-insensitively.
			header: func(name string) (string, bool) {
				vals := r.Header.Values(name)
				if len(vals) == 0 {
					return "", false
				}
				return strings.Join(vals, ", "), true
			},
			// A cookie sent more than once is read as the first, the most
			// specific path (RFC 6265 §5.4); names match exactly.
			cookie: func(name string) (string, bool) {
				c, err := r.Cookie(name)
				if err != nil {
					return "", false
				}
				return c.Value, true
			},
		}
		if plan.bodyArg != "" {
			consumes := "application/json"
			if route.Consumes != nil {
				consumes = route.Consumes.Text
			}
			if got := r.Header.Get("Content-Type"); !httpspec.SameType(got, consumes) {
				writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{"error": fmt.Sprintf("unsupported media type %q; this route consumes %q", got, consumes)})
				return
			}
			req.bodyIsJSON = httpspec.IsJSON(consumes)
			if req.bodyIsJSON {
				parsed, err := runtime.ParseJSONFrom(r.Body)
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("invalid request body: %v", err)})
					return
				}
				req.bodyJSON = parsed
			} else {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("invalid request body: %v", err)})
					return
				}
				req.bodyRaw = raw
			}
		}

		args, err := buildArgs(route, plan, req, u)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		resp, fault := callRoute(r.Context(), in, route, args)
		if fault != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"fault": fault.String()})
			return
		}
		produces := "application/json"
		if route.Produces != nil {
			produces = route.Produces.Text
		}
		if fault := writeResponse(w, resp, produces); fault != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"fault": fault.String()})
		}
	}
}

// writeResponse writes a program's response with the route's declared
// content type (ADR 0069 §5): JSON for a JSON type, and the text or bytes
// as they are for any other. A response with no body has no Content-Type.
// It returns a fault, having written nothing, when the response can't be
// sent (§6).
func writeResponse(w http.ResponseWriter, resp Response, produces string) *runtime.Fault {
	if fault := checkWire(resp); fault != nil {
		return fault
	}
	var body []byte
	contentType := ""
	if _, none := resp.Body.(runtime.None); !none && resp.Body != nil {
		contentType = produces
		if httpspec.IsJSON(produces) {
			encoded, err := json.Marshal(runtime.EncodeBodyJSON(resp.Body))
			if err != nil {
				return runtime.Faultf("response body does not encode as JSON: %v", err)
			}
			body = append(encoded, '\n')
		} else {
			switch b := resp.Body.(type) {
			case runtime.Str:
				body = []byte(b)
				if httpspec.IsText(produces) {
					contentType += "; charset=utf-8"
				}
			case runtime.Bytes:
				body = b.B
			default:
				return runtime.Faultf("route produces %q, which sends a string or bytes body, but the response body is %s", produces, resp.Body)
			}
		}
	}
	writeHeaders(w.Header(), resp)
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(resp.Status)
	_, _ = w.Write(body)
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
