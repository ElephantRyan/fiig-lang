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
	"Socket": true,
	// ADR 0114, ADR 0117 §2: a connection the runtime owns past any call,
	// reading for it and running its package's protocol on what arrives.
	"Session":    true,
	"Clock":      true,
	"FileSystem": true,
	// ADR 0140 §4: saving a setting the runtime reads back as config. Only
	// the runtime can do it, since it owns the snapshot and parses the text
	// as the field's type.
	"Settings": true,
	"Random":   true,
}

// IsPrimitive reports whether name is one of the closed primitive set.
func IsPrimitive(name string) bool { return primitives[name] }

// localEffects are the primitives that read the machine the program runs
// on instead of reaching another system (ADR 0068 §4). An activity or a
// gateway calls them directly; none is a capability a gateway requires.
var localEffects = map[string]bool{
	"Clock":  true,
	"Random": true,
}

// IsLocalEffect reports whether name is a local-effect primitive.
func IsLocalEffect(name string) bool { return localEffects[name] }

// Primitives lists the closed set, sorted.
func Primitives() []string {
	out := make([]string, 0, len(primitives))
	for name := range primitives {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
