package cli_test

import (
	"errors"
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
			"build": {Cmd: config.NewStringOrList("touch " + markerFile)},
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
				Cmd:       config.NewStringOrList("test -f " + markerFile),
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
				Cmd:       config.NewStringOrList("echo start"),
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
			"build": {Cmd: config.NewStringOrList("touch " + markerFile)},
		},
	}
	if err := config.SaveFile(filepath.Join(libDir, config.LocalConfigName), libCfg); err != nil {
		t.Fatalf("write lib config: %v", err)
	}

	appDir := t.TempDir()
	appCfg := &config.Config{
		Commands: map[string]config.Command{
			"deploy": {
				Cmd:       config.NewStringOrList("echo deploying"),
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

// TestCrossProjectDep_Cycle verifies that a cycle between two projects
// (A depends on ^B:start, B depends on ^A:start) returns ErrCrossProjectCycle.
func TestCrossProjectDep_Cycle(t *testing.T) {
	aDir := t.TempDir()
	bDir := t.TempDir()

	aCfg := &config.Config{
		Commands: map[string]config.Command{
			"start": {
				Cmd:       config.NewStringOrList("echo a"),
				DependsOn: []string{"^b:start"},
			},
		},
	}
	bCfg := &config.Config{
		Commands: map[string]config.Command{
			"start": {
				Cmd:       config.NewStringOrList("echo b"),
				DependsOn: []string{"^a:start"},
			},
		},
	}

	if err := config.SaveFile(filepath.Join(aDir, config.LocalConfigName), aCfg); err != nil {
		t.Fatalf("write a config: %v", err)
	}
	if err := config.SaveFile(filepath.Join(bDir, config.LocalConfigName), bCfg); err != nil {
		t.Fatalf("write b config: %v", err)
	}

	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	for _, pair := range [][2]string{{"a", aDir}, {"b", bDir}} {
		root := cli.NewRootCmdWithGlobal("test", globalPath)
		root.SetArgs([]string{"project", "add", pair[0], pair[1]})
		if err := root.Execute(); err != nil {
			t.Fatalf("project add %s: %v", pair[0], err)
		}
	}

	root := cli.NewRootCmdWithGlobal("test", globalPath)
	root.SetArgs([]string{"run", "start", "a"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected ErrCrossProjectCycle, got nil")
	}
	if !errors.Is(err, cli.ErrCrossProjectCycle) {
		t.Fatalf("expected ErrCrossProjectCycle, got: %v", err)
	}
}

// TestCrossProjectDep_ThreeHop verifies a three-hop chain:
// A:deploy depends on ^B:build, B:build depends on ^C:compile.
// Execution order must be C:compile → B:build → A:deploy.
func TestCrossProjectDep_ThreeHop(t *testing.T) {
	cDir := t.TempDir()
	bDir := t.TempDir()
	aDir := t.TempDir()

	orderFile := filepath.Join(t.TempDir(), "order.txt")

	cCfg := &config.Config{
		Commands: map[string]config.Command{
			"compile": {Cmd: config.NewStringOrList("echo c >> " + orderFile)},
		},
	}
	bCfg := &config.Config{
		Commands: map[string]config.Command{
			"build": {
				Cmd:       config.NewStringOrList("echo b >> " + orderFile),
				DependsOn: []string{"^c:compile"},
			},
		},
	}
	aCfg := &config.Config{
		Commands: map[string]config.Command{
			"deploy": {
				Cmd:       config.NewStringOrList("echo a >> " + orderFile),
				DependsOn: []string{"^b:build"},
			},
		},
	}

	for _, pair := range []struct {
		dir string
		cfg *config.Config
	}{
		{cDir, cCfg}, {bDir, bCfg}, {aDir, aCfg},
	} {
		if err := config.SaveFile(filepath.Join(pair.dir, config.LocalConfigName), pair.cfg); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}

	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	for _, pair := range [][2]string{{"c", cDir}, {"b", bDir}, {"a", aDir}} {
		root := cli.NewRootCmdWithGlobal("test", globalPath)
		root.SetArgs([]string{"project", "add", pair[0], pair[1]})
		if err := root.Execute(); err != nil {
			t.Fatalf("project add %s: %v", pair[0], err)
		}
	}

	root := cli.NewRootCmdWithGlobal("test", globalPath)
	root.SetArgs([]string{"run", "deploy", "a"})
	if err := root.Execute(); err != nil {
		t.Fatalf("run deploy: %v", err)
	}

	got, err := os.ReadFile(orderFile)
	if err != nil {
		t.Fatalf("read order file: %v", err)
	}
	want := "c\nb\na\n"
	if string(got) != want {
		t.Errorf("execution order = %q, want %q", string(got), want)
	}
}

// TestCrossProjectDep_UnregisteredProject verifies that referencing an
// unregistered project returns a clear error.
func TestCrossProjectDep_UnregisteredProject(t *testing.T) {
	appDir := t.TempDir()
	appCfg := &config.Config{
		Commands: map[string]config.Command{
			"start": {
				Cmd:       config.NewStringOrList("echo start"),
				DependsOn: []string{"^ghost:build"},
			},
		},
	}
	if err := config.SaveFile(filepath.Join(appDir, config.LocalConfigName), appCfg); err != nil {
		t.Fatalf("write config: %v", err)
	}

	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	root := cli.NewRootCmdWithGlobal("test", globalPath)
	root.SetArgs([]string{"project", "add", "app", appDir})
	if err := root.Execute(); err != nil {
		t.Fatalf("project add app: %v", err)
	}

	root = cli.NewRootCmdWithGlobal("test", globalPath)
	root.SetArgs([]string{"run", "start", "app"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error for unregistered project, got nil")
	}
}

// TestCrossProjectDep_UnknownCommand verifies that referencing a non-existent
// command in a registered project returns a clear error.
func TestCrossProjectDep_UnknownCommand(t *testing.T) {
	libDir := t.TempDir()
	libCfg := &config.Config{
		Commands: map[string]config.Command{
			"build": {Cmd: config.NewStringOrList("echo build")},
		},
	}
	if err := config.SaveFile(filepath.Join(libDir, config.LocalConfigName), libCfg); err != nil {
		t.Fatalf("write lib config: %v", err)
	}

	appDir := t.TempDir()
	appCfg := &config.Config{
		Commands: map[string]config.Command{
			"start": {
				Cmd:       config.NewStringOrList("echo start"),
				DependsOn: []string{"^lib:nonexistent"},
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

	root := cli.NewRootCmdWithGlobal("test", globalPath)
	root.SetArgs([]string{"run", "start", "app"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error for unknown command, got nil")
	}
}

// TestCrossProjectDep_DeduplicatedAcrossProjects verifies that when two
// projects both depend on the same ^lib:build, it runs exactly once.
func TestCrossProjectDep_DeduplicatedAcrossProjects(t *testing.T) {
	libDir := t.TempDir()
	counterFile := filepath.Join(t.TempDir(), "count.txt")

	libCfg := &config.Config{
		Commands: map[string]config.Command{
			// Appends a line to the counter file each time it runs.
			"build": {Cmd: config.NewStringOrList("echo x >> " + counterFile)},
		},
	}
	if err := config.SaveFile(filepath.Join(libDir, config.LocalConfigName), libCfg); err != nil {
		t.Fatalf("write lib config: %v", err)
	}

	aDir := t.TempDir()
	bDir := t.TempDir()
	for _, dir := range []string{aDir, bDir} {
		cfg := &config.Config{
			Commands: map[string]config.Command{
				"start": {
					Cmd:       config.NewStringOrList("echo start"),
					DependsOn: []string{"^lib:build"},
				},
			},
		}
		if err := config.SaveFile(filepath.Join(dir, config.LocalConfigName), cfg); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}

	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	for _, pair := range [][2]string{{"lib", libDir}, {"a", aDir}, {"b", bDir}} {
		root := cli.NewRootCmdWithGlobal("test", globalPath)
		root.SetArgs([]string{"project", "add", pair[0], pair[1]})
		if err := root.Execute(); err != nil {
			t.Fatalf("project add %s: %v", pair[0], err)
		}
	}

	root := cli.NewRootCmdWithGlobal("test", globalPath)
	root.SetArgs([]string{"run", "start", "a", "b"})
	if err := root.Execute(); err != nil {
		t.Fatalf("run start: %v", err)
	}

	got, err := os.ReadFile(counterFile)
	if err != nil {
		t.Fatalf("read counter file: %v", err)
	}
	// Each "echo x >> file" appends "x\n", so one run = "x\n".
	if string(got) != "x\n" {
		t.Errorf("lib:build ran %d time(s), want exactly 1; counter=%q",
			len(filepath.SplitList(string(got))), string(got))
	}
}

// TestCrossProjectDep_CwdMode verifies cross-project deps work when running
// without explicit project names (cwd path via --pwd).
func TestCrossProjectDep_CwdMode(t *testing.T) {
	libDir := t.TempDir()
	markerFile := filepath.Join(libDir, "ready.txt")

	libCfg := &config.Config{
		Commands: map[string]config.Command{
			"prepare": {Cmd: config.NewStringOrList("touch " + markerFile)},
		},
	}
	if err := config.SaveFile(filepath.Join(libDir, config.LocalConfigName), libCfg); err != nil {
		t.Fatalf("write lib config: %v", err)
	}

	appDir := t.TempDir()
	appCfg := &config.Config{
		Commands: map[string]config.Command{
			"start": {
				Cmd:       config.NewStringOrList("test -f " + markerFile),
				DependsOn: []string{"^lib:prepare"},
			},
		},
	}
	if err := config.SaveFile(filepath.Join(appDir, config.LocalConfigName), appCfg); err != nil {
		t.Fatalf("write app config: %v", err)
	}

	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	root := cli.NewRootCmdWithGlobal("test", globalPath)
	root.SetArgs([]string{"project", "add", "lib", libDir})
	if err := root.Execute(); err != nil {
		t.Fatalf("project add lib: %v", err)
	}

	// Run via cwd (no project name, use --pwd).
	root = cli.NewRootCmdWithGlobal("test", globalPath)
	root.SetArgs([]string{"--pwd", appDir, "run", "start"})
	if err := root.Execute(); err != nil {
		t.Fatalf("run start: %v", err)
	}

	if _, err := os.Stat(markerFile); os.IsNotExist(err) {
		t.Error("lib:prepare did not run (marker file not created)")
	}
}
