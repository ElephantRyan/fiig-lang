package adapter

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"fiig-lang/compiler/httpspec"
	"fiig-lang/compiler/syntax"
)

// CORS is the origins a deployment lets call an http adapter from a
// browser on another origin (ADR 0070 §1). A nil *CORS is CORS off.
type CORS struct {
	any     bool            // `*`: every origin, never credentials (§3)
	origins map[string]bool // exact, lowercased `scheme://host[:port]`
}

// Origins lists the allowed origins, sorted, or `*`.
func (c *CORS) Origins() []string {
	if c.any {
		return []string{"*"}
	}
	var list []string
	for o := range c.origins {
		list = append(list, o)
	}
	sort.Strings(list)
	return list
}

// preflightMaxAge is how long a browser may reuse a preflight answer
// (§6): long enough to save round trips, short enough that removing an
// origin stops its writes within half an hour.
const preflightMaxAge = "1800"

// safelistedResponseHeaders are the response headers every script may
// read already, so §4 leaves them out of Access-Control-Expose-Headers.
var safelistedResponseHeaders = map[string]bool{
	"cache-control": true, "content-language": true, "content-length": true,
	"content-type": true, "expires": true, "last-modified": true, "pragma": true,
}

// ParseOrigins reads the -allow-origin values (§1). It returns nil, CORS
// off, when there are none. Each must be exactly the form a browser sends
// in Origin; `null` and pattern matching are refused, and `*` must stand
// alone.
func ParseOrigins(values []string) (*CORS, error) {
	var list []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			list = append(list, v)
		}
	}
	if len(list) == 0 {
		return nil, nil
	}
	c := &CORS{origins: map[string]bool{}}
	for _, o := range list {
		switch {
		case o == "*":
			if len(list) > 1 {
				return nil, fmt.Errorf("allowed origin * must be the only one, since it already allows every origin")
			}
			c.any = true
			continue
		case strings.EqualFold(o, "null"):
			return nil, fmt.Errorf("allowed origin %q is refused: sandboxed iframes and file: pages send it, so it would trust anything", o)
		case strings.Contains(o, "*"):
			return nil, fmt.Errorf("allowed origin %q has a wildcard; list each origin exactly", o)
		}
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
			u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(o, "?") || strings.HasSuffix(o, "#") {
			return nil, fmt.Errorf("allowed origin %q isn't an origin: write scheme://host or scheme://host:port, with http or https and no path or trailing slash", o)
		}
		if port := u.Port(); (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
			return nil, fmt.Errorf("allowed origin %q names its scheme's default port, which browsers leave out of Origin; write %s://%s", o, u.Scheme, u.Hostname())
		}
		c.origins[strings.ToLower(o)] = true
	}
	return c, nil
}

// WithCORS wraps the handler HTTPHandler built for d with c's policy, or
// returns it unchanged when c is nil. It refuses `*` for an adapter whose
// routes read a cookie (§3), since a browser never sends one to it.
func WithCORS(h http.Handler, d *syntax.AdapterDecl, c *CORS) (http.Handler, error) {
	return WithCORSFor(h, []Served{{Decl: d}}, c)
}

// WithCORSFor is WithCORS for every adapter a listener serves (ADR 0079
// §6): one policy, a preflight answering with the methods of all of
// them, and the cookie refusal checked per adapter.
func WithCORSFor(h http.Handler, adapters []Served, c *CORS) (http.Handler, error) {
	if c == nil {
		return h, nil
	}
	mux, ok := h.(*http.ServeMux)
	if !ok {
		return nil, fmt.Errorf("CORS wraps the handler HTTPHandler built")
	}
	var verbs []string
	seen := map[string]bool{}
	for _, served := range adapters {
		for _, route := range served.Decl.Routes {
			if c.any {
				for _, a := range route.Args {
					if a.Source == "cookie" {
						return nil, fmt.Errorf("allowed origin * can't serve adapter %q: route %s %q reads cookie %q, and browsers never send cookies to a * policy; list the origins instead", served.Name(), route.Verb, route.Path, a.SourceName)
					}
				}
			}
			if !seen[route.Verb] {
				seen[route.Verb] = true
				verbs = append(verbs, route.Verb)
			}
		}
	}
	return &corsHandler{mux: mux, policy: c, verbs: verbs}, nil
}

type corsHandler struct {
	mux    *http.ServeMux
	policy *CORS
	verbs  []string // every verb the adapter's routes use, in route order
}

// allowed returns the Access-Control-Allow-Origin value for a request's
// Origin, or "" when it isn't allowed.
func (c *CORS) allowed(origin string) string {
	switch {
	case origin == "":
		return ""
	case c.any:
		return "*"
	case c.origins[strings.ToLower(origin)]:
		return origin
	}
	return ""
}

func (h *corsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if r.Method == http.MethodOptions && origin != "" && r.Header.Get("Access-Control-Request-Method") != "" {
		h.preflight(w, r, origin)
		return
	}
	h.mux.ServeHTTP(&corsWriter{ResponseWriter: w, policy: h.policy, allowOrigin: h.policy.allowed(origin)}, r)
}

// preflight answers a browser's OPTIONS from the route table (§2): the
// methods are those of the routes that would serve the path, and the
// request headers are echoed, since a route reads only the ones it names.
func (h *corsHandler) preflight(w http.ResponseWriter, r *http.Request, origin string) {
	out := w.Header()
	out.Add("Vary", "Access-Control-Request-Method")
	out.Add("Vary", "Access-Control-Request-Headers")
	if !h.policy.any {
		out.Add("Vary", "Origin")
	}
	var methods []string
	for _, verb := range h.verbs {
		probe := r.Clone(r.Context())
		probe.Method = verb
		if _, pattern := h.mux.Handler(probe); pattern != "" {
			methods = append(methods, verb)
		}
	}
	if len(methods) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no route has this path"})
		return
	}
	allowOrigin := h.policy.allowed(origin)
	requested := r.Header.Get("Access-Control-Request-Method")
	var headers []string
	for _, line := range r.Header.Values("Access-Control-Request-Headers") {
		for _, name := range strings.Split(line, ",") {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			if !httpspec.IsToken(name) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			headers = append(headers, name)
		}
	}
	if allowOrigin == "" || !contains(methods, requested) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	out.Set("Access-Control-Allow-Origin", allowOrigin)
	if !h.policy.any {
		out.Set("Access-Control-Allow-Credentials", "true")
	}
	out.Set("Access-Control-Allow-Methods", strings.Join(methods, ", "))
	if len(headers) > 0 {
		out.Set("Access-Control-Allow-Headers", strings.Join(headers, ", "))
	}
	out.Set("Access-Control-Max-Age", preflightMaxAge)
	w.WriteHeader(http.StatusNoContent)
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// corsWriter adds the CORS headers to every real response when its status
// is written, whichever path wrote it: a workflow's answer, a 400 or 415,
// a 404 for an unknown path, or a fault's 500 (§8).
type corsWriter struct {
	http.ResponseWriter
	policy      *CORS
	allowOrigin string
	written     bool
}

func (w *corsWriter) WriteHeader(status int) {
	if !w.written {
		w.written = true
		w.addHeaders()
	}
	w.ResponseWriter.WriteHeader(status)
}

// Unwrap lets http.ResponseController reach the connection, for ADR 0076
// §3's deadlines.
func (w *corsWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *corsWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *corsWriter) addHeaders() {
	h := w.Header()
	if !w.policy.any {
		addVaryOrigin(h)
	}
	if w.allowOrigin == "" {
		return
	}
	// §4: the headers this response set, as the program spelled them,
	// less the ones a script may read already, Set-Cookie, which no
	// script may, and the nosniff Go's own 404 and 405 add.
	var expose []string
	for name := range h {
		lower := strings.ToLower(name)
		if safelistedResponseHeaders[lower] || lower == "set-cookie" || lower == "vary" || lower == "x-content-type-options" || strings.HasPrefix(lower, "access-control-") {
			continue
		}
		expose = append(expose, name)
	}
	sort.Strings(expose)
	h.Set("Access-Control-Allow-Origin", w.allowOrigin)
	if !w.policy.any {
		h.Set("Access-Control-Allow-Credentials", "true")
	}
	if len(expose) > 0 {
		h.Set("Access-Control-Expose-Headers", strings.Join(expose, ", "))
	}
}

// addVaryOrigin adds Origin to the response's Vary (§5), keeping what the
// program set: `Vary: Accept-Language` becomes `Accept-Language, Origin`,
// and `Vary: *` already means the response is never reused.
func addVaryOrigin(h http.Header) {
	key := "Vary"
	for name := range h {
		if strings.EqualFold(name, "Vary") {
			key = name
		}
	}
	for _, line := range h[key] {
		for _, v := range strings.Split(line, ",") {
			if v = strings.TrimSpace(v); v == "*" || strings.EqualFold(v, "Origin") {
				return
			}
		}
	}
	h[key] = []string{strings.Join(append(h[key], "Origin"), ", ")}
}
