// Package capability holds the compiler's closed set of primitive
// capabilities (ADR 0042). The native floor is the socket: a capability
// is admitted here only if it cannot be expressed in fiig at all, not
// merely because writing it in fiig would be inconvenient. Every other
// capability — Database, PaymentGateway, an HTTP client — is meant to be a
// fiig contract with a fiig fulfiller (ADR 0043) layered over these.
package capability

import "sort"

// primitives is deliberately unexported and fixed: membership is a
// compiler decision, not something a package or a host can extend.
var primitives = map[string]bool{
	// TLS is native too (it needs audited crypto, not a fiig rewrite), but
	// as Socket.connectTls/startTls rather than a capability of its own:
	// protocols upgrade a live plain connection, and ADR 0010 allows a
	// gateway one capability (ADR 0060).
	"Socket":     true,
	"Clock":      true,
	"FileSystem": true,
	"Random":     true,
}

// IsPrimitive reports whether name is one of the closed primitive set.
func IsPrimitive(name string) bool { return primitives[name] }

// Primitives lists the closed set, sorted.
func Primitives() []string {
	out := make([]string, 0, len(primitives))
	for name := range primitives {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
