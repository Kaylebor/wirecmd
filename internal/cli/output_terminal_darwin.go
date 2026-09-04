//go:build darwin

package cli

import (
	"os"
	"syscall"
	"unsafe"
)

func outputFileIsTerminal(file *os.File) bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TIOCGETA, uintptr(unsafe.Pointer(&termios)))
	return errno == 0
}
