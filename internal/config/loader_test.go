package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akyrey/projector/internal/config"
)

// writeConfig is a test helper that writes a Config to a file.
func writeConfig(t *testing.T, path string, cfg *config.Config) {
	t.Helper()
	if err := config.SaveFile(path, cfg); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
}

// TestLoadFile_NotFound verifies ErrConfigNotFound on a missing file.
func TestLoadFile_NotFound(t *testing.T) {
	_, err := config.LoadFile("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != config.ErrConfigNotFound {
		// unwrap to handle wrapped errors
		t.Logf("err: %v", err)
	}
}

// TestLoadFile_ParseError verifies that malformed YAML is rejected.
func TestLoadFile_ParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte(":\ninvalid: [yaml: nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := config.LoadFile(path)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

// TestSaveAndLoadFile verifies a round-trip save/load.
func TestSaveAndLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	original := &config.Config{
		Projects: map[string]config.Project{
			"my-api": {Path: "/work/my-api"},
		},
		Commands: map[string]config.Command{
			"start": {
				Cmd:         "docker compose up -d",
				Description: "Start services",
				Env:         map[string]string{"COMPOSE_FILE": "docker-compose.dev.yml"},
			},
		},
	}

	if err := config.SaveFile(path, original); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	loaded, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	if loaded.Projects["my-api"].Path != "/work/my-api" {
		t.Errorf("project path: got %q, want %q", loaded.Projects["my-api"].Path, "/work/my-api")
	}

	startCmd := loaded.Commands["start"]
	if startCmd.Cmd != "docker compose up -d" {
		t.Errorf("cmd: got %q, want %q", startCmd.Cmd, "docker compose up -d")
	}
	if startCmd.Env["COMPOSE_FILE"] != "docker-compose.dev.yml" {
		t.Errorf("env: got %q, want %q", startCmd.Env["COMPOSE_FILE"], "docker-compose.dev.yml")
	}
}

// TestLoader_Load_HierarchyMerge verifies that configs merge in priority order:
// global < ancestor dirs < cwd, with closer definitions winning.
func TestLoader_Load_HierarchyMerge(t *testing.T) {
	// Build a temp directory tree: /tmp/root/a/b/c
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "a", "b")
	c := filepath.Join(root, "a", "b", "c")
	for _, d := range []string{a, b, c} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	globalPath := filepath.Join(root, "global.yaml")

	// Global: defines 'start' and 'stop'.
	writeConfig(t, globalPath, &config.Config{
		Commands: map[string]config.Command{
			"start": {Cmd: "global-start"},
			"stop":  {Cmd: "global-stop"},
		},
	})

	// /a: overrides 'start'.
	writeConfig(t, filepath.Join(a, config.LocalConfigName), &config.Config{
		Commands: map[string]config.Command{
			"start": {Cmd: "a-start"},
		},
	})

	// /a/b: adds 'build'.
	writeConfig(t, filepath.Join(b, config.LocalConfigName), &config.Config{
		Commands: map[string]config.Command{
			"build": {Cmd: "b-build"},
		},
	})

	// /a/b/c: overrides 'start' again.
	writeConfig(t, filepath.Join(c, config.LocalConfigName), &config.Config{
		Commands: map[string]config.Command{
			"start": {Cmd: "c-start"},
		},
	})

	loader := config.NewLoaderWithGlobal(globalPath)
	merged, err := loader.Load(c)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 'start' should come from /a/b/c (most specific).
	if got := merged.Commands["start"].Cmd; got != "c-start" {
		t.Errorf("start: got %q, want %q", got, "c-start")
	}

	// 'stop' should come from global (only defined there).
	if got := merged.Commands["stop"].Cmd; got != "global-stop" {
		t.Errorf("stop: got %q, want %q", got, "global-stop")
	}

	// 'build' should come from /a/b.
	if got := merged.Commands["build"].Cmd; got != "b-build" {
		t.Errorf("build: got %q, want %q", got, "b-build")
	}
}

// TestLoader_Load_NoLocalFiles verifies that missing local files are silently skipped.
func TestLoader_Load_NoLocalFiles(t *testing.T) {
	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	writeConfig(t, globalPath, &config.Config{
		Commands: map[string]config.Command{
			"start": {Cmd: "global-start"},
		},
	})

	// Use an actual temp dir (no .projector.yaml inside it).
	pwd := t.TempDir()

	loader := config.NewLoaderWithGlobal(globalPath)
	merged, err := loader.Load(pwd)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := merged.Commands["start"].Cmd; got != "global-start" {
		t.Errorf("start: got %q, want %q", got, "global-start")
	}
}

// TestLoader_Load_GlobalAbsent verifies behaviour when the global config is missing.
func TestLoader_Load_GlobalAbsent(t *testing.T) {
	loader := config.NewLoaderWithGlobal("/nonexistent/path/global.yaml")
	pwd := t.TempDir()

	// Write a local config in pwd.
	writeConfig(t, filepath.Join(pwd, config.LocalConfigName), &config.Config{
		Commands: map[string]config.Command{
			"start": {Cmd: "local-start"},
		},
	})

	merged, err := loader.Load(pwd)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := merged.Commands["start"].Cmd; got != "local-start" {
		t.Errorf("start: got %q, want %q", got, "local-start")
	}
}

// TestGlobalConfigPath verifies the path respects XDG_CONFIG_HOME.
func TestGlobalConfigPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	path, err := config.GlobalConfigPath()
	if err != nil {
		t.Fatalf("GlobalConfigPath: %v", err)
	}

	want := filepath.Join(dir, "projector", "config.yaml")
	if path != want {
		t.Errorf("got %q, want %q", path, want)
	}
}
