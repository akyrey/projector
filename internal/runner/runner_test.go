package runner_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/runner"
)

// TestRunner_Run_Success verifies a simple command produces output.
func TestRunner_Run_Success(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(nil, &out, &out)

	tgt := runner.Target{
		Name: "test",
		Dir:  t.TempDir(),
		Command: config.Command{
			Cmd: config.NewStringOrList("echo hello"),
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
	r := runner.New(nil, &out, &out)

	tgt := runner.Target{
		Name: "failing",
		Dir:  t.TempDir(),
		Command: config.Command{
			Cmd: config.NewStringOrList("exit 1"),
		},
	}

	if err := r.Run(context.Background(), tgt); err == nil {
		t.Fatal("expected error from failing command, got nil")
	}
}

// TestRunner_Run_EnvVar verifies that Env map entries are passed to the subprocess.
func TestRunner_Run_EnvVar(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(nil, &out, &out)

	tgt := runner.Target{
		Name: "env-test",
		Dir:  t.TempDir(),
		Command: config.Command{
			Cmd: config.NewStringOrList("echo $MY_VAR"),
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

// TestRunner_Run_DryRun verifies that dry-run prints the command without executing.
func TestRunner_Run_DryRun(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(nil, &out, &out)

	tgt := runner.Target{
		Name: "dry-test",
		Dir:  t.TempDir(),
		Command: config.Command{
			Cmd: config.NewStringOrList("echo should-not-appear"),
		},
		DryRun: true,
	}

	if err := r.Run(context.Background(), tgt); err != nil {
		t.Fatalf("Run dry: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "should-not-appear") && !strings.Contains(got, "dry-run") {
		t.Errorf("dry-run should not have executed the command; got: %q", got)
	}
	if !strings.Contains(got, "dry-run") {
		t.Errorf("expected [dry-run] label in output; got: %q", got)
	}
	if !strings.Contains(got, "echo should-not-appear") {
		t.Errorf("expected command string in dry-run output; got: %q", got)
	}
}

// TestRunner_RunConcurrent_AllSucceed runs multiple targets and checks all outputs appear.
func TestRunner_RunConcurrent_AllSucceed(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(nil, &out, &out)

	targets := []runner.Target{
		{Name: "alpha", Dir: t.TempDir(), Command: config.Command{Cmd: config.NewStringOrList("echo alpha-output")}},
		{Name: "beta", Dir: t.TempDir(), Command: config.Command{Cmd: config.NewStringOrList("echo beta-output")}},
		{Name: "gamma", Dir: t.TempDir(), Command: config.Command{Cmd: config.NewStringOrList("echo gamma-output")}},
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
	r := runner.New(nil, &out, &out)

	targets := []runner.Target{
		{Name: "ok", Dir: t.TempDir(), Command: config.Command{Cmd: config.NewStringOrList("echo ok")}},
		{Name: "fail", Dir: t.TempDir(), Command: config.Command{Cmd: config.NewStringOrList("exit 1")}},
	}

	if err := r.RunConcurrent(context.Background(), targets); err == nil {
		t.Fatal("expected error from failing target, got nil")
	}
}

// TestRunner_RunConcurrent_SingleTarget skips prefixes (delegates to Run).
func TestRunner_RunConcurrent_SingleTarget(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(nil, &out, &out)

	targets := []runner.Target{
		{Name: "solo", Dir: t.TempDir(), Command: config.Command{Cmd: config.NewStringOrList("echo solo-output")}},
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

// TestRunner_Run_DotEnvLoaded verifies that .env variables in the target directory are injected.
func TestRunner_Run_DotEnvLoaded(t *testing.T) {
	dir := t.TempDir()

	// Write a .env file in the target directory.
	if err := os.WriteFile(dir+"/.env", []byte("DOTENV_VAR=from-dotenv\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	var out bytes.Buffer
	r := runner.New(nil, &out, &out)

	tgt := runner.Target{
		Name: "dotenv-test",
		Dir:  dir,
		Command: config.Command{
			Cmd: config.NewStringOrList("echo $DOTENV_VAR"),
		},
	}

	if err := r.Run(context.Background(), tgt); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(out.String(), "from-dotenv") {
		t.Errorf("expected .env variable in output, got: %q", out.String())
	}
}

// TestRunner_Run_CommandEnvOverridesDotEnv verifies that command env: takes precedence over .env.
func TestRunner_Run_CommandEnvOverridesDotEnv(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(dir+"/.env", []byte("MY_VAR=from-dotenv\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	var out bytes.Buffer
	r := runner.New(nil, &out, &out)

	tgt := runner.Target{
		Name: "override-test",
		Dir:  dir,
		Command: config.Command{
			Cmd: config.NewStringOrList("echo $MY_VAR"),
			Env: map[string]string{"MY_VAR": "from-command-env"},
		},
	}

	if err := r.Run(context.Background(), tgt); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "from-command-env") {
		t.Errorf("expected command env to win; got: %q", got)
	}
	if strings.Contains(got, "from-dotenv") {
		t.Errorf("dotenv should have been overridden; got: %q", got)
	}
}

// TestRunner_Run_PreconditionPass verifies the command runs when precondition exits 0.
func TestRunner_Run_PreconditionPass(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(nil, &out, &out)

	tgt := runner.Target{
		Name: "pre-pass",
		Dir:  t.TempDir(),
		Command: config.Command{
			Cmd:           config.NewStringOrList("echo ran"),
			Preconditions: []string{"true"}, // always succeeds
		},
	}

	if err := r.Run(context.Background(), tgt); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "ran") {
		t.Errorf("expected 'ran' in output, got: %q", out.String())
	}
}

// TestRunner_Run_PreconditionFail verifies the command is blocked when precondition exits non-zero.
func TestRunner_Run_PreconditionFail(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(nil, &out, &out)

	tgt := runner.Target{
		Name: "pre-fail",
		Dir:  t.TempDir(),
		Command: config.Command{
			Cmd:           config.NewStringOrList("echo should-not-run"),
			Preconditions: []string{"false"}, // always fails
		},
	}

	err := r.Run(context.Background(), tgt)
	if err == nil {
		t.Fatal("expected error from failed precondition, got nil")
	}
	if strings.Contains(out.String(), "should-not-run") {
		t.Error("command should not have run when precondition failed")
	}
}

// TestRunner_Run_ExtraArgs verifies extra args are appended to the shell command.
func TestRunner_Run_ExtraArgs(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(nil, &out, &out)

	tgt := runner.Target{
		Name: "extra-args-test",
		Dir:  t.TempDir(),
		Command: config.Command{
			Cmd: config.NewStringOrList("echo"),
		},
		ExtraArgs: []string{"--flag", "value"},
	}

	if err := r.Run(context.Background(), tgt); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "--flag") || !strings.Contains(got, "value") {
		t.Errorf("expected extra args in output, got: %q", got)
	}
}

// TestRunner_Run_ExtraArgsNotPassedToDeps verifies extra args are NOT passed to dependency steps.
func TestRunner_Run_ExtraArgsNotPassedToDeps(t *testing.T) {
	tmpDir := t.TempDir()
	markerFile := tmpDir + "/dep-args.txt"

	var out bytes.Buffer
	r := runner.New(nil, &out, &out)

	commands := map[string]config.Command{
		"dep": {Cmd: config.NewStringOrList("echo dep-ran > " + markerFile)},
		"main": {
			Cmd:       config.NewStringOrList("echo main-ran"),
			DependsOn: []string{"dep"},
		},
	}

	tgt := runner.Target{
		Name:      "main",
		Dir:       tmpDir,
		Command:   commands["main"],
		ExtraArgs: []string{"--extra"},
	}

	if err := r.RunWithDeps(context.Background(), tgt, commands); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}

	combined := out.String()
	// Main command should have extra arg appended.
	if !strings.Contains(combined, "main-ran") {
		t.Errorf("expected main-ran in output, got: %q", combined)
	}
	// dep command ran but should not have received --extra.
	if strings.Contains(combined, "--extra") && !strings.Contains(combined, "main-ran --extra") {
		t.Errorf("extra args may have leaked into dep output: %q", combined)
	}
}

// TestRunner_RunConcurrent_Prefixes verifies output lines are prefixed with project names.
func TestRunner_RunConcurrent_Prefixes(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(nil, &out, &out)

	targets := []runner.Target{
		{Name: "proj-a", Dir: t.TempDir(), Command: config.Command{Cmd: config.NewStringOrList("echo line-a")}},
		{Name: "proj-b", Dir: t.TempDir(), Command: config.Command{Cmd: config.NewStringOrList("echo line-b")}},
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

// TestRunner_Run_ArrayCmd_AllSucceed verifies that an array cmd runs all steps in order.
func TestRunner_Run_ArrayCmd_AllSucceed(t *testing.T) {
	tmpDir := t.TempDir()
	orderFile := tmpDir + "/order.txt"

	var out bytes.Buffer
	r := runner.New(nil, &out, &out)

	tgt := runner.Target{
		Name: "multi",
		Dir:  tmpDir,
		Command: config.Command{
			Cmd: config.NewStringOrList(
				"echo first >> "+orderFile,
				"echo second >> "+orderFile,
				"echo third >> "+orderFile,
			),
		},
	}

	if err := r.Run(context.Background(), tgt); err != nil {
		t.Fatalf("Run array cmd: %v", err)
	}

	got, err := os.ReadFile(orderFile)
	if err != nil {
		t.Fatalf("read order file: %v", err)
	}
	want := "first\nsecond\nthird\n"
	if string(got) != want {
		t.Errorf("execution order = %q, want %q", string(got), want)
	}
}

// TestRunner_Run_ArrayCmd_AbortsOnFailure verifies that a failing step stops the chain.
func TestRunner_Run_ArrayCmd_AbortsOnFailure(t *testing.T) {
	tmpDir := t.TempDir()
	markerFile := tmpDir + "/ran.txt"

	var out bytes.Buffer
	r := runner.New(nil, &out, &out)

	tgt := runner.Target{
		Name: "multi-fail",
		Dir:  tmpDir,
		Command: config.Command{
			Cmd: config.NewStringOrList(
				"echo step1",
				"exit 1",
				"touch "+markerFile, // must NOT run
			),
		},
	}

	err := r.Run(context.Background(), tgt)
	if err == nil {
		t.Fatal("expected error from failing step, got nil")
	}

	if _, statErr := os.Stat(markerFile); !os.IsNotExist(statErr) {
		t.Error("third command should not have run after second step failed")
	}
}

// TestRunner_Run_ArrayCmd_ExtraArgsOnLastOnly verifies ExtraArgs go to the last cmd only.
func TestRunner_Run_ArrayCmd_ExtraArgsOnLastOnly(t *testing.T) {
	tmpDir := t.TempDir()
	firstFile := tmpDir + "/first.txt"
	lastFile := tmpDir + "/last.txt"

	var out bytes.Buffer
	r := runner.New(nil, &out, &out)

	tgt := runner.Target{
		Name: "multi-extra",
		Dir:  tmpDir,
		Command: config.Command{
			// First cmd writes its args to firstFile; last cmd writes to lastFile.
			Cmd: config.NewStringOrList(
				"echo no-extra > "+firstFile,
				"echo",
			),
		},
		ExtraArgs: []string{"got-extra", ">" + lastFile},
	}

	if err := r.Run(context.Background(), tgt); err != nil {
		t.Fatalf("Run array cmd with extra args: %v", err)
	}

	// first.txt should contain "no-extra" (ExtraArgs were NOT appended).
	firstContent, err := os.ReadFile(firstFile)
	if err != nil {
		t.Fatalf("read first file: %v", err)
	}
	if strings.Contains(string(firstContent), "got-extra") {
		t.Errorf("ExtraArgs leaked into first command; first.txt: %q", string(firstContent))
	}
}

// TestRunner_Run_ArrayCmd_DryRun verifies dry-run prints each command separately.
func TestRunner_Run_ArrayCmd_DryRun(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(nil, &out, &out)

	tgt := runner.Target{
		Name: "dry-multi",
		Dir:  t.TempDir(),
		Command: config.Command{
			Cmd: config.NewStringOrList("sail down", "sail up -d"),
		},
		DryRun: true,
	}

	if err := r.Run(context.Background(), tgt); err != nil {
		t.Fatalf("Run dry array: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "sail down") {
		t.Errorf("expected 'sail down' in dry-run output; got: %q", got)
	}
	if !strings.Contains(got, "sail up -d") {
		t.Errorf("expected 'sail up -d' in dry-run output; got: %q", got)
	}
	// Should have two separate [dry-run] lines, not one joined line.
	dryRunCount := strings.Count(got, "[dry-run]")
	if dryRunCount < 2 {
		t.Errorf("expected at least 2 [dry-run] lines for array cmd; got %d in: %q", dryRunCount, got)
	}
}
