package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tgdrive/teldrive/v2/internal/desktop"
)

// TestSafeFileNameCannotLeaveTheDownloadFolder is the property that matters: the
// name comes out of the database, and a save must never write somewhere else.
// The exact result of a Windows-only path depends on the platform the tests run
// on, so the assertion is the property, not the spelling.
func TestSafeFileNameCannotLeaveTheDownloadFolder(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"report.pdf",
		"中文名字.zip",
		"../../etc/passwd",
		`C:\Windows\system32\cmd.exe`,
		"a:b*c?.txt",
		"..",
		".",
		"",
		"line\nbreak.txt",
		strings.Repeat("long", 200) + ".bin",
	} {
		got := safeFileName(name)
		if got == "" || got == "." || got == ".." {
			t.Fatalf("safeFileName(%q) = %q, which is not a usable file name", name, got)
		}
		if strings.ContainsAny(got, `/\:`) {
			t.Fatalf("safeFileName(%q) = %q, which still contains a path separator", name, got)
		}
		if strings.ContainsRune(got, '\n') {
			t.Fatalf("safeFileName(%q) = %q, which still contains a control character", name, got)
		}
		if len(got) > maxSaveName {
			t.Fatalf("safeFileName(%q) kept %d bytes", name, len(got))
		}
		if !strings.HasSuffix(got, filepath.Ext(got)) {
			t.Fatalf("safeFileName(%q) = %q, which lost its extension", name, got)
		}
	}
	for _, test := range []struct{ name, want string }{
		{"report.pdf", "report.pdf"},
		{"中文名字.zip", "中文名字.zip"},
		{"..", fallbackSaveName},
		{".", fallbackSaveName},
		{"", fallbackSaveName},
	} {
		if got := safeFileName(test.name); got != test.want {
			t.Errorf("safeFileName(%q) = %q, want %q", test.name, got, test.want)
		}
	}
}

// TestUniquePathNumbersInsteadOfOverwriting is what a person expects from a
// download: two files with the same name must both survive.
func TestUniquePathNumbersInsteadOfOverwriting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first, err := uniquePath(dir, "report.pdf")
	if err != nil {
		t.Fatalf("uniquePath: %v", err)
	}
	if first != filepath.Join(dir, "report.pdf") {
		t.Fatalf("first path = %q", first)
	}
	if err := os.WriteFile(first, []byte("one"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	second, err := uniquePath(dir, "report.pdf")
	if err != nil {
		t.Fatalf("uniquePath: %v", err)
	}
	if second != filepath.Join(dir, "report (1).pdf") {
		t.Fatalf("second path = %q, want the numbered name", second)
	}
	if err := os.WriteFile(second, []byte("two"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	third, err := uniquePath(dir, "report.pdf")
	if err != nil {
		t.Fatalf("uniquePath: %v", err)
	}
	if third != filepath.Join(dir, "report (2).pdf") {
		t.Fatalf("third path = %q", third)
	}
}

// TestDesktopTargetDirUsesTheConfiguredFolder keeps the default honest: without a
// picker and without the "ask every time" switch, a save goes to the folder the
// settings page shows.
func TestDesktopTargetDirUsesTheConfiguredFolder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := desktop.NewSettingsStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if err := store.Set(desktop.Settings{DownloadDir: dir}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	handler := &desktopHandler{store: store}
	target, ok := handler.targetDir()
	if !ok || target != dir {
		t.Fatalf("targetDir = %q, %v; want the configured folder", target, ok)
	}
}

// TestDesktopTargetDirAsksWhenItShould is the other half: the switch has to make
// the picker appear, and a cancelled picker has to cancel the save rather than
// silently falling back to the default folder.
func TestDesktopTargetDirAsksWhenItShould(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := desktop.NewSettingsStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if err := store.Set(desktop.Settings{DownloadDir: dir, AskEveryTime: true}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	asked := false
	handler := &desktopHandler{store: store, shell: stubBridge{answer: func() (string, bool) {
		asked = true
		return "", false
	}}}
	if _, ok := handler.targetDir(); ok {
		t.Fatal("a cancelled picker still produced a target folder")
	}
	if !asked {
		t.Fatal("AskEveryTime did not ask")
	}
}

// stubBridge answers the picker without a screen.
type stubBridge struct {
	answer func() (string, bool)
}

func (b stubBridge) ChooseFolder(string, string) (string, bool) {
	if b.answer == nil {
		return "", false
	}
	return b.answer()
}
