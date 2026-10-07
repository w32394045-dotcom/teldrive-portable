package sysintegration

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRegistry struct {
	values  map[string]uint32
	strings map[string]string
	writes  map[string]string
	deletes []string
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{
		values:  map[string]uint32{},
		strings: map[string]string{},
		writes:  map[string]string{},
	}
}

func key(hive, path, name string) string { return hive + "|" + path + "|" + name }

func (r *fakeRegistry) ReadDWORD(hive, path, name string) (uint32, bool) {
	value, ok := r.values[key(hive, path, name)]
	return value, ok
}

func (r *fakeRegistry) ReadString(hive, path, name string) (string, bool) {
	value, ok := r.strings[key(hive, path, name)]
	return value, ok
}

func (r *fakeRegistry) WriteString(hive, path, name, value string) error {
	r.writes[key(hive, path, name)] = value
	r.strings[key(hive, path, name)] = value
	return nil
}

func (r *fakeRegistry) DeleteValue(hive, path, name string) error {
	r.deletes = append(r.deletes, key(hive, path, name))
	delete(r.strings, key(hive, path, name))
	return nil
}

type fakeRunner struct {
	outputs map[string]string
	calls   [][]string
	err     error
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if r.outputs != nil {
		if output, ok := r.outputs[strings.Join(append([]string{name}, args...), " ")]; ok {
			return []byte(output), r.err
		}
	}
	return []byte(""), r.err
}

func lastCall(t *testing.T, runner *fakeRunner) []string {
	t.Helper()
	if len(runner.calls) == 0 {
		t.Fatal("no command was executed")
	}
	return runner.calls[len(runner.calls)-1]
}

func TestAutostartCommandQuotesPathAndStaysQuiet(t *testing.T) {
	t.Parallel()
	manager := newWithDeps(`C:\Program Files\Teldrive\teldrive.exe`, newFakeRegistry(), &fakeRunner{}, "windows")
	command := manager.AutostartCommand()
	if !strings.HasPrefix(command, `"C:\Program Files\Teldrive\teldrive.exe"`) {
		t.Fatalf("command = %q, want a quoted executable path", command)
	}
	if !strings.HasSuffix(command, autostartFlag) {
		t.Fatalf("command = %q, want the %s flag", command, autostartFlag)
	}
}

func TestSetAutostartWritesCurrentUserRunKeyAndRemovesItAgain(t *testing.T) {
	t.Parallel()
	registry := newFakeRegistry()
	manager := newWithDeps(`C:\teldrive\teldrive.exe`, registry, &fakeRunner{}, "windows")

	if err := manager.SetAutostart(context.Background(), true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	stored, ok := registry.writes[key(HiveCurrentUser, runKeyPath, autostartValueName)]
	if !ok {
		t.Fatal("the Run value was not written")
	}
	if !strings.Contains(stored, "teldrive.exe") {
		t.Fatalf("stored = %q", stored)
	}
	// Never the machine-wide hive: autostart must not need administrator rights.
	if strings.Contains(key(HiveLocalMachine, runKeyPath, autostartValueName), HiveCurrentUser) {
		t.Fatal("sanity check failed")
	}
	if _, ok := registry.writes[key(HiveLocalMachine, runKeyPath, autostartValueName)]; ok {
		t.Fatal("autostart must be per-user, not machine-wide")
	}

	if err := manager.SetAutostart(context.Background(), false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if len(registry.deletes) != 1 || registry.deletes[0] != key(HiveCurrentUser, runKeyPath, autostartValueName) {
		t.Fatalf("deletes = %v", registry.deletes)
	}

	status := manager.Autostart(context.Background())
	if status.Enabled {
		t.Fatal("status still reports enabled after disabling")
	}
	if !status.Supported {
		t.Fatal("Windows must be reported as supported")
	}
}

func TestAutostartReflectsAnExistingRunEntry(t *testing.T) {
	t.Parallel()
	registry := newFakeRegistry()
	registry.strings[key(HiveCurrentUser, runKeyPath, autostartValueName)] = `"C:\old\teldrive.exe" --autostart`
	manager := newWithDeps(`C:\new\teldrive.exe`, registry, &fakeRunner{}, "windows")

	status := manager.Autostart(context.Background())
	if !status.Enabled {
		t.Fatal("an existing Run entry must be reported as enabled")
	}
	if status.Command != `"C:\old\teldrive.exe" --autostart` {
		t.Fatalf("command = %q", status.Command)
	}
}

func TestCheckPrerequisites(t *testing.T) {
	t.Parallel()
	ready := func() *fakeRegistry {
		registry := newFakeRegistry()
		registry.values[key(HiveLocalMachine, webClientServicePath, serviceStartName)] = 2
		registry.values[key(HiveLocalMachine, webClientKeyPath, basicAuthLevelName)] = 2
		registry.values[key(HiveLocalMachine, webClientKeyPath, fileSizeLimitName)] = recommendedFileSizeLimit
		return registry
	}

	t.Run("all set", func(t *testing.T) {
		t.Parallel()
		manager := newWithDeps("", ready(), &fakeRunner{}, "windows")
		prerequisites := manager.CheckPrerequisites(context.Background())
		if !prerequisites.Ready {
			t.Fatalf("expected ready, got %+v", prerequisites.Items)
		}
		if prerequisites.NeedsAdmin {
			t.Fatal("a ready configuration must not ask for elevation")
		}
	})

	// The three real-world defaults from a stock Windows install.
	for _, test := range []struct {
		name   string
		mutate func(*fakeRegistry)
		item   string
	}{
		{"webclient on manual", func(r *fakeRegistry) {
			r.values[key(HiveLocalMachine, webClientServicePath, serviceStartName)] = 3
		}, "webclient_service"},
		{"basic auth limited to ssl", func(r *fakeRegistry) {
			r.values[key(HiveLocalMachine, webClientKeyPath, basicAuthLevelName)] = 1
		}, "basic_auth_level"},
		{"50 mb download cap", func(r *fakeRegistry) {
			r.values[key(HiveLocalMachine, webClientKeyPath, fileSizeLimitName)] = 50000000
		}, "file_size_limit"},
		{"nothing configured", func(r *fakeRegistry) {
			r.values = map[string]uint32{}
		}, "webclient_service"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			registry := ready()
			test.mutate(registry)
			manager := newWithDeps("", registry, &fakeRunner{}, "windows")
			prerequisites := manager.CheckPrerequisites(context.Background())
			if prerequisites.Ready {
				t.Fatal("expected the configuration to be reported as not ready")
			}
			if !prerequisites.NeedsAdmin {
				t.Fatal("fixing HKLM needs administrator rights")
			}
			found := false
			for _, item := range prerequisites.Items {
				if item.Key == test.item && !item.OK {
					found = true
				}
			}
			if !found {
				t.Fatalf("item %q was not reported as failing: %+v", test.item, prerequisites.Items)
			}
			// The fix has to be runnable both by us and by the user.
			for _, want := range []string{"BasicAuthLevel", "FileSizeLimitInBytes", "Start-Service -Name WebClient"} {
				if !strings.Contains(prerequisites.FixCommand, want) {
					t.Errorf("fix command does not mention %q: %s", want, prerequisites.FixCommand)
				}
			}
		})
	}
}

func TestPickDriveLetter(t *testing.T) {
	t.Parallel()
	manager := newWithDeps("", newFakeRegistry(), &fakeRunner{}, "windows")
	// A real `net use` listing, including the header and a UNC mapping.
	output := `
新加卷             \\\\server\\share     Microsoft Windows Network
OK                 Z:        \\\\127.0.0.1@8080\\webdav    Web Client Network
OK                 Y:        \\\\server\\other              Microsoft Windows Network
`
	if got := manager.pickDriveLetter(output); got != "X:" {
		t.Fatalf("pickDriveLetter = %q, want X:", got)
	}
	if got := manager.pickDriveLetter(""); got != "Z:" {
		t.Fatalf("pickDriveLetter(empty) = %q, want Z:", got)
	}
}

func TestFindMountForURL(t *testing.T) {
	t.Parallel()
	url := "http://127.0.0.1:8080/webdav"
	// Windows reports the mapping as a UNC path, so both forms must match.
	for _, output := range []string{
		"OK           Z:        \\\\127.0.0.1@8080\\webdav     Web Client Network",
		"OK           Z:        http://127.0.0.1:8080/webdav   Web Client Network",
	} {
		drive, ok := findMountForURL(output, url)
		if !ok || drive != "Z:" {
			t.Fatalf("findMountForURL(%q) = %q, %v", strings.TrimSpace(output), drive, ok)
		}
	}
	if _, ok := findMountForURL("OK   Y:   \\\\other\\share   Network", url); ok {
		t.Fatal("an unrelated mapping must not match")
	}
}

func TestMountUsesPersistentCredentials(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	manager := newWithDeps("", newFakeRegistry(), runner, "windows")

	drive, err := manager.Mount(context.Background(), "http://127.0.0.1:8080/webdav", "teldrive", "secret-key", "Z:")
	if err != nil {
		t.Fatalf("mount: %v", err)
	}
	if drive != "Z:" {
		t.Fatalf("drive = %q", drive)
	}
	call := strings.Join(lastCall(t, runner), " ")
	for _, want := range []string{
		"net use Z: http://127.0.0.1:8080/webdav",
		"/user:teldrive secret-key",
		"/persistent:yes", // survives a reboot, which pairing with autostart relies on
	} {
		if !strings.Contains(call, want) {
			t.Errorf("mount command %q does not contain %q", call, want)
		}
	}
}

func TestMountPicksAFreeLetterWhenNoneIsGiven(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{outputs: map[string]string{
		"net use": "OK   Z:   \\\\127.0.0.1@8080\\webdav   Web Client Network\n",
	}}
	manager := newWithDeps("", newFakeRegistry(), runner, "windows")

	drive, err := manager.Mount(context.Background(), "http://127.0.0.1:8080/webdav", "teldrive", "k", "")
	if err != nil {
		t.Fatalf("mount: %v", err)
	}
	if drive != "Y:" {
		t.Fatalf("drive = %q, want the next free letter Y:", drive)
	}
}

func TestUnmountDeletesTheMappingAndSurfacesFailures(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	manager := newWithDeps("", newFakeRegistry(), runner, "windows")
	if err := manager.Unmount(context.Background(), "Z:"); err != nil {
		t.Fatalf("unmount: %v", err)
	}
	call := strings.Join(lastCall(t, runner), " ")
	if !strings.Contains(call, "net use Z: /delete /y") {
		t.Fatalf("unmount command = %q", call)
	}

	failing := &fakeRunner{err: errors.New("exit status 2"), outputs: map[string]string{
		"net use Y: /delete /y": "找不到网络连接。",
	}}
	manager = newWithDeps("", newFakeRegistry(), failing, "windows")
	err := manager.Unmount(context.Background(), "Y:")
	if err == nil {
		t.Fatal("a failing unmount must return an error")
	}
	if !strings.Contains(err.Error(), "找不到网络连接") {
		t.Fatalf("error should carry net's message, got %v", err)
	}
}

func TestMountReportsNothingToMountWhenEveryLetterIsTaken(t *testing.T) {
	t.Parallel()
	full := ""
	for _, letter := range driveLetters {
		full += "OK   " + string(letter) + ":   \\\\server\\share   Network\n"
	}
	runner := &fakeRunner{outputs: map[string]string{"net use": full}}
	manager := newWithDeps("", newFakeRegistry(), runner, "windows")

	if _, err := manager.Mount(context.Background(), "http://127.0.0.1:8080/webdav", "u", "p", ""); err == nil {
		t.Fatal("expected an error when no drive letter is free")
	}
}

func TestUnsupportedPlatformIsReportedNotCrashed(t *testing.T) {
	t.Parallel()
	registry := newFakeRegistry()
	manager := newWithDeps("", registry, &fakeRunner{}, "linux")

	if status := manager.Autostart(context.Background()); status.Supported {
		t.Fatal("autostart must be unsupported off Windows")
	}
	if err := manager.SetAutostart(context.Background(), true); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SetAutostart error = %v, want ErrUnsupported", err)
	}
	if status := manager.MountStatus(context.Background(), "http://x/webdav"); status.Supported {
		t.Fatal("mount must be unsupported off Windows")
	}
	if _, err := manager.Mount(context.Background(), "http://x/webdav", "u", "p", "Z:"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Mount error = %v, want ErrUnsupported", err)
	}
	if _, err := manager.MountStatus(context.Background(), "http://x/webdav").Prerequisites.Items, error(nil); err != nil {
		t.Fatal(err)
	}
}

func TestMountStatusFindsAnExistingMapping(t *testing.T) {
	t.Parallel()
	registry := newFakeRegistry()
	registry.values[key(HiveLocalMachine, webClientServicePath, serviceStartName)] = 2
	registry.values[key(HiveLocalMachine, webClientKeyPath, basicAuthLevelName)] = 2
	registry.values[key(HiveLocalMachine, webClientKeyPath, fileSizeLimitName)] = recommendedFileSizeLimit
	runner := &fakeRunner{outputs: map[string]string{
		"net use": "OK   Z:   \\\\127.0.0.1@8080\\webdav   Web Client Network\n",
	}}
	manager := newWithDeps("", registry, runner, "windows")

	status := manager.MountStatus(context.Background(), "http://127.0.0.1:8080/webdav")
	if !status.Mounted || status.Drive != "Z:" {
		t.Fatalf("status = %+v, want mounted on Z:", status)
	}
	if !status.Prerequisites.Ready {
		t.Fatal("prerequisites should be reported as ready")
	}
}
