package runner_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/runner"
)

// followUpFixture builds a target whose main command and follow-ups append
// their name to a log file, and records which OnSuccess callbacks fired.
type followUpFixture struct {
	log      string
	recorded []string
}

func (f *followUpFixture) cmd(name string) config.Command {
	return config.Command{Cmd: config.NewStringOrList("echo " + name + " >> " + f.log)}
}

func (f *followUpFixture) followUp(name string, cmd config.Command) runner.FollowUp {
	return runner.FollowUp{
		Name:    name,
		Command: cmd,
		OnSuccess: func() error {
			f.recorded = append(f.recorded, name)
			return nil
		},
	}
}

func (f *followUpFixture) ran(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(f.log)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(strings.TrimSpace(string(data)), "\n", ",")
}

func newFollowUpFixture(t *testing.T) *followUpFixture {
	return &followUpFixture{log: filepath.Join(t.TempDir(), "log.txt")}
}

func TestRunner_FollowUps(t *testing.T) {
	fail := config.Command{Cmd: config.NewStringOrList("exit 3")}

	tests := []struct {
		name         string
		main         func(f *followUpFixture) config.Command
		followUps    func(f *followUpFixture) []runner.FollowUp
		wantErr      bool
		wantRan      string
		wantRecorded []string
	}{
		{
			name: "run in order after the main command",
			main: func(f *followUpFixture) config.Command { return f.cmd("main") },
			followUps: func(f *followUpFixture) []runner.FollowUp {
				return []runner.FollowUp{f.followUp("a", f.cmd("a")), f.followUp("b", f.cmd("b"))}
			},
			wantRan:      "main,a,b",
			wantRecorded: []string{"a", "b"},
		},
		{
			name: "skipped when the main command fails",
			main: func(*followUpFixture) config.Command { return fail },
			followUps: func(f *followUpFixture) []runner.FollowUp {
				return []runner.FollowUp{f.followUp("a", f.cmd("a"))}
			},
			wantErr: true,
			wantRan: "",
		},
		{
			name: "a failing follow-up is not recorded and stops the rest",
			main: func(f *followUpFixture) config.Command { return f.cmd("main") },
			followUps: func(f *followUpFixture) []runner.FollowUp {
				return []runner.FollowUp{
					f.followUp("a", f.cmd("a")),
					f.followUp("bad", fail),
					f.followUp("c", f.cmd("c")),
				}
			},
			wantErr:      true,
			wantRan:      "main,a",
			wantRecorded: []string{"a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFollowUpFixture(t)
			var out bytes.Buffer
			r := runner.New(nil, &out, &out)

			err := r.Run(context.Background(), runner.Target{
				Name:      "update",
				Dir:       t.TempDir(),
				Command:   tt.main(f),
				FollowUps: tt.followUps(f),
			})

			if (err != nil) != tt.wantErr {
				t.Fatalf("Run err = %v, wantErr %v", err, tt.wantErr)
			}
			if got := f.ran(t); got != tt.wantRan {
				t.Errorf("ran = %q, want %q", got, tt.wantRan)
			}
			if strings.Join(f.recorded, ",") != strings.Join(tt.wantRecorded, ",") {
				t.Errorf("recorded = %v, want %v", f.recorded, tt.wantRecorded)
			}
		})
	}
}

func TestRunner_FollowUps_OnSuccessErrorStops(t *testing.T) {
	f := newFollowUpFixture(t)
	errSave := errors.New("disk full")
	r := runner.New(nil, &bytes.Buffer{}, &bytes.Buffer{})

	err := r.Run(context.Background(), runner.Target{
		Dir:     t.TempDir(),
		Command: f.cmd("main"),
		FollowUps: []runner.FollowUp{
			{Name: "a", Command: f.cmd("a"), OnSuccess: func() error { return errSave }},
			f.followUp("b", f.cmd("b")),
		},
	})

	if !errors.Is(err, errSave) {
		t.Fatalf("err = %v, want %v", err, errSave)
	}
	if got := f.ran(t); got != "main,a" {
		t.Errorf("ran = %q, want %q", got, "main,a")
	}
}

func TestRunner_FollowUps_DryRunPrintsWithoutRecording(t *testing.T) {
	f := newFollowUpFixture(t)
	var out bytes.Buffer
	r := runner.New(nil, &out, &out)

	err := r.Run(context.Background(), runner.Target{
		Name:      "update",
		Dir:       t.TempDir(),
		Command:   f.cmd("main"),
		FollowUps: []runner.FollowUp{f.followUp("once-a", f.cmd("a"))},
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := f.ran(t); got != "" {
		t.Errorf("dry-run executed commands: %q", got)
	}
	if len(f.recorded) != 0 {
		t.Errorf("dry-run recorded %v", f.recorded)
	}
	if !strings.Contains(out.String(), "once-a") {
		t.Errorf("dry-run output does not mention the follow-up: %q", out.String())
	}
}

func TestRunner_RunWithDeps_FollowUpsRunAfterRootOnly(t *testing.T) {
	f := newFollowUpFixture(t)
	r := runner.New(nil, &bytes.Buffer{}, &bytes.Buffer{})

	install := f.cmd("install")
	update := f.cmd("update")
	update.DependsOn = []string{"install"}

	err := r.RunWithDeps(context.Background(), runner.Target{
		Name:      "update",
		Dir:       t.TempDir(),
		Command:   update,
		FollowUps: []runner.FollowUp{f.followUp("once", f.cmd("once"))},
	}, map[string]config.Command{"install": install, "update": update})
	if err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}

	if got := f.ran(t); got != "install,update,once" {
		t.Errorf("ran = %q, want %q", got, "install,update,once")
	}
}
