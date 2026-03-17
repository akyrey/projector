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
}

// Runner executes commands, handling both single and multi-target scenarios.
type Runner struct {
	stdout io.Writer
	stderr io.Writer
}

// New creates a Runner writing to the given output streams.
func New(stdout, stderr io.Writer) *Runner {
	return &Runner{stdout: stdout, stderr: stderr}
}

// NewDefault creates a Runner writing to os.Stdout and os.Stderr.
func NewDefault() *Runner {
	return New(os.Stdout, os.Stderr)
}

// Run executes a single target, streaming its output directly to the runner's
// stdout/stderr (no prefix). Intended for single-project invocations.
func (r *Runner) Run(ctx context.Context, t Target) error {
	return r.runTarget(ctx, t, r.stdout, r.stderr)
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
	for _, name := range order {
		cmd, ok := commands[name]
		if !ok {
			return fmt.Errorf("command %q not found in context", name)
		}

		step := Target{
			Name:    name,
			Dir:     t.Dir,
			Command: cmd,
		}

		if err := r.runTarget(ctx, step, r.stdout, r.stderr); err != nil {
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
			if err := r.runTarget(gctx, t, pw, pw); err != nil {
				return fmt.Errorf("[%s] %w", t.Name, err)
			}
			return nil
		})
	}

	return g.Wait()
}

// runTarget executes a single target, writing stdout to outW and stderr to errW.
func (r *Runner) runTarget(ctx context.Context, t Target, outW, errW io.Writer) error {
	shell, flag := shellAndFlag()
	cmd := exec.CommandContext(ctx, shell, flag, t.Command.Cmd)
	cmd.Dir = t.Dir
	cmd.Stdout = outW
	cmd.Stderr = errW

	// Apply base environment then overlay command-specific vars.
	cmd.Env = os.Environ()
	for k, v := range t.Command.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command %q failed: %w", t.Command.Cmd, err)
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
