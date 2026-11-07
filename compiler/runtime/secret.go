package runtime

import "crypto/subtle"

// Secret is a config value that is never shown (ADR 0071 §4). Everywhere a
// value is printed it is Secret(NAME), naming its environment variable;
// only reveal gives the text, and the checker allows that only in a gateway.
type Secret struct {
	Env   string // the environment variable it was read from
	value string
}

// NewSecret wraps a value read from the environment variable env.
func NewSecret(env, value string) Secret { return Secret{Env: env, value: value} }

func (Secret) isValue()         {}
func (s Secret) String() string { return "Secret(" + s.Env + ")" }

func init() {
	builtinMethods["Secret"] = map[string]builtinFunc{
		"reveal": func(recv Value, _ []Value) (Value, *Fault) {
			return Str(recv.(Secret).value), nil
		},
		// Constant time in the length of the candidate, so checking a
		// presented token doesn't leak it through timing; like Go's
		// subtle.ConstantTimeCompare, it still reveals a length mismatch.
		"matches": func(recv Value, args []Value) (Value, *Fault) {
			got := []byte(string(args[0].(Str)))
			want := []byte(recv.(Secret).value)
			return Bool(subtle.ConstantTimeCompare(got, want) == 1), nil
		},
	}
}
