//go:build !windows

package main

// enableUTF8Console is a no-op where the console already speaks UTF-8.
func enableUTF8Console() {}
