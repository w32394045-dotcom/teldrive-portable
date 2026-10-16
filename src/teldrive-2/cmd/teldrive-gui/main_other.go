//go:build !windows

package main

import (
	"fmt"
	"os"
)

// The native shell is Windows-only: it is written against the Windows APIs and
// exists because the portable bundle is a Windows distribution. Everywhere else
// the console program is the whole story.
func main() {
	fmt.Fprintln(os.Stderr, "teldrive-gui 只在 Windows 上有原生窗口；其他系统请使用 teldrive。")
	os.Exit(1)
}
