package cli_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akyrey/projector/internal/cli"
	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/project"
)

func TestStatus_ReportsPendingOnceSteps(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	apiDir, _ := onceProject(t, step("s1", "true"), step("s2", "true"))
	webDir := t.TempDir()

	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	for name, dir := range map[string]string{"api": apiDir, "web": webDir} {
		if err := execute(t, globalPath, "project", "add", name, dir); err != nil {
			t.Fatalf("project add: %v", err)
		}
	}

	statusOf := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		root := cli.NewRootCmdWithGlobal("test", globalPath)
		root.SetOut(&out)
		root.SetArgs(append([]string{"status"}, args...))
		if err := root.Execute(); err != nil {
			t.Fatalf("status: %v", err)
		}
		return out.String()
	}

	before := statusOf()
	apiLine := lineWith(before, "api")
	if !strings.Contains(apiLine, "2 pending") {
		t.Errorf("api line = %q, want 2 pending\n%s", apiLine, before)
	}
	if !strings.Contains(lineWith(before, "web"), "not a git repo") {
		t.Errorf("web should be reported as not a git repo:\n%s", before)
	}

	if err := execute(t, globalPath, "update", "api"); err != nil {
		t.Fatalf("update: %v", err)
	}
	after := statusOf("api")
	if strings.Contains(after, "pending") || strings.Contains(after, "web") {
		t.Errorf("after update, only api with no pending steps expected:\n%s", after)
	}
}

func TestStatus_UnknownProject(t *testing.T) {
	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	if err := config.SaveFile(globalPath, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	err := execute(t, globalPath, "status", "nope")
	if !errors.Is(err, project.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// lineWith returns the first line of text starting with prefix.
func lineWith(text, prefix string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

// TestStatus_ManyProjectsWithOnceSteps exercises concurrent state access under -race,
// and checks aliases are not double-counted.
func TestStatus_ManyProjectsWithOnceSteps(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	names := []string{"p1", "p2", "p3", "p4"}
	for _, name := range names {
		dir, _ := onceProject(t, step("s1", "true"))
		cfg, err := config.LoadFile(filepath.Join(dir, config.LocalConfigName))
		if err != nil {
			t.Fatal(err)
		}
		upd := cfg.Commands["update"]
		upd.Aliases = []string{"u"}
		cfg.Commands["update"] = upd
		if err := config.SaveFile(filepath.Join(dir, config.LocalConfigName), cfg); err != nil {
			t.Fatal(err)
		}
		if err := execute(t, globalPath, "project", "add", name, dir); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	root := cli.NewRootCmdWithGlobal("test", globalPath)
	root.SetOut(&out)
	root.SetArgs([]string{"status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, name := range names {
		if line := lineWith(out.String(), name); !strings.Contains(line, "1 pending") {
			t.Errorf("%s line = %q, want 1 pending", name, line)
		}
	}
}
