//go:build !windows

package runner_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/runner"
)

// waitForFile polls until path exists and returns its trimmed content.
func waitForFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(data)) > 0 {
			return strings.TrimSpace(string(data))
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
	return ""
}

func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// TestWithProcessGroup_CancelKillsGrandchildren checks that cancelling a run
// stops background children of the shell too, and that Run returns instead of
// waiting on the output pipe they hold open.
func TestWithProcessGroup_CancelKillsGrandchildren(t *testing.T) {
	run := map[string]func(context.Context, *runner.Runner, runner.Target) error{
		"Run": func(ctx context.Context, r *runner.Runner, t runner.Target) error { return r.Run(ctx, t) },
		"RunConcurrent": func(ctx context.Context, r *runner.Runner, t runner.Target) error {
			other := runner.Target{Name: "other", Dir: t.Dir, Command: config.Command{Cmd: config.NewStringOrList("true")}}
			return r.RunConcurrent(ctx, []runner.Target{t, other})
		},
	}
	for name, fn := range run {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "pid")
			target := runner.Target{
				Name:    "logs",
				Dir:     dir,
				Command: config.Command{Cmd: config.NewStringOrList("sleep 30 & echo $! > " + pidFile + "; wait")},
			}
			var out bytes.Buffer
			r := runner.New(nil, &out, &out, runner.WithProcessGroup())

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- fn(ctx, r, target) }()

			pid, err := strconv.Atoi(waitForFile(t, pidFile))
			if err != nil {
				t.Fatal(err)
			}
			cancel()

			select {
			case err := <-done:
				if err == nil {
					t.Error("cancelled run returned nil error")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("run did not return after cancel")
			}

			deadline := time.Now().Add(3 * time.Second)
			for alive(pid) && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if alive(pid) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				t.Errorf("grandchild %d survived cancel", pid)
			}
		})
	}
}

// TestWithProcessGroup_KillsChildrenIgnoringSIGTERM checks that a child that
// ignores SIGTERM is killed once the grace period ends.
func TestWithProcessGroup_KillsChildrenIgnoringSIGTERM(t *testing.T) {
	defer runner.SetKillGrace(300 * time.Millisecond)()

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	target := runner.Target{
		Name:    "stubborn",
		Dir:     dir,
		Command: config.Command{Cmd: config.NewStringOrList(`sh -c "trap '' TERM; echo \$\$ > ` + pidFile + `; while :; do sleep 1; done" & wait`)},
	}
	var out bytes.Buffer
	r := runner.New(nil, &out, &out, runner.WithProcessGroup())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx, target) }()

	pid, err := strconv.Atoi(waitForFile(t, pidFile))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after cancel")
	}

	deadline := time.Now().Add(3 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Errorf("child %d ignoring SIGTERM survived", pid)
	}
}
