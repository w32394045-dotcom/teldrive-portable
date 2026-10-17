//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// OpenAppWindow opens the drive the way an application opens: as a window with
// nothing but the page in it, no tab strip and no address bar.
//
// It is still a browser - the one already installed on the machine renders it -
// not an embedded engine, which is the difference between a window that is the
// web interface and a second interface that has to be kept in step with it.
// Chromium and Edge both implement this as --app=URL; a machine with neither
// falls back to the default browser, which is a tab but still the real page.
//
// profileDir is a browser profile of the drive's own: the login survives
// between starts, and the window never mixes with the person's own browsing.
func OpenAppWindow(url, profileDir string) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("nothing to open")
	}
	browser, ok := findChromium()
	if !ok {
		return fmt.Errorf("no Chromium-based browser found to open %s in app mode", url)
	}
	var args []string
	if profileDir != "" {
		if err := os.MkdirAll(profileDir, 0o755); err != nil {
			return fmt.Errorf("create the browser profile: %w", err)
		}
		args = append(args, "--user-data-dir="+profileDir)
	}
	args = append(args,
		"--app="+url,
		"--window-size=1280,860",
		// A first run must not open the browser's own welcome tabs next to the
		// drive, and must not offer to become the default browser from behind it.
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-features=Translate",
	)
	command := exec.Command(browser, args...)
	if err := command.Start(); err != nil {
		return fmt.Errorf("start %s: %w", filepath.Base(browser), err)
	}
	// The window outlives this process on purpose: closing the drive from its
	// tray icon should not close the page somebody is looking at.
	return command.Process.Release()
}

// findChromium returns the installed Chromium-based browser, preferring the one
// Windows itself would consider the record for the executable.
func findChromium() (string, bool) {
	for _, name := range []string{"msedge.exe", "chrome.exe"} {
		if path, ok := registeredApp(name); ok {
			return path, true
		}
	}
	for _, path := range knownBrowserPaths() {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, true
		}
	}
	return "", false
}

// registeredApp reads the App Paths entry, which is where an installer records
// an executable that can be launched by name. It is more reliable than guessing
// install directories, because it also covers channels and custom locations.
func registeredApp(name string) (string, bool) {
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		key, err := registry.OpenKey(root,
			`SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`+name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		value, _, err := key.GetStringValue("")
		key.Close()
		if err != nil || strings.TrimSpace(value) == "" {
			continue
		}
		if info, err := os.Stat(value); err == nil && !info.IsDir() {
			return value, true
		}
	}
	return "", false
}

// knownBrowserPaths covers the channels that do not always write an App Paths
// entry (Chrome puts Beta and Dev in their own directories).
func knownBrowserPaths() []string {
	var paths []string
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if root == "" {
			continue
		}
		for _, channel := range []string{"Chrome", "Chrome Beta", "Chrome Dev", "Chrome SxS"} {
			paths = append(paths, filepath.Join(root, "Google", channel, "Application", "chrome.exe"))
		}
		paths = append(paths, filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"))
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		paths = append(paths, filepath.Join(local, "Google", "Chrome", "Application", "chrome.exe"))
		paths = append(paths, filepath.Join(local, "Microsoft", "Edge", "Application", "msedge.exe"))
	}
	return paths
}
