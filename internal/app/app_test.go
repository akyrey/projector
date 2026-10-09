package app_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akyrey/projector/internal/app"
	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/project"
	"github.com/akyrey/projector/internal/runner"
)

// fixture is an App with a private global config and registered projects.
type fixture struct {
	app    *app.App
	loader *config.Loader
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	loader := config.NewLoaderWithGlobal(filepath.Join(t.TempDir(), "global.yaml"))
	return &fixture{app: app.New(loader), loader: loader}
}

// addProject writes cfg to a new dir and registers it under name.
func (f *fixture) addProject(t *testing.T, name string, cfg *config.Config) string {
	t.Helper()
	dir := t.TempDir()
	if err := config.SaveFile(filepath.Join(dir, config.LocalConfigName), cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if name != "" {
		if err := project.NewRegistry(f.loader).Add(name, dir); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	return dir
}

func cmds(m map[string]string) *config.Config {
	cfg := &config.Config{Commands: map[string]config.Command{}}
	for name, c := range m {
		cfg.Commands[name] = config.Command{Cmd: config.NewStringOrList(c)}
	}
	return cfg
}

func bufRunner() (*runner.Runner, *bytes.Buffer) {
	var buf bytes.Buffer
	return runner.New(nil, &buf, &buf), &buf
}

func TestRun_InDirWritesToGivenRunner(t *testing.T) {
	f := newFixture(t)
	dir := f.addProject(t, "", cmds(map[string]string{"hello": "echo hi"}))
	r, out := bufRunner()

	err := f.app.Run(context.Background(), r, app.RunRequest{Command: "hello", Dir: dir, ExtraArgs: []string{"there"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "hi there" {
		t.Errorf("output = %q, want %q", got, "hi there")
	}
}

func TestRun_InProjectsRunsCrossProjectDepsFirst(t *testing.T) {
	f := newFixture(t)
	f.addProject(t, "lib", cmds(map[string]string{"build": "echo lib-build"}))
	f.addProject(t, "web", &config.Config{Commands: map[string]config.Command{
		"start": {Cmd: config.NewStringOrList("echo web-start"), DependsOn: []string{"^lib:build"}},
	}})
	r, out := bufRunner()

	if err := f.app.Run(context.Background(), r, app.RunRequest{Command: "start", Projects: []string{"web"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.Fields(out.String()); strings.Join(got, ",") != "lib-build,web-start" {
		t.Errorf("output = %q, want lib-build then web-start", out.String())
	}
}

func TestRun_Errors(t *testing.T) {
	f := newFixture(t)
	dir := f.addProject(t, "api", cmds(map[string]string{"start": "true"}))
	f.addProject(t, "a", &config.Config{Commands: map[string]config.Command{
		"x": {Cmd: config.NewStringOrList("true"), DependsOn: []string{"^b:x"}},
	}})
	f.addProject(t, "b", &config.Config{Commands: map[string]config.Command{
		"x": {Cmd: config.NewStringOrList("true"), DependsOn: []string{"^a:x"}},
	}})

	tests := []struct {
		name    string
		req     app.RunRequest
		wantErr string
		is      error
	}{
		{name: "unknown command in dir", req: app.RunRequest{Command: "nope", Dir: dir}, wantErr: `command "nope" not found`},
		{name: "unknown project", req: app.RunRequest{Command: "start", Projects: []string{"ghost"}}, wantErr: `project "ghost" is not registered`},
		{name: "cross-project cycle", req: app.RunRequest{Command: "x", Projects: []string{"a"}}, is: app.ErrCrossProjectCycle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := bufRunner()
			err := f.app.Run(context.Background(), r, tt.req)
			if err == nil {
				t.Fatal("Run succeeded, want error")
			}
			if tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantErr)
			}
			if tt.is != nil && !errors.Is(err, tt.is) {
				t.Errorf("err = %v, want errors.Is %v", err, tt.is)
			}
		})
	}
}

func TestRun_OnceStepsRunOnce(t *testing.T) {
	f := newFixture(t)
	dir := f.addProject(t, "api", &config.Config{Commands: map[string]config.Command{
		"update": {
			Cmd:  config.NewStringOrList("echo update"),
			Once: []config.OnceStep{{ID: "s1", Cmd: config.NewStringOrList("echo s1")}},
		},
	}})

	if n, err := f.app.PendingOnceCount(dir); err != nil || n != 1 {
		t.Fatalf("PendingOnceCount = %d, %v; want 1", n, err)
	}
	for i := 0; i < 2; i++ {
		r, _ := bufRunner()
		if err := f.app.Run(context.Background(), r, app.RunRequest{Command: "update", Projects: []string{"api"}}); err != nil {
			t.Fatalf("Run %d: %v", i, err)
		}
	}
	if n, err := f.app.PendingOnceCount(dir); err != nil || n != 0 {
		t.Errorf("PendingOnceCount after run = %d, %v; want 0", n, err)
	}
}

func TestProjects_SortedAndFiltered(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"web", "api", "lib"} {
		f.addProject(t, name, &config.Config{})
	}

	all, err := f.app.Projects(nil)
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	var names []string
	for _, p := range all {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "api,lib,web" {
		t.Errorf("Projects = %v, want api,lib,web", names)
	}

	if _, err := f.app.Projects([]string{"ghost"}); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("Projects(ghost) err = %v, want ErrNotFound", err)
	}
}
