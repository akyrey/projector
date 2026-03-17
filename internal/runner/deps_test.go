package runner_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/runner"
)

// commands is a helper to build a map of command definitions from pairs.
func commands(pairs ...string) map[string]config.Command {
	if len(pairs)%2 != 0 {
		panic("commands: pairs must be even (name, cmd, ...)")
	}
	m := make(map[string]config.Command, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		m[pairs[i]] = config.Command{Cmd: pairs[i+1]}
	}
	return m
}

// commandsWithDeps builds a map where each name gets both a cmd string and deps.
func commandsWithDeps(defs []struct {
	name string
	cmd  string
	deps []string
}) map[string]config.Command {
	m := make(map[string]config.Command, len(defs))
	for _, d := range defs {
		m[d.name] = config.Command{Cmd: d.cmd, DependsOn: d.deps}
	}
	return m
}

// --- ResolveDependencyOrder tests -------------------------------------------

// TestResolveDependencyOrder_NoDeps: a command with no dependencies returns just itself.
func TestResolveDependencyOrder_NoDeps(t *testing.T) {
	cmds := commands("start", "echo start")

	order, err := runner.ResolveDependencyOrder([]string{"start"}, cmds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(order) != 1 || order[0] != "start" {
		t.Errorf("got %v, want [start]", order)
	}
}

// TestResolveDependencyOrder_LinearChain: a -> b -> c resolved in correct order.
func TestResolveDependencyOrder_LinearChain(t *testing.T) {
	cmds := commandsWithDeps([]struct {
		name string
		cmd  string
		deps []string
	}{
		{"a", "echo a", []string{"b"}},
		{"b", "echo b", []string{"c"}},
		{"c", "echo c", nil},
	})

	order, err := runner.ResolveDependencyOrder([]string{"a"}, cmds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// c must come before b, and b before a.
	pos := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}

	if pos("c") >= pos("b") {
		t.Errorf("c should come before b; got order %v", order)
	}
	if pos("b") >= pos("a") {
		t.Errorf("b should come before a; got order %v", order)
	}
}

// TestResolveDependencyOrder_Diamond: a -> b,c; b -> d; c -> d.
func TestResolveDependencyOrder_Diamond(t *testing.T) {
	cmds := commandsWithDeps([]struct {
		name string
		cmd  string
		deps []string
	}{
		{"a", "echo a", []string{"b", "c"}},
		{"b", "echo b", []string{"d"}},
		{"c", "echo c", []string{"d"}},
		{"d", "echo d", nil},
	})

	order, err := runner.ResolveDependencyOrder([]string{"a"}, cmds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pos := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}

	// d must come before b and c; b and c must come before a.
	if pos("d") >= pos("b") {
		t.Errorf("d should come before b; order: %v", order)
	}
	if pos("d") >= pos("c") {
		t.Errorf("d should come before c; order: %v", order)
	}
	if pos("b") >= pos("a") {
		t.Errorf("b should come before a; order: %v", order)
	}
	if pos("c") >= pos("a") {
		t.Errorf("c should come before a; order: %v", order)
	}
}

// TestResolveDependencyOrder_CycleDirectSelf: a -> a produces ErrCyclicDependency.
func TestResolveDependencyOrder_CycleDirectSelf(t *testing.T) {
	cmds := commandsWithDeps([]struct {
		name string
		cmd  string
		deps []string
	}{
		{"a", "echo a", []string{"a"}},
	})

	_, err := runner.ResolveDependencyOrder([]string{"a"}, cmds)
	if !errors.Is(err, runner.ErrCyclicDependency) {
		t.Errorf("expected ErrCyclicDependency, got %v", err)
	}
}

// TestResolveDependencyOrder_CycleTwoNodes: a -> b -> a.
func TestResolveDependencyOrder_CycleTwoNodes(t *testing.T) {
	cmds := commandsWithDeps([]struct {
		name string
		cmd  string
		deps []string
	}{
		{"a", "echo a", []string{"b"}},
		{"b", "echo b", []string{"a"}},
	})

	_, err := runner.ResolveDependencyOrder([]string{"a"}, cmds)
	if !errors.Is(err, runner.ErrCyclicDependency) {
		t.Errorf("expected ErrCyclicDependency, got %v", err)
	}
}

// TestResolveDependencyOrder_UnknownDep: referencing a non-existent command returns ErrUnknownDependency.
func TestResolveDependencyOrder_UnknownDep(t *testing.T) {
	cmds := commandsWithDeps([]struct {
		name string
		cmd  string
		deps []string
	}{
		{"a", "echo a", []string{"ghost"}},
	})

	_, err := runner.ResolveDependencyOrder([]string{"a"}, cmds)
	if !errors.Is(err, runner.ErrUnknownDependency) {
		t.Errorf("expected ErrUnknownDependency, got %v", err)
	}
}

// TestResolveDependencyOrder_MultipleRoots: two independent roots both resolved.
func TestResolveDependencyOrder_MultipleRoots(t *testing.T) {
	cmds := commandsWithDeps([]struct {
		name string
		cmd  string
		deps []string
	}{
		{"x", "echo x", nil},
		{"y", "echo y", nil},
	})

	order, err := runner.ResolveDependencyOrder([]string{"x", "y"}, cmds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(order) != 2 {
		t.Errorf("expected 2 items, got %d: %v", len(order), order)
	}
}

// TestResolveDependencyOrder_SharedDep: two roots share a dependency; dep appears once.
func TestResolveDependencyOrder_SharedDep(t *testing.T) {
	cmds := commandsWithDeps([]struct {
		name string
		cmd  string
		deps []string
	}{
		{"a", "echo a", []string{"shared"}},
		{"b", "echo b", []string{"shared"}},
		{"shared", "echo shared", nil},
	})

	order, err := runner.ResolveDependencyOrder([]string{"a", "b"}, cmds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// shared should appear exactly once.
	count := 0
	for _, n := range order {
		if n == "shared" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 'shared' to appear exactly once; got order %v", order)
	}
}

// --- RunWithDeps tests -------------------------------------------------------

// TestRunWithDeps_NoDeps: a command without deps runs normally.
func TestRunWithDeps_NoDeps(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(&out, &out)

	cmds := commands("start", "echo started")

	tgt := runner.Target{
		Name:    "start",
		Dir:     t.TempDir(),
		Command: cmds["start"],
	}

	if err := r.RunWithDeps(context.Background(), tgt, cmds); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}

	if !strings.Contains(out.String(), "started") {
		t.Errorf("expected 'started' in output, got: %q", out.String())
	}
}

// TestRunWithDeps_LinearChain: verify deps execute before the main command.
func TestRunWithDeps_LinearChain(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(&out, &out)

	cmds := commandsWithDeps([]struct {
		name string
		cmd  string
		deps []string
	}{
		{"build", "echo building", []string{"install"}},
		{"install", "echo installing", nil},
	})

	tgt := runner.Target{
		Name:    "build",
		Dir:     t.TempDir(),
		Command: cmds["build"],
	}

	if err := r.RunWithDeps(context.Background(), tgt, cmds); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}

	output := out.String()
	installPos := strings.Index(output, "installing")
	buildPos := strings.Index(output, "building")

	if installPos == -1 || buildPos == -1 {
		t.Fatalf("expected both 'installing' and 'building' in output; got: %q", output)
	}

	if installPos > buildPos {
		t.Errorf("'installing' should appear before 'building'; output:\n%s", output)
	}
}

// TestRunWithDeps_FailingDep: a failing dependency stops execution.
func TestRunWithDeps_FailingDep(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(&out, &out)

	cmds := commandsWithDeps([]struct {
		name string
		cmd  string
		deps []string
	}{
		{"build", "echo should not run", []string{"setup"}},
		{"setup", "exit 1", nil},
	})

	tgt := runner.Target{
		Name:    "build",
		Dir:     t.TempDir(),
		Command: cmds["build"],
	}

	err := r.RunWithDeps(context.Background(), tgt, cmds)
	if err == nil {
		t.Fatal("expected error from failing dependency, got nil")
	}

	// The main command should NOT have run.
	if strings.Contains(out.String(), "should not run") {
		t.Errorf("main command ran despite failing dependency; output: %q", out.String())
	}
}

// TestRunWithDeps_CycleReturnsError: a cycle in depends_on is detected at resolve time.
func TestRunWithDeps_CycleReturnsError(t *testing.T) {
	var out bytes.Buffer
	r := runner.New(&out, &out)

	cmds := commandsWithDeps([]struct {
		name string
		cmd  string
		deps []string
	}{
		{"a", "echo a", []string{"b"}},
		{"b", "echo b", []string{"a"}},
	})

	tgt := runner.Target{
		Name:    "a",
		Dir:     t.TempDir(),
		Command: cmds["a"],
	}

	err := r.RunWithDeps(context.Background(), tgt, cmds)
	if !errors.Is(err, runner.ErrCyclicDependency) {
		t.Errorf("expected ErrCyclicDependency, got %v", err)
	}
}
