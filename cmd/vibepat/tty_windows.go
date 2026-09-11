//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// isTerminal reports whether f is a real interactive console.
//
// The implementation differs from Unix because golang.org/x/sys/unix does not
// exist on Windows, and the termios ioctl this project otherwise relies on has
// no Windows equivalent. GetConsoleMode is the corresponding check: it succeeds
// only for a genuine console handle, so a redirected pipe or a file is correctly
// reported as not a terminal.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) == nil
}
