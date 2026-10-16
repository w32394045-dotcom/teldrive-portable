// Package desktop is the native shell for the portable drive on Windows: one
// window and one tray icon that belong to this computer.
//
// It exists because the console was the only thing holding the drive open. A
// portable app that has to keep a black window on screen is a portable app
// people close by accident, and the window also cannot answer the two questions
// that actually matter at a glance: where the drive is, and who is waiting to be
// let in.
package desktop

import "errors"

// Snapshot is what the shell shows about the running drive. It is read on the UI
// thread, so it must be cheap: the work that fills it happens on another
// goroutine.
type Snapshot struct {
	// Address is where this computer opens the drive.
	Address string
	// NetworkAddress is where another device on the same network opens it.
	NetworkAddress string
	// Pending counts the devices waiting to be approved.
	Pending int
	// Detail is one line about what the drive is doing.
	Detail string
}

// Callbacks is everything the shell does on the user's behalf. Every callback
// runs on the UI thread, so it must return promptly; anything slow belongs on a
// goroutine of the caller's own.
type Callbacks struct {
	// Title is the window title and the tray tooltip prefix.
	Title string
	// StartHidden opens the drive in the tray only, with no window on screen.
	// That is what starting at login needs: nobody asked for a window then.
	StartHidden bool
	// Snapshot returns the current state; it is called on a timer.
	Snapshot func() Snapshot
	// OpenBrowser opens the drive in the default browser.
	OpenBrowser func() error
	// Copy puts text on the clipboard, for the address.
	Copy func(text string) error
	// Quit asks the application to shut down; the shell stops when it returns.
	Quit func()
	// Log receives diagnostics. The shell has no console to print them to.
	Log func(msg string, args ...any)
}

func (c Callbacks) title() string {
	if c.Title == "" {
		return "Teldrive"
	}
	return c.Title
}

func (c Callbacks) log(msg string, args ...any) {
	if c.Log != nil {
		c.Log(msg, args...)
	}
}

// ErrUnsupported is returned on platforms without a native shell.
var ErrUnsupported = errors.New("the desktop shell is only implemented for Windows")
