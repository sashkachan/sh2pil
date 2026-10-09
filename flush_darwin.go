//go:build darwin

package main

import "golang.org/x/sys/unix"

// flushInput drops the input the terminal driver already holds, including a reply to a
// colour query that came back before this process started. TIOCFLUSH with FREAD (1, from
// <sys/file.h>) is the Darwin spelling of the request.
func flushInput(fd int) {
	_ = unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, 1)
}
