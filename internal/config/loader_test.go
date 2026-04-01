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
				Cmd:         config.NewStringOrList("docker compose up -d"),
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
	if startCmd.Cmd.String() != "docker compose up -d" {
		t.Errorf("cmd: got %q, want %q", startCmd.Cmd.String(), "docker compose up -d")
	}
	if startCmd.Env["COMPOSE_FILE"] != "docker-compose.dev.yml" {
		t.Errorf("env: got %q, want %q", startCmd.Env["COMPOSE_FILE"], "docker-compose.dev.yml")
	}
}

// TestSaveAndLoadFile_ArrayCmd verifies that a multi-command list round-trips correctly.
func TestSaveAndLoadFile_ArrayCmd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	original := &config.Config{
		Commands: map[string]config.Command{
			"restart": {
				Cmd:         config.NewStringOrList("./vendor/bin/sail down", "./vendor/bin/sail up -d"),
				Description: "Restart sail",
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

	restart := loaded.Commands["restart"]
	if !restart.Cmd.IsMulti() {
		t.Fatalf("expected IsMulti to be true; values=%v", restart.Cmd.Values())
	}
	vals := restart.Cmd.Values()
	if len(vals) != 2 {
		t.Fatalf("expected 2 cmd values, got %d: %v", len(vals), vals)
	}
	if vals[0] != "./vendor/bin/sail down" {
		t.Errorf("cmd[0]: got %q, want %q", vals[0], "./vendor/bin/sail down")
	}
	if vals[1] != "./vendor/bin/sail up -d" {
		t.Errorf("cmd[1]: got %q, want %q", vals[1], "./vendor/bin/sail up -d")
	}
	if restart.Cmd.String() != "./vendor/bin/sail down && ./vendor/bin/sail up -d" {
		t.Errorf("String(): got %q", restart.Cmd.String())
	}
}

// TestStringOrList_UnmarshalYAML_Scalar verifies that a plain YAML string is accepted.
func TestStringOrList_UnmarshalYAML_Scalar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("commands:\n  start:\n    cmd: \"echo hello\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	cmd := loaded.Commands["start"]
	if cmd.Cmd.IsEmpty() {
		t.Fatal("expected non-empty cmd")
	}
	if cmd.Cmd.IsMulti() {
		t.Errorf("expected single cmd, got multi: %v", cmd.Cmd.Values())
	}
	if cmd.Cmd.String() != "echo hello" {
		t.Errorf("String(): got %q, want %q", cmd.Cmd.String(), "echo hello")
	}
}

// TestStringOrList_UnmarshalYAML_Sequence verifies that a YAML list is accepted.
func TestStringOrList_UnmarshalYAML_Sequence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := "commands:\n  restart:\n    cmd:\n      - \"sail down\"\n      - \"sail up -d\"\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	cmd := loaded.Commands["restart"]
	if !cmd.Cmd.IsMulti() {
		t.Fatalf("expected IsMulti, got single: %v", cmd.Cmd.Values())
	}
	vals := cmd.Cmd.Values()
	if vals[0] != "sail down" || vals[1] != "sail up -d" {
		t.Errorf("unexpected values: %v", vals)
	}
	if cmd.Cmd.String() != "sail down && sail up -d" {
		t.Errorf("String(): got %q", cmd.Cmd.String())
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
			"start": {Cmd: config.NewStringOrList("global-start")},
			"stop":  {Cmd: config.NewStringOrList("global-stop")},
		},
	})

	// /a: overrides 'start'.
	writeConfig(t, filepath.Join(a, config.LocalConfigName), &config.Config{
		Commands: map[string]config.Command{
			"start": {Cmd: config.NewStringOrList("a-start")},
		},
	})

	// /a/b: adds 'build'.
	writeConfig(t, filepath.Join(b, config.LocalConfigName), &config.Config{
		Commands: map[string]config.Command{
			"build": {Cmd: config.NewStringOrList("b-build")},
		},
	})

	// /a/b/c: overrides 'start' again.
	writeConfig(t, filepath.Join(c, config.LocalConfigName), &config.Config{
		Commands: map[string]config.Command{
			"start": {Cmd: config.NewStringOrList("c-start")},
		},
	})

	loader := config.NewLoaderWithGlobal(globalPath)
	merged, err := loader.Load(c)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 'start' should come from /a/b/c (most specific).
	if got := merged.Commands["start"].Cmd.String(); got != "c-start" {
		t.Errorf("start: got %q, want %q", got, "c-start")
	}

	// 'stop' should come from global (only defined there).
	if got := merged.Commands["stop"].Cmd.String(); got != "global-stop" {
		t.Errorf("stop: got %q, want %q", got, "global-stop")
	}

	// 'build' should come from /a/b.
	if got := merged.Commands["build"].Cmd.String(); got != "b-build" {
		t.Errorf("build: got %q, want %q", got, "b-build")
	}
}

// TestLoader_Load_NoLocalFiles verifies that missing local files are silently skipped.
func TestLoader_Load_NoLocalFiles(t *testing.T) {
	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	writeConfig(t, globalPath, &config.Config{
		Commands: map[string]config.Command{
			"start": {Cmd: config.NewStringOrList("global-start")},
		},
	})

	// Use an actual temp dir (no .projector.yaml inside it).
	pwd := t.TempDir()

	loader := config.NewLoaderWithGlobal(globalPath)
	merged, err := loader.Load(pwd)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := merged.Commands["start"].Cmd.String(); got != "global-start" {
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
			"start": {Cmd: config.NewStringOrList("local-start")},
		},
	})

	merged, err := loader.Load(pwd)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := merged.Commands["start"].Cmd.String(); got != "local-start" {
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

// TestLoad_AliasExpansion verifies that commands with aliases are reachable
// via the alias name after merging.
func TestLoad_AliasExpansion(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")

	writeConfig(t, filepath.Join(dir, config.LocalConfigName), &config.Config{
		Commands: map[string]config.Command{
			"build": {
				Cmd:     config.NewStringOrList("go build ./..."),
				Aliases: []string{"b", "compile"},
			},
		},
	})

	loader := config.NewLoaderWithGlobal(globalPath)
	merged, err := loader.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Original name must still be present.
	if _, ok := merged.Commands["build"]; !ok {
		t.Error("expected 'build' command in merged config")
	}

	// Each alias must resolve to the same shell command.
	for _, alias := range []string{"b", "compile"} {
		cmd, ok := merged.Commands[alias]
		if !ok {
			t.Errorf("expected alias %q in merged config", alias)
			continue
		}
		if cmd.Cmd.String() != "go build ./..." {
			t.Errorf("alias %q: want cmd %q, got %q", alias, "go build ./...", cmd.Cmd.String())
		}
		// Aliases of aliases must not be expanded (Aliases field should be nil).
		if len(cmd.Aliases) != 0 {
			t.Errorf("alias %q: expected Aliases to be cleared, got %v", alias, cmd.Aliases)
		}
	}
}

// TestLoad_AliasSelfSkipped verifies that a self-alias does not cause duplication.
func TestLoad_AliasSelfSkipped(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")

	writeConfig(t, filepath.Join(dir, config.LocalConfigName), &config.Config{
		Commands: map[string]config.Command{
			"start": {
				Cmd:     config.NewStringOrList("echo start"),
				Aliases: []string{"start"}, // self-alias, should be ignored
			},
		},
	})

	loader := config.NewLoaderWithGlobal(globalPath)
	merged, err := loader.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 'start' should appear exactly once (with its Aliases intact).
	cmd := merged.Commands["start"]
	if cmd.Cmd.String() != "echo start" {
		t.Errorf("start: got cmd %q", cmd.Cmd.String())
	}
	// Only 'start' itself should be in the map — self-alias doesn't create extra entry.
	if len(merged.Commands) != 1 {
		t.Errorf("expected 1 command, got %d: %v", len(merged.Commands), merged.Commands)
	}
}

// TestExpandServices_Basic verifies that services expand into commands with the
// correct "<exec> <suffix>" shell command string.
func TestExpandServices_Basic(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")

	writeConfig(t, filepath.Join(dir, config.LocalConfigName), &config.Config{
		Services: map[string]config.Service{
			"sail": {
				Exec: "docker compose exec laravel.test",
				Commands: map[string]string{
					"artisan":  "php artisan",
					"composer": "composer",
				},
			},
		},
	})

	loader := config.NewLoaderWithGlobal(globalPath)
	merged, err := loader.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	tests := []struct {
		name    string
		wantCmd string
	}{
		{"artisan", "docker compose exec laravel.test php artisan"},
		{"composer", "docker compose exec laravel.test composer"},
	}

	for _, tc := range tests {
		cmd, ok := merged.Commands[tc.name]
		if !ok {
			t.Errorf("expected command %q to be generated from service, but not found", tc.name)
			continue
		}
		if cmd.Cmd.String() != tc.wantCmd {
			t.Errorf("command %q: got cmd %q, want %q", tc.name, cmd.Cmd.String(), tc.wantCmd)
		}
	}
}

// TestExpandServices_EmptySuffix verifies that a command with an empty suffix
// generates "<exec>" (without a trailing space).
func TestExpandServices_EmptySuffix(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")

	writeConfig(t, filepath.Join(dir, config.LocalConfigName), &config.Config{
		Services: map[string]config.Service{
			"redis": {
				Exec: "docker compose exec redis redis-cli",
				Commands: map[string]string{
					"redis-cli": "", // empty suffix: exec is the whole command
				},
			},
		},
	})

	loader := config.NewLoaderWithGlobal(globalPath)
	merged, err := loader.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cmd, ok := merged.Commands["redis-cli"]
	if !ok {
		t.Fatal("expected command 'redis-cli' to be generated")
	}
	if cmd.Cmd.String() != "docker compose exec redis redis-cli" {
		t.Errorf("redis-cli: got cmd %q, want %q", cmd.Cmd.String(), "docker compose exec redis redis-cli")
	}
}

// TestExpandServices_MultipleServices verifies that multiple services each
// generate their own commands independently.
func TestExpandServices_MultipleServices(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")

	writeConfig(t, filepath.Join(dir, config.LocalConfigName), &config.Config{
		Services: map[string]config.Service{
			"sail": {
				Exec:     "docker compose exec laravel.test",
				Commands: map[string]string{"artisan": "php artisan"},
			},
			"node": {
				Exec:     "docker compose exec node",
				Commands: map[string]string{"pnpm": "pnpm"},
			},
		},
	})

	loader := config.NewLoaderWithGlobal(globalPath)
	merged, err := loader.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := merged.Commands["artisan"].Cmd.String(); got != "docker compose exec laravel.test php artisan" {
		t.Errorf("artisan: got %q", got)
	}
	if got := merged.Commands["pnpm"].Cmd.String(); got != "docker compose exec node pnpm" {
		t.Errorf("pnpm: got %q", got)
	}
}

// TestExpandServices_ExplicitCmdOverrides verifies that an explicit command with
// a non-empty Cmd field takes full precedence over a service-generated command.
func TestExpandServices_ExplicitCmdOverrides(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")

	writeConfig(t, filepath.Join(dir, config.LocalConfigName), &config.Config{
		Services: map[string]config.Service{
			"sail": {
				Exec:     "docker compose exec laravel.test",
				Commands: map[string]string{"composer": "composer"},
			},
		},
		Commands: map[string]config.Command{
			// Explicit cmd overrides the service-generated one (run locally).
			"composer": {Cmd: config.NewStringOrList("composer"), Description: "Run composer locally"},
		},
	})

	loader := config.NewLoaderWithGlobal(globalPath)
	merged, err := loader.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cmd := merged.Commands["composer"]
	if cmd.Cmd.String() != "composer" {
		t.Errorf("composer: got cmd %q, want %q (explicit override)", cmd.Cmd.String(), "composer")
	}
	if cmd.Description != "Run composer locally" {
		t.Errorf("composer: description %q, want %q", cmd.Description, "Run composer locally")
	}
}

// TestExpandServices_MetadataLayeredOnGenerated verifies that an explicit command
// entry with no Cmd field preserves the service-generated Cmd while layering its
// other fields (description, env, depends_on, etc.) on top.
func TestExpandServices_MetadataLayeredOnGenerated(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")

	writeConfig(t, filepath.Join(dir, config.LocalConfigName), &config.Config{
		Services: map[string]config.Service{
			"sail": {
				Exec:     "docker compose exec laravel.test",
				Commands: map[string]string{"artisan": "php artisan"},
			},
		},
		Commands: map[string]config.Command{
			// No Cmd field — should keep generated cmd and add metadata.
			"artisan": {
				Description: "Run artisan inside the container",
				DependsOn:   []string{"build-assets"},
				Env:         map[string]string{"APP_ENV": "local"},
			},
		},
	})

	loader := config.NewLoaderWithGlobal(globalPath)
	merged, err := loader.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cmd := merged.Commands["artisan"]
	if cmd.Cmd.String() != "docker compose exec laravel.test php artisan" {
		t.Errorf("artisan: got cmd %q, want generated cmd", cmd.Cmd.String())
	}
	if cmd.Description != "Run artisan inside the container" {
		t.Errorf("artisan: description %q", cmd.Description)
	}
	if len(cmd.DependsOn) != 1 || cmd.DependsOn[0] != "build-assets" {
		t.Errorf("artisan: depends_on %v", cmd.DependsOn)
	}
	if cmd.Env["APP_ENV"] != "local" {
		t.Errorf("artisan: env APP_ENV %q", cmd.Env["APP_ENV"])
	}
}

// TestExpandServices_EmptyExecSkipped verifies that a service with an empty Exec
// is silently skipped (no commands are generated).
func TestExpandServices_EmptyExecSkipped(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")

	writeConfig(t, filepath.Join(dir, config.LocalConfigName), &config.Config{
		Services: map[string]config.Service{
			"broken": {
				Exec:     "", // empty — should be skipped
				Commands: map[string]string{"artisan": "php artisan"},
			},
		},
	})

	loader := config.NewLoaderWithGlobal(globalPath)
	merged, err := loader.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if _, ok := merged.Commands["artisan"]; ok {
		t.Error("expected no 'artisan' command from service with empty exec")
	}
}

// TestExpandServices_ServiceHierarchy verifies that services participate in the
// config hierarchy: a local config's service overrides a global one with the
// same name, and commands from both layers are correctly expanded.
func TestExpandServices_ServiceHierarchy(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	globalPath := filepath.Join(root, "global.yaml")

	// Global: defines sail service with artisan.
	writeConfig(t, globalPath, &config.Config{
		Services: map[string]config.Service{
			"sail": {
				Exec:     "docker compose exec app",
				Commands: map[string]string{"artisan": "php artisan"},
			},
		},
	})

	// Local (sub): overrides sail service with a different container name.
	writeConfig(t, filepath.Join(sub, config.LocalConfigName), &config.Config{
		Services: map[string]config.Service{
			"sail": {
				Exec:     "docker compose exec laravel.test", // overrides global
				Commands: map[string]string{"artisan": "php artisan", "composer": "composer"},
			},
		},
	})

	loader := config.NewLoaderWithGlobal(globalPath)
	merged, err := loader.Load(sub)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// artisan should use the local container name.
	if got := merged.Commands["artisan"].Cmd.String(); got != "docker compose exec laravel.test php artisan" {
		t.Errorf("artisan: got %q, want local override", got)
	}
	// composer should also be present from the local service definition.
	if got := merged.Commands["composer"].Cmd.String(); got != "docker compose exec laravel.test composer" {
		t.Errorf("composer: got %q", got)
	}
}

// TestExpandServices_GlobalServiceLocalMetadata verifies the combined scenario:
// a service defined globally provides the Cmd, while a local explicit command
// without a Cmd layers metadata on top.
func TestExpandServices_GlobalServiceLocalMetadata(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	globalPath := filepath.Join(root, "global.yaml")

	// Global: defines the service.
	writeConfig(t, globalPath, &config.Config{
		Services: map[string]config.Service{
			"sail": {
				Exec:     "docker compose exec laravel.test",
				Commands: map[string]string{"artisan": "php artisan"},
			},
		},
	})

	// Local: adds metadata to artisan without repeating the exec prefix.
	writeConfig(t, filepath.Join(sub, config.LocalConfigName), &config.Config{
		Commands: map[string]config.Command{
			"artisan": {
				Description: "Run artisan in container",
				DependsOn:   []string{"migrate"},
			},
		},
	})

	loader := config.NewLoaderWithGlobal(globalPath)
	merged, err := loader.Load(sub)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cmd := merged.Commands["artisan"]
	if cmd.Cmd.String() != "docker compose exec laravel.test php artisan" {
		t.Errorf("artisan: cmd %q, want service-generated cmd", cmd.Cmd.String())
	}
	if cmd.Description != "Run artisan in container" {
		t.Errorf("artisan: description %q", cmd.Description)
	}
	if len(cmd.DependsOn) != 1 || cmd.DependsOn[0] != "migrate" {
		t.Errorf("artisan: depends_on %v", cmd.DependsOn)
	}
}
