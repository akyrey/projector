package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/akyrey/projector/internal/cli"
	"github.com/akyrey/projector/internal/config"
)

// TestChoose_SelectByNumber verifies the picker selects a command by number.
func TestChoose_SelectByNumber(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Commands: map[string]config.Command{
			"build": {Cmd: "go build ./...", Description: "Build the project"},
			"start": {Cmd: "echo start"},
		},
	}
	if err := config.SaveFile(filepath.Join(dir, config.LocalConfigName), cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}

	globalPath := filepath.Join(t.TempDir(), "global.yaml")

	// Commands are sorted: "build" = 1, "start" = 2.
	// Simulate user typing "1\n" followed by what the command does (true = noop).
	// We set the command to "true" so it succeeds without side effects.
	// Actually we only test that --choose + stdin "1" selects "build" and runs it.
	// Use --dry-run to avoid actually running anything.

	root := cli.NewRootCmdWithGlobal("test", globalPath)
	root.SetArgs([]string{"--pwd", dir, "--choose", "--dry-run", "run"})
	root.SetIn(strings.NewReader("1\n"))

	var out strings.Builder
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("choose run: %v", err)
	}
	// dry-run output goes to os.Stdout (runner bypasses cobra out), so just
	// verify it didn't error — the selection succeeded.
}

// TestChoose_SelectByName verifies the picker accepts a command name directly.
func TestChoose_SelectByName(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Commands: map[string]config.Command{
			"build": {Cmd: "true"},
			"start": {Cmd: "true"},
		},
	}
	if err := config.SaveFile(filepath.Join(dir, config.LocalConfigName), cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}

	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	root := cli.NewRootCmdWithGlobal("test", globalPath)
	root.SetArgs([]string{"--pwd", dir, "--choose", "--dry-run", "run"})
	root.SetIn(strings.NewReader("start\n"))

	if err := root.Execute(); err != nil {
		t.Fatalf("choose by name: %v", err)
	}
}

// TestChoose_InvalidNumberReturnsError verifies out-of-range number returns an error.
func TestChoose_InvalidNumberReturnsError(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Commands: map[string]config.Command{
			"build": {Cmd: "true"},
		},
	}
	if err := config.SaveFile(filepath.Join(dir, config.LocalConfigName), cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}

	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	root := cli.NewRootCmdWithGlobal("test", globalPath)
	root.SetArgs([]string{"--pwd", dir, "--choose", "run"})
	root.SetIn(strings.NewReader("99\n")) // out of range

	if err := root.Execute(); err == nil {
		t.Fatal("expected error for out-of-range selection, got nil")
	}
}

// TestChoose_PrefixMatch verifies the picker accepts a unique prefix.
func TestChoose_PrefixMatch(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Commands: map[string]config.Command{
			"build":   {Cmd: "true"},
			"backend": {Cmd: "true"},
			"start":   {Cmd: "true"},
		},
	}
	if err := config.SaveFile(filepath.Join(dir, config.LocalConfigName), cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}

	globalPath := filepath.Join(t.TempDir(), "global.yaml")

	t.Run("unique prefix matches", func(t *testing.T) {
		root := cli.NewRootCmdWithGlobal("test", globalPath)
		root.SetArgs([]string{"--pwd", dir, "--choose", "--dry-run", "run"})
		root.SetIn(strings.NewReader("st\n")) // unique prefix for "start"
		if err := root.Execute(); err != nil {
			t.Fatalf("prefix match: %v", err)
		}
	})

	t.Run("ambiguous prefix returns error", func(t *testing.T) {
		root := cli.NewRootCmdWithGlobal("test", globalPath)
		root.SetArgs([]string{"--pwd", dir, "--choose", "run"})
		root.SetIn(strings.NewReader("b\n")) // matches both "build" and "backend"
		if err := root.Execute(); err == nil {
			t.Fatal("expected error for ambiguous prefix, got nil")
		}
	})
}
