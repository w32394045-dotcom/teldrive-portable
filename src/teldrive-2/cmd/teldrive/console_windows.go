//go:build windows

package main

import "syscall"

// enableUTF8Console switches the attached console to UTF-8 so the launcher hint
// (which is Chinese) is readable on a system whose default console code page is
// a legacy one such as 936. Best effort: if there is no console, or the call
// fails, output falls back to whatever the console already uses.
func enableUTF8Console() {
	const utf8CodePage = 65001
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	setConsoleOutputCP := kernel32.NewProc("SetConsoleOutputCP")
	if err := setConsoleOutputCP.Find(); err != nil {
		return
	}
	_, _, _ = setConsoleOutputCP.Call(uintptr(utf8CodePage))
}
