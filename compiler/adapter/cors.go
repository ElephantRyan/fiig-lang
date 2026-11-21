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
	any      bool                  // `*`: every origin, never credentials (§3)
	origins  map[string]bool       // exact, lowercased `scheme://host[:port]`
	patterns map[string]subdomains // `scheme://*.domain[:port]`, lowercased (§9)
}

// subdomains is a wildcard entry, `https://*.example.com` (§9, amended
// 2026-11-21): any origin with this scheme and port whose host is one or
// more labels, then `.` and the domain. The domain itself (the apex) is
// not matched; list it exactly beside the pattern if it serves a front end.
type subdomains struct {
	scheme string // "http" or "https"
	domain string // "example.com": at least two labels
	port   string // "" for the scheme's default
}

// matches reports whether origin, already lowercased, is a subdomain of
// p's domain with p's scheme and port. Each label before the domain must be
// letters, digits and hyphens, so nothing but a host can match.
func (p subdomains) matches(origin string) bool {
	rest, ok := strings.CutPrefix(origin, p.scheme+"://")
	if !ok {
		return false
	}
	host := rest
	if p.port != "" {
		if host, ok = strings.CutSuffix(rest, ":"+p.port); !ok {
			return false
		}
	}
	sub, ok := strings.CutSuffix(host, "."+p.domain)
	if !ok || sub == "" {
		return false
	}
	for _, label := range strings.Split(sub, ".") {
		if !isHostLabel(label) {
			return false
		}
	}
	return true
}

// isHostLabel reports whether label is one DNS label as an origin spells
// it: lowercase letters, digits and inner hyphens, 1 to 63 bytes.
func isHostLabel(label string) bool {
	if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for i := 0; i < len(label); i++ {
		b := label[i]
		if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-') {
			return false
		}
	}
	return true
}

// Origins lists the allowed origins and subdomain patterns, sorted, or `*`.
func (c *CORS) Origins() []string {
	if c.any {
		return []string{"*"}
	}
	var list []string
	for o := range c.origins {
		list = append(list, o)
	}
	for o := range c.patterns {
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
// in Origin, or that form with `*` as the whole first label of the host
// (`https://*.example.com`, §9 as amended 2026-11-21); `null` and any other
// pattern are refused, and `*` must stand alone.
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
	c := &CORS{origins: map[string]bool{}, patterns: map[string]subdomains{}}
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
			p, err := parseSubdomains(o)
			if err != nil {
				return nil, err
			}
			c.patterns[strings.ToLower(o)] = p
			continue
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

// parseSubdomains reads a wildcard entry (§9): `scheme://*.domain` or
// `scheme://*.domain:port`, the `*` standing for one or more whole labels.
// A `*` anywhere else, `https://*` alone, and a domain of one label
// (`https://*.com`) are refused, since each would trust hosts nobody
// writing it means to.
func parseSubdomains(o string) (subdomains, error) {
	shape := fmt.Errorf("allowed origin %q has a wildcard where none is allowed; the only pattern is * as the whole first label of the host, as https://*.example.com or http://*.example.com:8080", o)
	lower := strings.ToLower(o)
	var p subdomains
	var rest string
	switch {
	case strings.HasPrefix(lower, "https://*."):
		p.scheme, rest = "https", lower[len("https://*."):]
	case strings.HasPrefix(lower, "http://*."):
		p.scheme, rest = "http", lower[len("http://*."):]
	default:
		return p, shape
	}
	if strings.Contains(rest, "*") {
		return p, shape
	}
	if host, port, found := strings.Cut(rest, ":"); found {
		if port == "" || len(port) > 5 || strings.Trim(port, "0123456789") != "" || port[0] == '0' {
			return p, fmt.Errorf("allowed origin %q has a port that isn't a number from 1 to 65535", o)
		}
		if (p.scheme == "http" && port == "80") || (p.scheme == "https" && port == "443") {
			return p, fmt.Errorf("allowed origin %q names its scheme's default port, which browsers leave out of Origin; write %s://*.%s", o, p.scheme, host)
		}
		rest, p.port = host, port
	}
	labels := strings.Split(rest, ".")
	for _, label := range labels {
		if !isHostLabel(label) {
			return p, fmt.Errorf("allowed origin %q isn't an origin pattern: after *. comes a domain name, dot-separated labels of letters, digits and hyphens, then an optional :port, with no path or trailing slash", o)
		}
	}
	if len(labels) < 2 {
		return p, fmt.Errorf("allowed origin %q would allow every host under %q; a pattern's domain has at least two labels, as https://*.example.com", o, rest)
	}
	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return p, fmt.Errorf("allowed origin %q ends in a number, like an IP address, which has no subdomains; list the origin exactly", o)
	}
	p.domain = rest
	return p, nil
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
	lower := strings.ToLower(origin)
	for _, p := range c.patterns {
		if p.matches(lower) {
			return origin
		}
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
