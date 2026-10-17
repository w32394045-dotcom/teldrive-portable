//go:build !windows

package main

import (
	"fmt"
	"os"
)

// The tray is Windows-only, because the portable bundle is a Windows
// distribution. Everywhere else the console program is the whole story.
func main() {
	fmt.Fprintln(os.Stderr, "teldrive-gui 只在 Windows 上有托盘图标；其他系统请使用 teldrive。")
	os.Exit(1)
}
