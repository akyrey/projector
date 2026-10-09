package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/project"
	"github.com/akyrey/projector/internal/runner"
)

// ErrCrossProjectCycle is returned when cross-project depends_on entries form a cycle.
var ErrCrossProjectCycle = errors.New("cross-project cyclic dependency detected")

// RunRequest describes one invocation of a named command.
type RunRequest struct {
	// Command is the command name (or alias) to run.
	Command string

	// Projects are registered project names to run in concurrently. When
	// empty the command runs in Dir.
	Projects []string

	// Dir is the directory used when Projects is empty.
	Dir string

	// ExtraArgs are appended verbatim to the resolved shell command string.
	ExtraArgs []string

	// DryRun prints the resolved commands without executing them.
	DryRun bool
}

// Run resolves req and executes it with r. Cross-project deps (^proj:cmd) run
// first, then local deps, then the command and its pending once steps.
func (a *App) Run(ctx context.Context, r *runner.Runner, req RunRequest) error {
	if len(req.Projects) == 0 {
		return a.runInDir(ctx, r, req)
	}
	return a.runInProjects(ctx, r, req)
}

// runInDir resolves and runs req.Command in req.Dir.
func (a *App) runInDir(ctx context.Context, r *runner.Runner, req RunRequest) error {
	merged, err := a.loader.Load(req.Dir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	cmd, ok := merged.Commands[req.Command]
	if !ok {
		return fmt.Errorf("command %q not found in current context", req.Command)
	}

	crossDeps, localDeps, err := partitionDeps(cmd.DependsOn)
	if err != nil {
		return err
	}
	if err := a.runCrossProjectDeps(ctx, r, crossDeps, req.DryRun, newCrossDepWalk()); err != nil {
		return err
	}

	// Strip cross-project entries from the command before passing to the runner.
	localCmd := cmd
	localCmd.DependsOn = localDeps

	followUps, err := a.onceFollowUps(req.Dir, req.Command, cmd)
	if err != nil {
		return err
	}

	t := runner.Target{
		Name:      req.Command,
		Dir:       req.Dir,
		Command:   localCmd,
		ExtraArgs: req.ExtraArgs,
		DryRun:    req.DryRun,
		FollowUps: followUps,
	}
	if len(localDeps) > 0 {
		return r.RunWithDeps(ctx, t, patchCommandMap(merged.Commands, req.Command, localCmd))
	}
	return r.Run(ctx, t)
}

// runInProjects resolves req.Command in each project and runs them
// concurrently, after all of their cross-project deps (deduplicated).
func (a *App) runInProjects(ctx context.Context, r *runner.Runner, req RunRequest) error {
	depTargets := make([]runner.DepTarget, 0, len(req.Projects))
	allCrossDeps := make(map[string]crossDep) // key: "proj:cmd"
	hasLocalDeps := false

	for _, name := range req.Projects {
		proj, err := a.registry.Get(name)
		if err != nil {
			if errors.Is(err, project.ErrNotFound) {
				return fmt.Errorf("project %q is not registered; use 'projector project add' to register it", name)
			}
			return fmt.Errorf("get project %q: %w", name, err)
		}

		// Each project resolves its own command from its own directory hierarchy.
		merged, err := a.loader.LoadForProject(proj.Path)
		if err != nil {
			return fmt.Errorf("load config for project %q: %w", name, err)
		}
		cmd, ok := merged.Commands[req.Command]
		if !ok {
			return fmt.Errorf("command %q not found for project %q", req.Command, name)
		}

		crossDeps, localDeps, err := partitionDeps(cmd.DependsOn)
		if err != nil {
			return fmt.Errorf("project %q: %w", name, err)
		}
		for _, cd := range crossDeps {
			allCrossDeps[cd.projName+":"+cd.cmdName] = cd
		}
		hasLocalDeps = hasLocalDeps || len(localDeps) > 0

		localCmd := cmd
		localCmd.DependsOn = localDeps

		followUps, err := a.onceFollowUps(proj.Path, req.Command, cmd)
		if err != nil {
			return fmt.Errorf("project %q: %w", name, err)
		}

		depTargets = append(depTargets, runner.DepTarget{
			Target: runner.Target{
				Name:      name,
				Dir:       proj.Path,
				Command:   localCmd,
				ExtraArgs: req.ExtraArgs,
				DryRun:    req.DryRun,
				FollowUps: followUps,
			},
			Commands: patchCommandMap(merged.Commands, req.Command, localCmd),
		})
	}

	if err := a.runCrossProjectDeps(ctx, r, sortedCrossDeps(allCrossDeps), req.DryRun, newCrossDepWalk()); err != nil {
		return err
	}

	if hasLocalDeps {
		return r.RunConcurrentWithDeps(ctx, depTargets)
	}
	targets := make([]runner.Target, len(depTargets))
	for i, dt := range depTargets {
		targets[i] = dt.Target
	}
	return r.RunConcurrent(ctx, targets)
}

// sortedCrossDeps returns the deps ordered by "proj:cmd" for deterministic runs.
func sortedCrossDeps(byKey map[string]crossDep) []crossDep {
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make([]crossDep, len(keys))
	for i, k := range keys {
		ordered[i] = byKey[k]
	}
	return ordered
}

// crossDep represents a cross-project dependency parsed from a ^proj:cmd entry.
type crossDep struct {
	projName string
	cmdName  string
}

// partitionDeps separates a DependsOn list into cross-project deps (^proj:cmd)
// and local deps (all others). Returns an error for malformed ^-entries.
func partitionDeps(dependsOn []string) (cross []crossDep, local []string, err error) {
	for _, dep := range dependsOn {
		rest, isCross := strings.CutPrefix(dep, "^")
		if !isCross {
			local = append(local, dep)
			continue
		}
		idx := strings.IndexByte(rest, ':')
		if idx < 1 || idx == len(rest)-1 {
			return nil, nil, fmt.Errorf(
				"cross-project dep %q must be in the form ^<project>:<command>", dep)
		}
		cross = append(cross, crossDep{projName: rest[:idx], cmdName: rest[idx+1:]})
	}
	return cross, local, nil
}

// crossDepWalk tracks cross-project deps during one invocation, keyed by
// "proj\x00cmd". inProgress holds the current recursion path (for cycle
// detection); done holds every dep that already ran, so a dep reachable through
// several paths (a diamond) runs only once.
type crossDepWalk struct {
	inProgress map[string]struct{}
	done       map[string]struct{}
}

func newCrossDepWalk() *crossDepWalk {
	return &crossDepWalk{
		inProgress: make(map[string]struct{}),
		done:       make(map[string]struct{}),
	}
}

// runCrossProjectDeps resolves and runs each cross-project dep sequentially.
// Each dep is run in its registered project's directory, after its own
// cross-project deps.
func (a *App) runCrossProjectDeps(ctx context.Context, r *runner.Runner, deps []crossDep, dryRun bool, walk *crossDepWalk) error {
	for _, cd := range deps {
		key := cd.projName + "\x00" + cd.cmdName
		if _, ran := walk.done[key]; ran {
			continue
		}
		if _, active := walk.inProgress[key]; active {
			return fmt.Errorf("%w: %s:%s", ErrCrossProjectCycle, cd.projName, cd.cmdName)
		}
		walk.inProgress[key] = struct{}{}

		if err := a.runCrossProjectDep(ctx, r, cd, dryRun, walk); err != nil {
			return err
		}

		delete(walk.inProgress, key)
		walk.done[key] = struct{}{}
	}
	return nil
}

// runCrossProjectDep runs one cross-project dep after its own cross-project deps.
func (a *App) runCrossProjectDep(ctx context.Context, r *runner.Runner, cd crossDep, dryRun bool, walk *crossDepWalk) error {
	proj, err := a.registry.Get(cd.projName)
	if err != nil {
		if errors.Is(err, project.ErrNotFound) {
			return fmt.Errorf("cross-project dep: project %q is not registered", cd.projName)
		}
		return fmt.Errorf("cross-project dep: get project %q: %w", cd.projName, err)
	}

	merged, err := a.loader.LoadForProject(proj.Path)
	if err != nil {
		return fmt.Errorf("cross-project dep: load config for project %q: %w", cd.projName, err)
	}
	cmd, ok := merged.Commands[cd.cmdName]
	if !ok {
		return fmt.Errorf("cross-project dep: command %q not found in project %q", cd.cmdName, cd.projName)
	}

	subCross, localDeps, err := partitionDeps(cmd.DependsOn)
	if err != nil {
		return fmt.Errorf("cross-project dep %q/%q: %w", cd.projName, cd.cmdName, err)
	}
	if err := a.runCrossProjectDeps(ctx, r, subCross, dryRun, walk); err != nil {
		return err
	}

	localCmd := cmd
	localCmd.DependsOn = localDeps

	followUps, err := a.onceFollowUps(proj.Path, cd.cmdName, cmd)
	if err != nil {
		return fmt.Errorf("cross-project dep %q/%q: %w", cd.projName, cd.cmdName, err)
	}

	t := runner.Target{
		Name:      fmt.Sprintf("%s/%s", cd.projName, cd.cmdName),
		Dir:       proj.Path,
		Command:   localCmd,
		DryRun:    dryRun,
		FollowUps: followUps,
	}
	if len(localDeps) > 0 {
		err = r.RunWithDeps(ctx, t, patchCommandMap(merged.Commands, cd.cmdName, localCmd))
	} else {
		err = r.Run(ctx, t)
	}
	if err != nil {
		return fmt.Errorf("cross-project dep %q/%q: %w", cd.projName, cd.cmdName, err)
	}
	return nil
}

// patchCommandMap returns a copy of commands with the entry for key replaced by patched.
func patchCommandMap(commands map[string]config.Command, key string, patched config.Command) map[string]config.Command {
	out := make(map[string]config.Command, len(commands))
	for k, v := range commands {
		out[k] = v
	}
	out[key] = patched
	return out
}
