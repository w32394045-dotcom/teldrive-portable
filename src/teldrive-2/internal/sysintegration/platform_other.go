//go:build !windows

package sysintegration

import (
	"context"
	"os/exec"
	"runtime"
)

func platformName() string { return runtime.GOOS }

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// platformRegistry is a no-op on platforms without the Windows registry; the
// Manager turns "not supported" into a clear message before any of this is used.
type platformRegistry struct{}

func (platformRegistry) ReadDWORD(string, string, string) (uint32, bool)  { return 0, false }
func (platformRegistry) ReadString(string, string, string) (string, bool) { return "", false }
func (platformRegistry) WriteString(string, string, string, string) error { return ErrUnsupported }
func (platformRegistry) DeleteValue(string, string, string) error         { return ErrUnsupported }
