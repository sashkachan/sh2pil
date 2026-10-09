//go:build linux

package main

import "golang.org/x/sys/unix"

// flushInput drops the input the terminal driver already holds, including a reply to a
// colour query that came back before this process started. TCFLSH with TCIFLUSH (0) is the
// Linux spelling of the request; its argument is a plain value, not a pointer.
func flushInput(fd int) {
	_ = unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH)
}
