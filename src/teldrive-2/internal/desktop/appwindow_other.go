//go:build !windows

package desktop

// OpenAppWindow reports that there is no app-mode window to open here.
func OpenAppWindow(string, string) error { return ErrUnsupported }
