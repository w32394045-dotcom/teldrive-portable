// Package desktop is the tray presence for the portable drive on Windows.
//
// The drive is a web application. This package exists so that a double-click can
// start it and open the page in the browser people already use, with no console
// window, no embedded browser engine and no second interface to keep in sync
// with the first one. What is left is a tray icon: it is the one thing a
// windowless program needs in order to be visible, stoppable and reopenable.
package desktop

import "errors"

// Status is what the tray icon reports: where the drive is, and whether anybody
// is waiting to be let in. It is read on the UI thread by a timer, so it must be
// cheap; the work that fills it belongs on another goroutine.
type Status struct {
	// Address is where this computer opens the drive.
	Address string
	// NetworkAddress is where another device on the same network opens it.
	NetworkAddress string
	// Pending counts the devices waiting to be approved.
	Pending int
	// Detail is one line about what the drive is doing, shown in the tooltip.
	Detail string
}

// Callbacks is everything the tray does on the user's behalf. Every callback
// runs on the UI thread, so it must return promptly; anything slow belongs on a
// goroutine of the caller's own.
type Callbacks struct {
	// Title is the tray tooltip prefix and the text on the message boxes.
	Title string
	// Status returns the current state; it is called on a timer.
	Status func() Status
	// OpenBrowser opens the drive in the default browser. It is what happens on
	// a double-click, because the drive is the page.
	OpenBrowser func() error
	// Copy puts text on the clipboard, for the address.
	Copy func(text string) error
	// Quit asks the application to shut down; the tray stops when it returns.
	Quit func()
	// Log receives diagnostics. The tray has no console to print them to.
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
