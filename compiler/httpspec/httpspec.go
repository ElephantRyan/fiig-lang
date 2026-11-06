// Package httpspec holds the HTTP grammar rules ADR 0069 enforces, in one
// place for both sides: the checker applies them to constants, and the
// adapter to the values a program builds at run time (§6).
package httpspec

import (
	"fmt"
	"strings"
)

// IsToken reports whether s is an RFC 9110 §5.6.2 token: one or more
// tchars. Header names and cookie names (RFC 6265 §4.1.1) are tokens.
func IsToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isTchar(s[i]) {
			return false
		}
	}
	return true
}

func isTchar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", b) >= 0
}

// ValidMediaType reports whether s is `type/subtype` with both parts tokens
// and no parameters (ADR 0069 §5: the adapter adds `charset` itself).
func ValidMediaType(s string) bool {
	typ, sub, ok := strings.Cut(s, "/")
	return ok && IsToken(typ) && IsToken(sub)
}

// IsJSON reports whether a declared media type is JSON: application/json,
// or any type with a +json suffix (RFC 6839).
func IsJSON(mediaType string) bool {
	m := strings.ToLower(mediaType)
	return m == "application/json" || strings.HasSuffix(m, "+json")
}

// IsText reports whether a declared media type is text/*, which gets
// `; charset=utf-8` when its body is a string.
func IsText(mediaType string) bool {
	return strings.HasPrefix(strings.ToLower(mediaType), "text/")
}

// SameType reports whether a request's Content-Type header value names
// the declared type, ignoring parameters and case (ADR 0069 §5).
func SameType(header, declared string) bool {
	typ, _, _ := strings.Cut(header, ";")
	return strings.EqualFold(strings.TrimSpace(typ), declared)
}

// HeaderValueProblem describes why v can't be a header value, or "" if it
// can: a control character other than tab (RFC 9110 §5.5). Non-ASCII text
// is allowed and written as UTF-8.
func HeaderValueProblem(v string) string {
	for i := 0; i < len(v); i++ {
		if b := v[i]; (b < 0x20 && b != '\t') || b == 0x7f {
			return fmt.Sprintf("contains control character %s", describeByte(b))
		}
	}
	return ""
}

// CookieValueProblem describes why v can't be a cookie value, or "" if it
// can: RFC 6265 §4.1.1's cookie-octets, so no space, comma, semicolon,
// double quote, backslash, control character, or non-ASCII byte.
func CookieValueProblem(v string) string {
	for i := 0; i < len(v); i++ {
		b := v[i]
		if b <= 0x20 || b >= 0x7f || b == '"' || b == ',' || b == ';' || b == '\\' {
			return fmt.Sprintf("contains %s, which a cookie value can't carry", describeByte(b))
		}
	}
	return ""
}

// CookiePathProblem describes why p can't be a cookie's Path, or "": a
// semicolon or a control character would write an attribute of its own.
func CookiePathProblem(p string) string {
	for i := 0; i < len(p); i++ {
		if b := p[i]; b < 0x20 || b == 0x7f || b == ';' {
			return fmt.Sprintf("contains %s", describeByte(b))
		}
	}
	return ""
}

// IsHostname reports whether d can be a cookie's Domain: dot-separated
// labels of letters, digits, and hyphens, optionally with one leading dot.
func IsHostname(d string) bool {
	d = strings.TrimPrefix(d, ".")
	if d == "" || len(d) > 253 {
		return false
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			b := label[i]
			if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-') {
				return false
			}
		}
	}
	return true
}

// StatusProblem describes why status can't be a program's response, or "":
// fiig sends 200 to 599 (ADR 0069 §6).
func StatusProblem(status int64) string {
	if status < 200 || status > 599 {
		return fmt.Sprintf("status %d is outside 200 to 599", status)
	}
	return ""
}

// BodyForbidden reports whether a response with this status can't carry a
// body (RFC 9110 §15.3.5, §15.4.5).
func BodyForbidden(status int64) bool {
	return status == 204 || status == 304
}

// AdapterOwnedHeader says where the adapter takes name from when it is a
// header the adapter writes itself (ADR 0069 §6, ADR 0070 §7), or "" when
// a program may set it.
func AdapterOwnedHeader(name string) string {
	lower := strings.ToLower(name)
	switch lower {
	case "content-type":
		return "the adapter writes it from produces"
	case "content-length":
		return "the adapter writes it from the body"
	case "set-cookie":
		return "the adapter writes it from cookies"
	}
	if strings.HasPrefix(lower, "access-control-") {
		return "the adapter writes CORS headers from fiig serve -allow-origin"
	}
	return ""
}

func describeByte(b byte) string {
	switch b {
	case '\r':
		return "a carriage return"
	case '\n':
		return "a line feed"
	case ' ':
		return "a space"
	}
	if b < 0x20 || b == 0x7f {
		return fmt.Sprintf("byte 0x%02x", b)
	}
	if b >= 0x80 {
		return "a non-ASCII byte"
	}
	return fmt.Sprintf("%q", string(b))
}

// CookieRuleProblem describes a cookie a browser would ignore without a
// word (ADR 0069 §6), or "": SameSite=None without Secure (RFC 6265bis),
// and the __Secure- and __Host- name prefixes' requirements.
func CookieRuleProblem(name string, sameSiteNone, secure, hasDomain bool, path string) string {
	if sameSiteNone && !secure {
		return "is sameSite AnySite and sentOverPlainHttp, and browsers refuse SameSite=None without Secure"
	}
	if strings.HasPrefix(name, "__Secure-") && !secure {
		return "has the __Secure- prefix, which browsers refuse without Secure; drop sentOverPlainHttp"
	}
	if strings.HasPrefix(name, "__Host-") {
		switch {
		case !secure:
			return "has the __Host- prefix, which browsers refuse without Secure; drop sentOverPlainHttp"
		case hasDomain:
			return "has the __Host- prefix, which browsers refuse with a domain"
		case path != "/":
			return "has the __Host- prefix, which browsers refuse with a path other than \"/\""
		}
	}
	return ""
}
