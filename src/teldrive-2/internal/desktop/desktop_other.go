//go:build !windows

package desktop

// The shell is Windows-only. Everywhere else the drive is the console program it
// has always been, and saying so is better than a window that cannot exist.

// Run reports that there is no native shell on this platform.
func Run(Callbacks) error { return ErrUnsupported }

// Message does nothing without a message box to show it in.
func Message(string, string) {}

// Error does nothing without a message box to show it in.
func Error(string, string) {}

// Prompt reports that nobody can be asked.
func Prompt(string, string, bool) (string, bool) { return "", false }

// CopyToClipboard reports that there is no clipboard here.
func CopyToClipboard(string) error { return ErrUnsupported }
