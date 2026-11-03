//go:build !windows

package runtime

import "syscall"

// On Unix the portable syscall constants in socketFailureOf already match.
var platformErrnoVariants = map[syscall.Errno]string{}
