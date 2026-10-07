package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestWriteLauncherHintExplainsTheBundleAndTheCLI(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	writeLauncherHint(&out, "2.0.3")
	text := out.String()
	// Outside a bundle the hint must both tell the user what to do instead and
	// document the command line that remains available.
	for _, want := range []string{"2.0.3", "teldrive.exe", "teldrive run -c config.toml", "teldrive version", "teldrive --help"} {
		if !strings.Contains(text, want) {
			t.Errorf("launcher hint does not mention %q:\n%s", want, text)
		}
	}
}

func TestPauseIsSkippedWhenDisabled(t *testing.T) {
	t.Setenv(pauseEnv, "1")
	var out bytes.Buffer
	pauseWhenDoubleClicked(os.Stdin, &out)
	if out.Len() != 0 {
		t.Fatalf("expected no pause prompt, got %q", out.String())
	}
}

func TestPauseIsSkippedForNonConsoleStdin(t *testing.T) {
	// A pipe is not a character device, so scripted runs must never block.
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	var out bytes.Buffer
	pauseWhenDoubleClicked(reader, &out)
	if out.Len() != 0 {
		t.Fatalf("expected no pause prompt for piped stdin, got %q", out.String())
	}
}

func TestRootCommandWithoutSubcommandExplainsTheEntryPoint(t *testing.T) {
	t.Setenv(pauseEnv, "1")
	root := newRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{})
	// Outside a bundle this must not fail: it explains what to do instead.
	if err := root.Execute(); err != nil {
		t.Fatalf("root command without a subcommand returned %v", err)
	}
	if !strings.Contains(out.String(), "teldrive run -c config.toml") {
		t.Fatalf("root output does not document the CLI:\n%s", out.String())
	}
}
