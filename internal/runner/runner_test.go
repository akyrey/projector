package runner_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/runner"
)

// TestRunner_Run_Success verifies a simple command produces output.
func TestRunner_Run_Success(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(&out, &out)

	tgt := runner.Target{
		Name: "test",
		Dir:  t.TempDir(),
		Command: config.Command{
			Cmd: "echo hello",
		},
	}

	if err := r.Run(context.Background(), tgt); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(out.String(), "hello") {
		t.Errorf("expected 'hello' in output, got: %q", out.String())
	}
}

// TestRunner_Run_Failure returns an error when the command exits non-zero.
func TestRunner_Run_Failure(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(&out, &out)

	tgt := runner.Target{
		Name: "failing",
		Dir:  t.TempDir(),
		Command: config.Command{
			Cmd: "exit 1",
		},
	}

	if err := r.Run(context.Background(), tgt); err == nil {
		t.Fatal("expected error from failing command, got nil")
	}
}

// TestRunner_Run_EnvVar verifies that Env map entries are passed to the subprocess.
func TestRunner_Run_EnvVar(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(&out, &out)

	tgt := runner.Target{
		Name: "env-test",
		Dir:  t.TempDir(),
		Command: config.Command{
			Cmd: "echo $MY_VAR",
			Env: map[string]string{
				"MY_VAR": "secret-value",
			},
		},
	}

	if err := r.Run(context.Background(), tgt); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(out.String(), "secret-value") {
		t.Errorf("expected env var in output, got: %q", out.String())
	}
}

// TestRunner_RunConcurrent_AllSucceed runs multiple targets and checks all outputs appear.
func TestRunner_RunConcurrent_AllSucceed(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(&out, &out)

	targets := []runner.Target{
		{Name: "alpha", Dir: t.TempDir(), Command: config.Command{Cmd: "echo alpha-output"}},
		{Name: "beta", Dir: t.TempDir(), Command: config.Command{Cmd: "echo beta-output"}},
		{Name: "gamma", Dir: t.TempDir(), Command: config.Command{Cmd: "echo gamma-output"}},
	}

	if err := r.RunConcurrent(context.Background(), targets); err != nil {
		t.Fatalf("RunConcurrent: %v", err)
	}

	combined := out.String()
	for _, want := range []string{"alpha-output", "beta-output", "gamma-output"} {
		if !strings.Contains(combined, want) {
			t.Errorf("expected %q in output; got:\n%s", want, combined)
		}
	}
}

// TestRunner_RunConcurrent_OneFailure returns an error if any target fails.
func TestRunner_RunConcurrent_OneFailure(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(&out, &out)

	targets := []runner.Target{
		{Name: "ok", Dir: t.TempDir(), Command: config.Command{Cmd: "echo ok"}},
		{Name: "fail", Dir: t.TempDir(), Command: config.Command{Cmd: "exit 1"}},
	}

	if err := r.RunConcurrent(context.Background(), targets); err == nil {
		t.Fatal("expected error from failing target, got nil")
	}
}

// TestRunner_RunConcurrent_SingleTarget skips prefixes (delegates to Run).
func TestRunner_RunConcurrent_SingleTarget(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(&out, &out)

	targets := []runner.Target{
		{Name: "solo", Dir: t.TempDir(), Command: config.Command{Cmd: "echo solo-output"}},
	}

	if err := r.RunConcurrent(context.Background(), targets); err != nil {
		t.Fatalf("RunConcurrent single: %v", err)
	}

	// Single target uses Run (no prefix), so output should not have a bracket prefix.
	got := out.String()
	if !strings.Contains(got, "solo-output") {
		t.Errorf("expected 'solo-output', got: %q", got)
	}
}

// TestRunner_RunConcurrent_Prefixes verifies output lines are prefixed with project names.
func TestRunner_RunConcurrent_Prefixes(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(&out, &out)

	targets := []runner.Target{
		{Name: "proj-a", Dir: t.TempDir(), Command: config.Command{Cmd: "echo line-a"}},
		{Name: "proj-b", Dir: t.TempDir(), Command: config.Command{Cmd: "echo line-b"}},
	}

	if err := r.RunConcurrent(context.Background(), targets); err != nil {
		t.Fatalf("RunConcurrent: %v", err)
	}

	combined := out.String()
	// Each line should contain the project prefix (stripped of ANSI for testing, we just
	// check for the bracket-wrapped name).
	if !strings.Contains(combined, "proj-a") {
		t.Errorf("expected 'proj-a' prefix in output; got:\n%s", combined)
	}
	if !strings.Contains(combined, "proj-b") {
		t.Errorf("expected 'proj-b' prefix in output; got:\n%s", combined)
	}
}
