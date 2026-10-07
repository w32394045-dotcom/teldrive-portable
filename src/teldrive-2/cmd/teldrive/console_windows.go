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

// minimizeConsoleWindow hides the console when the app is started at login, so an
// autostart does not throw a black window in the user's face every boot.
func minimizeConsoleWindow() {
	const swMinimize = 6
	user32 := syscall.NewLazyDLL("user32.dll")
	getConsoleWindow := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow")
	showWindow := user32.NewProc("ShowWindow")
	if err := getConsoleWindow.Find(); err != nil {
		return
	}
	if err := showWindow.Find(); err != nil {
		return
	}
	handle, _, _ := getConsoleWindow.Call()
	if handle == 0 {
		return
	}
	_, _, _ = showWindow.Call(handle, uintptr(swMinimize))
}
