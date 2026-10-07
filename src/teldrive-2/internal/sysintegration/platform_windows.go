//go:build windows

package sysintegration

import (
	"context"
	"os/exec"
	"runtime"

	"golang.org/x/sys/windows/registry"
)

func platformName() string { return runtime.GOOS }

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type platformRegistry struct{}

func hiveKey(hive string) registry.Key {
	if hive == HiveLocalMachine {
		return registry.LOCAL_MACHINE
	}
	return registry.CURRENT_USER
}

func (platformRegistry) ReadDWORD(hive, path, name string) (uint32, bool) {
	key, err := registry.OpenKey(hiveKey(hive), path, registry.QUERY_VALUE)
	if err != nil {
		return 0, false
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue(name)
	if err != nil {
		return 0, false
	}
	return uint32(value), true
}

func (platformRegistry) ReadString(hive, path, name string) (string, bool) {
	key, err := registry.OpenKey(hiveKey(hive), path, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer key.Close()
	value, _, err := key.GetStringValue(name)
	if err != nil {
		return "", false
	}
	return value, true
}

func (platformRegistry) WriteString(hive, path, name, value string) error {
	key, _, err := registry.CreateKey(hiveKey(hive), path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue(name, value)
}

func (platformRegistry) DeleteValue(hive, path, name string) error {
	key, err := registry.OpenKey(hiveKey(hive), path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.DeleteValue(name); err != nil && err != registry.ErrNotExist {
		return err
	}
	return nil
}

// runnerPlatform is referenced by tests to document the platform under test.
const runnerPlatform = "windows"
