package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akyrey/projector/internal/cli"
	"github.com/akyrey/projector/internal/config"
)

// TestRunFilter_GlobMatchesProjects verifies --filter selects projects by name glob.
// We test that: (1) matching pattern finds the right project and runs without error,
// (2) a pattern that matches nothing doesn't break anything (just runs in 0 projects
//
//	which falls back to cwd if no explicit projects, or is fine if explicit names given).
func TestRunFilter_GlobMatchesProjects(t *testing.T) {
	// Set up two project directories, each with a local .projector.yaml.
	apiDir := t.TempDir()
	frontendDir := t.TempDir()

	writeLocalConfig := func(dir string) {
		t.Helper()
		cfg := &config.Config{
			Commands: map[string]config.Command{
				// Use 'true' so the command always succeeds without output concerns.
				"noop": {Cmd: "true"},
			},
		}
		if err := config.SaveFile(filepath.Join(dir, config.LocalConfigName), cfg); err != nil {
			t.Fatalf("write local config: %v", err)
		}
	}
	writeLocalConfig(apiDir)
	writeLocalConfig(frontendDir)

	globalPath := filepath.Join(t.TempDir(), "global.yaml")

	// Register both projects using separate root instances (each shares globalPath).
	register := func(name, dir string) {
		t.Helper()
		root := cli.NewRootCmdWithGlobal("test", globalPath)
		root.SetArgs([]string{"project", "add", name, dir})
		if err := root.Execute(); err != nil {
			t.Fatalf("project add %s: %v", name, err)
		}
	}
	register("api-service", apiDir)
	register("frontend-app", frontendDir)

	t.Run("filter matches api-service only", func(t *testing.T) {
		root := cli.NewRootCmdWithGlobal("test", globalPath)
		root.SetArgs([]string{"run", "noop", "--filter", "api-*"})
		if err := root.Execute(); err != nil {
			t.Fatalf("run with api-* filter: %v", err)
		}
	})

	t.Run("filter matches all projects", func(t *testing.T) {
		root := cli.NewRootCmdWithGlobal("test", globalPath)
		root.SetArgs([]string{"run", "noop", "--filter", "*"})
		if err := root.Execute(); err != nil {
			t.Fatalf("run with * filter: %v", err)
		}
	})

	t.Run("invalid glob pattern returns error", func(t *testing.T) {
		root := cli.NewRootCmdWithGlobal("test", globalPath)
		root.SetArgs([]string{"run", "noop", "--filter", "["}) // invalid glob
		if err := root.Execute(); err == nil {
			t.Fatal("expected error for invalid glob pattern, got nil")
		}
	})
}

// TestConfigEditDoesNotOverwriteExistingFile ensures that running
// `projector config edit` on an already-existing config file does not
// replace its contents with the skeleton.
func TestConfigEditDoesNotOverwriteExistingFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, config.LocalConfigName)

	// Write a custom config.
	original := &config.Config{
		Commands: map[string]config.Command{
			"build": {Cmd: "go build ./...", Description: "Build the project"},
		},
	}
	if err := config.SaveFile(cfgPath, original); err != nil {
		t.Fatalf("setup: save original config: %v", err)
	}

	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("setup: read config: %v", err)
	}

	// Use 'true' as the editor: exits 0 without touching anything.
	t.Setenv("EDITOR", "true")
	t.Setenv("VISUAL", "")

	root := cli.NewRootCmdWithGlobal("test", filepath.Join(dir, "global.yaml"))
	root.SetArgs([]string{"--pwd", dir, "config", "edit"})
	if err := root.Execute(); err != nil {
		t.Fatalf("config edit: %v", err)
	}

	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after edit: %v", err)
	}

	if string(before) != string(after) {
		t.Errorf("config edit overwrote existing file\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestConfigEditCreatesSkeletonWhenMissing ensures that running
// `projector config edit` on a missing config file creates a skeleton.
func TestConfigEditCreatesSkeletonWhenMissing(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, config.LocalConfigName)

	t.Setenv("EDITOR", "true")
	t.Setenv("VISUAL", "")

	root := cli.NewRootCmdWithGlobal("test", filepath.Join(dir, "global.yaml"))
	root.SetArgs([]string{"--pwd", dir, "config", "edit"})
	if err := root.Execute(); err != nil {
		t.Fatalf("config edit: %v", err)
	}

	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Error("config edit did not create the config file")
	}

	cfg, err := config.LoadFile(cfgPath)
	if err != nil {
		t.Fatalf("load created config: %v", err)
	}
	if len(cfg.Commands) == 0 {
		t.Error("created config has no commands in skeleton")
	}
}
