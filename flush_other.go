//go:build !darwin && !linux

package main

// flushInput is a no-op where the request has no known spelling.
func flushInput(fd int) {}
