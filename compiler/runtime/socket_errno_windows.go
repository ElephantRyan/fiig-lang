package runtime

import "syscall"

// Winsock reports refusals, resets and unreachable hosts with its own WSAE
// codes, which syscall.ECONNREFUSED and friends do not match on Windows.
var platformErrnoVariants = map[syscall.Errno]string{
	10061: "Refused",     // WSAECONNREFUSED
	10054: "Reset",       // WSAECONNRESET
	10053: "Reset",       // WSAECONNABORTED
	10051: "Unreachable", // WSAENETUNREACH
	10065: "Unreachable", // WSAEHOSTUNREACH
}
