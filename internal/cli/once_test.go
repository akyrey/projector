package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akyrey/projector/internal/cli"
	"github.com/akyrey/projector/internal/config"
)

// onceProject writes a project whose "update" command logs "update" and has
// the given once steps, and returns its dir and the log file path.
func onceProject(t *testing.T, steps ...config.OnceStep) (dir, logFile string) {
	t.Helper()
	dir = t.TempDir()
	logFile = filepath.Join(t.TempDir(), "log.txt")
	for i := range steps {
		steps[i].Cmd = config.NewStringOrList(strings.ReplaceAll(steps[i].Cmd.String(), "LOG", logFile))
	}
	cfg := &config.Config{Commands: map[string]config.Command{
		"update": {Cmd: config.NewStringOrList("echo update >> " + logFile), Once: steps},
	}}
	if err := config.SaveFile(filepath.Join(dir, config.LocalConfigName), cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return dir, logFile
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(strings.TrimSpace(string(data)), "\n", ",")
}

func execute(t *testing.T, globalPath string, args ...string) error {
	t.Helper()
	root := cli.NewRootCmdWithGlobal("test", globalPath)
	root.SetArgs(args)
	return root.Execute()
}

func step(id, cmd string) config.OnceStep {
	return config.OnceStep{ID: id, Cmd: config.NewStringOrList(cmd)}
}

func TestOnceSteps_RunOncePerMachine(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir, logFile := onceProject(t, step("s1", "echo s1 >> LOG"), step("s2", "echo s2 >> LOG"))
	globalPath := filepath.Join(t.TempDir(), "global.yaml")

	for i := 0; i < 2; i++ {
		if err := execute(t, globalPath, "--pwd", dir, "update"); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}

	if got, want := readLog(t, logFile), "update,s1,s2,update"; got != want {
		t.Errorf("log = %q, want %q", got, want)
	}
}

func TestOnceSteps_FailedStepRetriedNextRun(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	flag := filepath.Join(t.TempDir(), "fixed")
	// s2 fails until the flag file exists.
	dir, logFile := onceProject(t,
		step("s1", "echo s1 >> LOG"),
		step("s2", "test -f "+flag+" && echo s2 >> LOG"),
	)
	globalPath := filepath.Join(t.TempDir(), "global.yaml")

	if err := execute(t, globalPath, "--pwd", dir, "update"); err == nil {
		t.Fatal("first run: want error from failing once step, got nil")
	}
	if err := os.WriteFile(flag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := execute(t, globalPath, "--pwd", dir, "update"); err != nil {
		t.Fatalf("second run: %v", err)
	}

	if got, want := readLog(t, logFile), "update,s1,update,s2"; got != want {
		t.Errorf("log = %q, want %q", got, want)
	}
}

func TestOnceSteps_DryRunDoesNotRecord(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir, logFile := onceProject(t, step("s1", "echo s1 >> LOG"))
	globalPath := filepath.Join(t.TempDir(), "global.yaml")

	if err := execute(t, globalPath, "--pwd", dir, "--dry-run", "update"); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if err := execute(t, globalPath, "--pwd", dir, "update"); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got, want := readLog(t, logFile), "update,s1"; got != want {
		t.Errorf("log = %q, want %q", got, want)
	}
}

func TestOnceSteps_NamedProjectsAndCrossProjectDeps(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	libDir, libLog := onceProject(t, step("lib-1", "echo lib-1 >> LOG"))
	appDir := t.TempDir()
	appCfg := &config.Config{Commands: map[string]config.Command{
		"update": {Cmd: config.NewStringOrList("true"), DependsOn: []string{"^lib:update"}},
	}}
	if err := config.SaveFile(filepath.Join(appDir, config.LocalConfigName), appCfg); err != nil {
		t.Fatal(err)
	}

	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	for name, dir := range map[string]string{"lib": libDir, "app": appDir} {
		if err := execute(t, globalPath, "project", "add", name, dir); err != nil {
			t.Fatalf("project add: %v", err)
		}
	}

	// Via a cross-project dep, then directly by name: lib-1 runs only the first time.
	if err := execute(t, globalPath, "update", "app"); err != nil {
		t.Fatalf("update app: %v", err)
	}
	if err := execute(t, globalPath, "update", "lib"); err != nil {
		t.Fatalf("update lib: %v", err)
	}

	if got, want := readLog(t, libLog), "update,lib-1,update"; got != want {
		t.Errorf("lib log = %q, want %q", got, want)
	}
}

func TestOnceSteps_SharedAcrossSubdirSymlinkAndAlias(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir, logFile := onceProject(t, step("s1", "echo s1 >> LOG"))
	// Re-save with an alias on update.
	cfg, err := config.LoadFile(filepath.Join(dir, config.LocalConfigName))
	if err != nil {
		t.Fatal(err)
	}
	upd := cfg.Commands["update"]
	upd.Aliases = []string{"up2"}
	cfg.Commands["update"] = upd
	if err := config.SaveFile(filepath.Join(dir, config.LocalConfigName), cfg); err != nil {
		t.Fatal(err)
	}

	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}

	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	if err := execute(t, globalPath, "--pwd", dir, "update"); err != nil {
		t.Fatalf("update: %v", err)
	}
	for _, args := range [][]string{
		{"--pwd", sub, "update"},
		{"--pwd", link, "update"},
		{"--pwd", dir, "up2"},
	} {
		if err := execute(t, globalPath, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}

	if got, want := readLog(t, logFile), "update,s1,update,update,update"; got != want {
		t.Errorf("log = %q, want %q (s1 must run once)", got, want)
	}
}

func TestOnceSteps_RunWithoutMainCmd(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "log.txt")
	cfg := &config.Config{Commands: map[string]config.Command{
		"fix": {Once: []config.OnceStep{step("s1", "echo s1 >> "+logFile)}},
	}}
	if err := config.SaveFile(filepath.Join(dir, config.LocalConfigName), cfg); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	for i := 0; i < 2; i++ {
		if err := execute(t, globalPath, "--pwd", dir, "fix"); err != nil {
			t.Fatalf("fix: %v", err)
		}
	}
	if got := readLog(t, logFile); got != "s1" {
		t.Errorf("log = %q, want %q", got, "s1")
	}
}
