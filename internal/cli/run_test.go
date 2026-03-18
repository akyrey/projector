package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akyrey/projector/internal/cli"
	"github.com/akyrey/projector/internal/config"
)

// TestCrossProjectDep_RunsBeforeMain verifies that a ^proj:cmd dep executes
// in the referenced project's directory before the main command.
func TestCrossProjectDep_RunsBeforeMain(t *testing.T) {
	// lib project: has a 'build' command that writes a marker file.
	libDir := t.TempDir()
	markerFile := filepath.Join(libDir, "built.txt")

	libCfg := &config.Config{
		Commands: map[string]config.Command{
			"build": {Cmd: "touch " + markerFile},
		},
	}
	if err := config.SaveFile(filepath.Join(libDir, config.LocalConfigName), libCfg); err != nil {
		t.Fatalf("write lib config: %v", err)
	}

	// app project: has a 'deploy' command that depends on ^lib:build,
	// and checks the marker exists.
	appDir := t.TempDir()
	appCfg := &config.Config{
		Commands: map[string]config.Command{
			"deploy": {
				Cmd:       "test -f " + markerFile,
				DependsOn: []string{"^lib:build"},
			},
		},
	}
	if err := config.SaveFile(filepath.Join(appDir, config.LocalConfigName), appCfg); err != nil {
		t.Fatalf("write app config: %v", err)
	}

	globalPath := filepath.Join(t.TempDir(), "global.yaml")

	// Register projects.
	for _, pair := range [][2]string{{"lib", libDir}, {"app", appDir}} {
		root := cli.NewRootCmdWithGlobal("test", globalPath)
		root.SetArgs([]string{"project", "add", pair[0], pair[1]})
		if err := root.Execute(); err != nil {
			t.Fatalf("project add %s: %v", pair[0], err)
		}
	}

	// Run deploy in app — should succeed because lib:build runs first.
	root := cli.NewRootCmdWithGlobal("test", globalPath)
	root.SetArgs([]string{"run", "deploy", "app"})
	if err := root.Execute(); err != nil {
		t.Fatalf("run deploy: %v", err)
	}

	// Verify lib was actually built.
	if _, err := os.Stat(markerFile); os.IsNotExist(err) {
		t.Error("lib:build did not run (marker file not created)")
	}
}

// TestCrossProjectDep_MalformedEntryReturnsError verifies that a malformed
// cross-project dep like ^proj-only (no colon) returns a clear error.
func TestCrossProjectDep_MalformedEntryReturnsError(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Commands: map[string]config.Command{
			"start": {
				Cmd:       "echo start",
				DependsOn: []string{"^bad-format"}, // missing :cmd
			},
		},
	}
	if err := config.SaveFile(filepath.Join(dir, config.LocalConfigName), cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}

	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	root := cli.NewRootCmdWithGlobal("test", globalPath)
	root.SetArgs([]string{"--pwd", dir, "run", "start"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error for malformed cross-project dep, got nil")
	}
}

// TestCrossProjectDep_DryRun verifies dry-run prints cross-project deps without executing.
func TestCrossProjectDep_DryRun(t *testing.T) {
	libDir := t.TempDir()
	markerFile := filepath.Join(libDir, "built.txt")

	libCfg := &config.Config{
		Commands: map[string]config.Command{
			"build": {Cmd: "touch " + markerFile},
		},
	}
	if err := config.SaveFile(filepath.Join(libDir, config.LocalConfigName), libCfg); err != nil {
		t.Fatalf("write lib config: %v", err)
	}

	appDir := t.TempDir()
	appCfg := &config.Config{
		Commands: map[string]config.Command{
			"deploy": {
				Cmd:       "echo deploying",
				DependsOn: []string{"^lib:build"},
			},
		},
	}
	if err := config.SaveFile(filepath.Join(appDir, config.LocalConfigName), appCfg); err != nil {
		t.Fatalf("write app config: %v", err)
	}

	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	for _, pair := range [][2]string{{"lib", libDir}, {"app", appDir}} {
		root := cli.NewRootCmdWithGlobal("test", globalPath)
		root.SetArgs([]string{"project", "add", pair[0], pair[1]})
		if err := root.Execute(); err != nil {
			t.Fatalf("project add %s: %v", pair[0], err)
		}
	}

	// Dry-run: should not create the marker file.
	root := cli.NewRootCmdWithGlobal("test", globalPath)
	root.SetArgs([]string{"--dry-run", "run", "deploy", "app"})
	if err := root.Execute(); err != nil {
		t.Fatalf("dry-run: %v", err)
	}

	if _, err := os.Stat(markerFile); !os.IsNotExist(err) {
		t.Error("dry-run should not have created the marker file")
	}
}
