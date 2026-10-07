package sysintegration

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestLiveAutostartAgainstTheRealRegistry is skipped unless explicitly requested:
//
//	set TELDRIVE_LIVE_SYSINTEGRATION=1
//	go test ./internal/sysintegration/ -run TestLiveAutostart -v
//
// It writes and removes the per-user Run entry for real, because that is the only
// way to prove the autostart feature works on a given machine. It restores the
// previous value on the way out.
func TestLiveAutostartAgainstTheRealRegistry(t *testing.T) {
	if os.Getenv("TELDRIVE_LIVE_SYSINTEGRATION") != "1" {
		t.Skip("set TELDRIVE_LIVE_SYSINTEGRATION=1 to touch the real registry")
	}
	ctx := context.Background()
	manager := New(`C:\teldrive-live-test\teldrive.exe`)

	registry := platformRegistry{}
	previous, hadPrevious := registry.ReadString(HiveCurrentUser, runKeyPath, autostartValueName)
	defer func() {
		if hadPrevious {
			_ = registry.WriteString(HiveCurrentUser, runKeyPath, autostartValueName, previous)
		} else {
			_ = registry.DeleteValue(HiveCurrentUser, runKeyPath, autostartValueName)
		}
	}()

	t.Run("enable writes a usable command", func(t *testing.T) {
		if err := manager.SetAutostart(ctx, true); err != nil {
			t.Fatalf("SetAutostart(true) = %v", err)
		}
		status := manager.Autostart(ctx)
		if !status.Enabled {
			t.Fatal("status should report enabled after enabling")
		}
		// The stored command must be directly runnable: a quoted path plus the flag.
		if !strings.HasPrefix(status.Command, `"C:\teldrive-live-test\teldrive.exe"`) {
			t.Fatalf("command = %q, want the quoted executable path", status.Command)
		}
		if !strings.Contains(status.Command, autostartFlag) {
			t.Fatalf("command = %q, want %s", status.Command, autostartFlag)
		}
		if strings.Contains(status.Command, `\\`) {
			t.Fatalf("command = %q, backslashes must not be escaped", status.Command)
		}
		t.Logf("Run key value: %s", status.Command)
	})

	t.Run("disable removes it", func(t *testing.T) {
		if err := manager.SetAutostart(ctx, false); err != nil {
			t.Fatalf("SetAutostart(false) = %v", err)
		}
		if status := manager.Autostart(ctx); status.Enabled {
			t.Fatal("status still reports enabled after disabling")
		}
	})

	t.Run("prerequisites reflect this machine", func(t *testing.T) {
		prerequisites := manager.CheckPrerequisites(ctx)
		if len(prerequisites.Items) != 3 {
			t.Fatalf("expected three checks, got %d", len(prerequisites.Items))
		}
		for _, item := range prerequisites.Items {
			t.Logf("%-18s ok=%-5v current=%-10s required=%s",
				item.Key, item.OK, item.Current, item.Required)
		}
		if prerequisites.Ready && !prerequisites.NeedsAdmin {
			t.Log("this machine is already configured for WebDAV mounting")
		}
	})
}
