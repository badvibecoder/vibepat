//go:build !windows

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// isTerminal reports whether f is a real interactive terminal.
//
// The obvious check -- os.ModeCharDevice -- is wrong: /dev/null is a character
// device but not a terminal, and a CI runner or a sandbox commonly hands the
// process one. Asking the kernel for the terminal attributes is definitive, so
// this issues a termios ioctl instead. Piped output and CI logs therefore stay
// free of ANSI color, and the interactive REPL refuses to start rather than
// panicking on a stream it cannot put into raw mode.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	return err == nil
}
