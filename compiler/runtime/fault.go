package runtime

import (
	"fmt"
	"strings"
)

// Fault is an unexpected runtime defect — fiig-lang.md §3.6's "unexpected
// runtime defects," distinct from a typed Result error, which is just an
// ordinary Value flowing through ordinary control flow. A Fault can only
// arise from something the checker didn't (or couldn't yet) rule out ahead
// of time: an unregistered capability, a division by zero, a match with no
// matching arm, calling a declaration with the wrong argument shape. In a
// fully checked program these should never actually fire; they exist so the
// interpreter fails loudly with real context instead of panicking bare or
// silently returning a zero value.
type Fault struct {
	Message string
	// Stack is the chain of declaration/capability names active when the
	// fault occurred, outermost first — built up one frame at a time as the
	// fault propagates back out through Interp.Call (interp.go), so a fault
	// deep inside a nested workflow call still reports the whole call chain
	// that led to it, not just the innermost frame.
	Stack []string
}

func Faultf(format string, args ...any) *Fault {
	return &Fault{Message: fmt.Sprintf(format, args...)}
}

// withFrame prepends op to the stack — called once per Interp.Call as a
// fault unwinds, so Stack ends up outermost-first by construction.
func (f *Fault) withFrame(op string) *Fault {
	f.Stack = append([]string{op}, f.Stack...)
	return f
}

func (f *Fault) Error() string { return f.String() }

func (f *Fault) String() string {
	if len(f.Stack) == 0 {
		return f.Message
	}
	return f.Message + " (in " + strings.Join(f.Stack, " -> ") + ")"
}
