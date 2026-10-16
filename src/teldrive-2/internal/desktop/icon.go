package desktop

import _ "embed"

// trayIcon is the same icon the web interface uses, so the window, the tray and
// the browser tab are recognisably one thing. It is embedded rather than read
// from disk because a portable bundle is copied around, and a shell that loses
// its icon when a file goes missing is worse than no icon at all.
//
//go:embed assets/tray.ico
var trayIcon []byte

func faviconBytes() ([]byte, error) {
	if len(trayIcon) == 0 {
		return nil, ErrUnsupported
	}
	return trayIcon, nil
}
