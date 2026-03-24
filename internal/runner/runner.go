// Package runner handles executing shell commands, both in a single directory
// and concurrently across multiple named projects.
package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/akyrey/projector/internal/config"
)

// Target represents a single execution unit: a named context + directory + command.
type Target struct {
	// Name is the display label for this target (project name, or directory).
	Name string

	// Dir is the working directory in which to run the command.
	Dir string

	// Command is the resolved command definition to execute.
	Command config.Command

	// ExtraArgs are additional arguments appended to Command.Cmd at runtime.
	// They are shell-quoted and joined with a space before appending.
	ExtraArgs []string

	// DryRun, when true, prints what would be executed without actually running.
	DryRun bool
}

// Runner executes commands, handling both single and multi-target scenarios.
type Runner struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

// New creates a Runner with the given I/O streams.
// stdin is connected to single-target executions so that interactive commands
// (e.g. "docker compose exec laravel.test php artisan tinker") work correctly.
// Pass nil to disable stdin (appropriate for concurrent multi-target runs or
// automated/test contexts where no terminal is available).
func New(stdin io.Reader, stdout, stderr io.Writer) *Runner {
	return &Runner{stdin: stdin, stdout: stdout, stderr: stderr}
}

// NewDefault creates a Runner connected to os.Stdin, os.Stdout, and os.Stderr.
func NewDefault() *Runner {
	return New(os.Stdin, os.Stdout, os.Stderr)
}

// Run executes a single target, streaming its output directly to the runner's
// stdout/stderr (no prefix). Intended for single-project invocations.
// stdin is forwarded to the subprocess so that interactive commands work
// (e.g. "docker compose exec" requires a TTY or -T when stdin is not a terminal).
func (r *Runner) Run(ctx context.Context, t Target) error {
	return r.runTarget(ctx, t, r.stdin, r.stdout, r.stderr)
}

// RunWithDeps resolves the depends_on chain for t using t.Name as the root key,
// runs each dependency in topological order (sequentially), and then runs t itself.
//
// commands is the full merged command map for t's context; it is used to
// resolve dependency definitions.  If any dependency fails, execution stops
// and the error is returned without running subsequent commands.
func (r *Runner) RunWithDeps(ctx context.Context, t Target, commands map[string]config.Command) error {
	order, err := ResolveDependencyOrder([]string{t.Name}, commands)
	if err != nil {
		return fmt.Errorf("resolve dependency order: %w", err)
	}

	// Run each step in order; the last entry is always t itself.
	for i, name := range order {
		cmd, ok := commands[name]
		if !ok {
			return fmt.Errorf("command %q not found in context", name)
		}

		step := Target{
			Name:    name,
			Dir:     t.Dir,
			Command: cmd,
			DryRun:  t.DryRun,
		}

		// Extra args only apply to the root command (last in topo order), not deps.
		if i == len(order)-1 {
			step.ExtraArgs = t.ExtraArgs
		}

		if err := r.runTarget(ctx, step, r.stdin, r.stdout, r.stderr); err != nil {
			return fmt.Errorf("dependency %q failed: %w", name, err)
		}
	}

	return nil
}

// DepTarget bundles a Target with the full command map needed to resolve its
// depends_on chain. Used by RunConcurrentWithDeps.
type DepTarget struct {
	Target   Target
	Commands map[string]config.Command
}

// RunConcurrentWithDeps executes all DepTargets concurrently. Within each
// project the depends_on chain is resolved and run sequentially before the
// main command, mirroring RunWithDeps but across multiple projects at once.
func (r *Runner) RunConcurrentWithDeps(ctx context.Context, targets []DepTarget) error {
	if len(targets) == 0 {
		return nil
	}

	if len(targets) == 1 {
		return r.RunWithDeps(ctx, targets[0].Target, targets[0].Commands)
	}

	var mu sync.Mutex

	sorted := make([]DepTarget, len(targets))
	copy(sorted, targets)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Target.Name < sorted[j].Target.Name
	})

	g, gctx := errgroup.WithContext(ctx)

	for i, dt := range sorted {
		i, dt := i, dt
		pw := newPrefixWriter(r.stdout, &mu, dt.Target.Name, colorFor(i))
		// Build a sub-runner that writes to the prefix writer so dep output
		// is also prefixed with the project name.
		sub := &Runner{stdout: pw, stderr: pw}
		g.Go(func() error {
			if err := sub.RunWithDeps(gctx, dt.Target, dt.Commands); err != nil {
				return fmt.Errorf("[%s] %w", dt.Target.Name, err)
			}
			return nil
		})
	}

	return g.Wait()
}

// RunConcurrent executes all targets concurrently, prefixing each line of output
// with the target's name. All targets are started at the same time and the function
// blocks until all have finished (or the context is cancelled).
//
// If any target fails, the other targets are allowed to finish before the first
// error is returned (errgroup does not cancel siblings).
func (r *Runner) RunConcurrent(ctx context.Context, targets []Target) error {
	if len(targets) == 0 {
		return nil
	}

	if len(targets) == 1 {
		// For a single target, skip prefixes for cleaner output.
		return r.Run(ctx, targets[0])
	}

	// Build a shared mutex to serialise writes across all prefix writers.
	var mu sync.Mutex

	// Sort targets by name for deterministic color assignment.
	sorted := make([]Target, len(targets))
	copy(sorted, targets)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	g, gctx := errgroup.WithContext(ctx)

	for i, t := range sorted {
		i, t := i, t // capture loop variables
		pw := newPrefixWriter(r.stdout, &mu, t.Name, colorFor(i))
		g.Go(func() error {
			if err := r.runTarget(gctx, t, nil, pw, pw); err != nil {
				return fmt.Errorf("[%s] %w", t.Name, err)
			}
			return nil
		})
	}

	return g.Wait()
}

// runTarget executes a single target, reading from inR and writing stdout/stderr
// to outW/errW. If inR is nil the subprocess receives no stdin (appropriate for
// concurrent multi-target runs). If t.DryRun is true the command is printed but
// not executed.
func (r *Runner) runTarget(ctx context.Context, t Target, inR io.Reader, outW, errW io.Writer) error {
	shell, flag := shellAndFlag()

	// Build the final shell command string. Extra args are appended verbatim
	// after the configured command, separated by a space.
	shellCmd := t.Command.Cmd
	if len(t.ExtraArgs) > 0 {
		shellCmd = shellCmd + " " + strings.Join(t.ExtraArgs, " ")
	}

	// Build env: process env → .env file → command-specific vars (highest priority).
	// We build this before checking preconditions so they share the same env.
	baseEnv := os.Environ()

	// Load .env from the target directory; missing file is silently skipped.
	dotEnv, err := config.LoadDotEnv(t.Dir)
	if err != nil {
		return fmt.Errorf("load .env: %w", err)
	}
	for k, v := range dotEnv {
		baseEnv = append(baseEnv, fmt.Sprintf("%s=%s", k, v))
	}

	// Command-specific env overrides .env values.
	for k, v := range t.Command.Env {
		baseEnv = append(baseEnv, fmt.Sprintf("%s=%s", k, v))
	}

	label := t.Name
	if label == "" {
		label = t.Dir
	}

	if t.DryRun {
		// Print preconditions and the command; don't execute anything.
		for _, pre := range t.Command.Preconditions {
			if _, err := fmt.Fprintf(outW, "[dry-run] %s: precondition: %s %s %q\n", label, shell, flag, pre); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(outW, "[dry-run] %s: %s %s %q\n", label, shell, flag, shellCmd); err != nil {
			return err
		}
		return nil
	}

	// Check preconditions before running the command.
	for _, pre := range t.Command.Preconditions {
		preCmd := exec.CommandContext(ctx, shell, flag, pre)
		preCmd.Dir = t.Dir
		preCmd.Env = baseEnv
		preCmd.Stdin = inR
		// Precondition output goes to stderr so it doesn't pollute stdout.
		preCmd.Stderr = errW
		if err := preCmd.Run(); err != nil {
			return fmt.Errorf("precondition %q failed for %q: %w", pre, t.Name, err)
		}
	}

	cmd := exec.CommandContext(ctx, shell, flag, shellCmd)
	cmd.Dir = t.Dir
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = errW
	cmd.Env = baseEnv

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command %q failed: %w", shellCmd, err)
	}

	return nil
}

// shellAndFlag returns the shell binary and the flag used to pass a command string.
// On Windows this is "cmd" and "/C"; on all other platforms "sh" and "-c".
func shellAndFlag() (string, string) {
	if runtime.GOOS == "windows" {
		return "cmd", "/C"
	}
	return "sh", "-c"
}
