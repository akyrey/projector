package cli

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/project"
	"github.com/akyrey/projector/internal/runner"
)

func newRunCmd(d *deps) *cobra.Command {
	var filters []string

	cmd := &cobra.Command{
		Use:   "run <command> [project...] [-- extra args]",
		Short: "Run a named command in one or more projects",
		Long: `Run a named command in one or more registered projects concurrently.

If no project names are given, the command runs in the current working directory
(or the directory specified by --pwd).

When multiple project names are provided each project's directory is used to
resolve its own version of the command (from its local .projector.yaml), so
different projects can use different shell commands for the same logical action.

Any arguments after -- are appended verbatim to the shell command string.

Use --filter (-f) with a glob pattern to select projects by name instead of
listing them explicitly.

Examples:
  projector run start                        # run 'start' in cwd
  projector run start api frontend worker    # run 'start' in three projects concurrently
  projector run build -- --release           # run 'build' with extra flag --release
  projector run test api -- -v -run TestFoo  # run 'test' in api project with extra args
  projector run start -f 'api-*'             # run 'start' in all projects matching api-*
  projector run build -f '*-service' -f 'lib-*'  # multiple patterns`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeCommandNames(d),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Split args at -- boundary.
			// ArgsLenAtDash returns -1 if -- was not present.
			dashIdx := cmd.ArgsLenAtDash()
			var projectArgs, extraArgs []string
			if dashIdx == -1 {
				projectArgs = args
				extraArgs = nil
			} else {
				projectArgs = args[:dashIdx]
				extraArgs = args[dashIdx:]
			}

			var cmdName string
			var projectNames []string

			if d.flags.choose && len(projectArgs) == 0 {
				// No command name given: use the interactive picker.
				pwd, err := d.pwd()
				if err != nil {
					return fmt.Errorf("get working directory: %w", err)
				}
				merged, err := d.loader.Load(pwd)
				if err != nil {
					return fmt.Errorf("load config: %w", err)
				}
				chosen, err := pickCommand(merged.Commands, cmd.InOrStdin(), cmd.OutOrStdout())
				if err != nil {
					return fmt.Errorf("pick command: %w", err)
				}
				cmdName = chosen
			} else if len(projectArgs) == 0 {
				return fmt.Errorf("command name is required (or use --choose to pick interactively)")
			} else {
				cmdName = projectArgs[0]
				projectNames = projectArgs[1:]
			}

			// Expand --filter patterns into additional project names.
			if len(filters) > 0 {
				matched, err := matchProjects(d, filters)
				if err != nil {
					return err
				}
				// Merge: explicit names + matched names (deduplicated).
				seen := make(map[string]struct{}, len(projectNames))
				for _, n := range projectNames {
					seen[n] = struct{}{}
				}
				for _, n := range matched {
					if _, ok := seen[n]; !ok {
						projectNames = append(projectNames, n)
						seen[n] = struct{}{}
					}
				}
			}

			return runCommand(d, cmdName, projectNames, extraArgs, d.flags.dryRun)
		},
	}

	cmd.Flags().StringArrayVarP(&filters, "filter", "f", nil, "glob pattern to select projects by name (repeatable)")

	return cmd
}

// runCommand is the shared logic between `projector run` and the shorthand dispatch.
// extraArgs are appended verbatim to the resolved shell command string.
// dryRun prints the resolved commands without executing them.
func runCommand(d *deps, cmdName string, projectNames []string, extraArgs []string, dryRun bool) error {
	// Single-project (or cwd) path.
	if len(projectNames) == 0 {
		return runInCwd(d, cmdName, extraArgs, dryRun)
	}

	return runInProjects(d, cmdName, projectNames, extraArgs, dryRun)
}

// runInCwd resolves and runs cmdName in the effective working directory.
// If the command has depends_on entries they are executed first in topological order.
// Cross-project deps (^proj:cmd) are resolved first, then local deps, then the command.
// extraArgs are appended verbatim to the resolved shell command string.
// dryRun prints the resolved commands without executing them.
func runInCwd(d *deps, cmdName string, extraArgs []string, dryRun bool) error {
	pwd, err := d.pwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	merged, err := d.loader.Load(pwd)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	cmd, ok := merged.Commands[cmdName]
	if !ok {
		return fmt.Errorf("command %q not found in current context", cmdName)
	}

	// Separate cross-project deps (^proj:cmd) from local deps.
	crossDeps, localDeps, err := partitionDeps(cmd.DependsOn)
	if err != nil {
		return err
	}

	// Run cross-project deps first (sequentially per unique project).
	if err := runCrossProjectDeps(d, crossDeps, dryRun, nil); err != nil {
		return err
	}

	// Strip cross-project entries from the command before passing to the runner.
	localCmd := cmd
	localCmd.DependsOn = localDeps

	t := runner.Target{
		Name:      cmdName,
		Dir:       pwd,
		Command:   localCmd,
		ExtraArgs: extraArgs,
		DryRun:    dryRun,
	}

	if len(localDeps) > 0 {
		// Rebuild commands map with the patched version so RunWithDeps sees it.
		localCommands := patchCommandMap(merged.Commands, cmdName, localCmd)
		return d.runner.RunWithDeps(rootContext(), t, localCommands)
	}

	return d.runner.Run(rootContext(), t)
}

// runInProjects resolves and runs cmdName in each named project concurrently.
// Dependencies (depends_on) for each project are resolved and executed sequentially
// within that project before the main command runs.
// Cross-project deps (^proj:cmd) are resolved first before any project runs.
// extraArgs are appended verbatim to the resolved shell command string for each project.
// dryRun prints the resolved commands without executing them.
func runInProjects(d *deps, cmdName string, projectNames []string, extraArgs []string, dryRun bool) error {
	type projectTarget struct {
		target   runner.Target
		commands map[string]config.Command
	}

	pts := make([]projectTarget, 0, len(projectNames))

	// Collect all cross-project deps from all projects (deduplicated).
	allCrossDeps := make(map[string]crossDep) // key: "proj:cmd"

	for _, name := range projectNames {
		proj, err := d.registry.Get(name)
		if err != nil {
			if errors.Is(err, project.ErrNotFound) {
				return fmt.Errorf("project %q is not registered; use 'projector project add' to register it", name)
			}
			return fmt.Errorf("get project %q: %w", name, err)
		}

		// Each project resolves its own command from its own directory hierarchy.
		merged, err := d.loader.LoadForProject(proj.Path)
		if err != nil {
			return fmt.Errorf("load config for project %q: %w", name, err)
		}

		cmd, ok := merged.Commands[cmdName]
		if !ok {
			return fmt.Errorf("command %q not found for project %q", cmdName, name)
		}

		// Partition deps for this project.
		crossDeps, localDeps, err := partitionDeps(cmd.DependsOn)
		if err != nil {
			return fmt.Errorf("project %q: %w", name, err)
		}
		for _, cd := range crossDeps {
			allCrossDeps[cd.projName+":"+cd.cmdName] = cd
		}

		// Use a patched command with only local deps for the runner.
		localCmd := cmd
		localCmd.DependsOn = localDeps

		pts = append(pts, projectTarget{
			target: runner.Target{
				Name:      name,
				Dir:       proj.Path,
				Command:   localCmd,
				ExtraArgs: extraArgs,
				DryRun:    dryRun,
			},
			commands: patchCommandMap(merged.Commands, cmdName, localCmd),
		})
	}

	// Run all cross-project deps first (sequentially per unique dep).
	if len(allCrossDeps) > 0 {
		ordered := make([]crossDep, 0, len(allCrossDeps))
		for _, cd := range allCrossDeps {
			ordered = append(ordered, cd)
		}
		sort.Slice(ordered, func(i, j int) bool {
			ki := ordered[i].projName + ":" + ordered[i].cmdName
			kj := ordered[j].projName + ":" + ordered[j].cmdName
			return ki < kj
		})
		if err := runCrossProjectDeps(d, ordered, dryRun, nil); err != nil {
			return err
		}
	}

	// If any project has local depends_on, we need per-project dep resolution.
	hasDeps := false
	for _, pt := range pts {
		if len(pt.target.Command.DependsOn) > 0 {
			hasDeps = true
			break
		}
	}

	if !hasDeps {
		targets := make([]runner.Target, len(pts))
		for i, pt := range pts {
			targets[i] = pt.target
		}
		return d.runner.RunConcurrent(rootContext(), targets)
	}

	// With deps: wrap each project's execution as a target that includes its
	// full dep chain, then run all projects concurrently.
	depsTargets := make([]runner.DepTarget, len(pts))
	for i, pt := range pts {
		depsTargets[i] = runner.DepTarget{
			Target:   pt.target,
			Commands: pt.commands,
		}
	}

	return d.runner.RunConcurrentWithDeps(rootContext(), depsTargets)
}

// matchProjects returns the names of all registered projects that match at
// least one of the given glob patterns. Uses path.Match semantics (same as
// filepath.Match but OS-independent). Returns an error if a pattern is malformed
// or no projects match any pattern.
func matchProjects(d *deps, patterns []string) ([]string, error) {
	all, err := d.registry.List()
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}

	// Validate all patterns up-front.
	for _, pat := range patterns {
		if _, err := path.Match(pat, ""); err != nil {
			return nil, fmt.Errorf("invalid filter pattern %q: %w", pat, err)
		}
	}

	var matched []string
	for name := range all {
		for _, pat := range patterns {
			ok, _ := path.Match(pat, name) // error already checked above
			if ok {
				matched = append(matched, name)
				break
			}
		}
	}

	sort.Strings(matched)
	return matched, nil
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
		if strings.HasPrefix(dep, "^") {
			rest := dep[1:] // strip ^
			idx := strings.IndexByte(rest, ':')
			if idx < 1 || idx == len(rest)-1 {
				return nil, nil, fmt.Errorf(
					"cross-project dep %q must be in the form ^<project>:<command>", dep)
			}
			cross = append(cross, crossDep{
				projName: rest[:idx],
				cmdName:  rest[idx+1:],
			})
		} else {
			local = append(local, dep)
		}
	}
	return cross, local, nil
}

// ErrCrossProjectCycle is returned when cross-project depends_on entries form a cycle.
var ErrCrossProjectCycle = errors.New("cross-project cyclic dependency detected")

// runCrossProjectDeps resolves and runs each cross-project dep sequentially.
// Each dep is run in its registered project's directory.
// visited tracks in-progress nodes (keyed by "proj\x00cmd") to detect cycles.
// Pass nil on the first call; the map is allocated lazily.
func runCrossProjectDeps(d *deps, deps []crossDep, dryRun bool, visited map[string]struct{}) error {
	if visited == nil {
		visited = make(map[string]struct{})
	}

	for _, cd := range deps {
		key := cd.projName + "\x00" + cd.cmdName
		if _, seen := visited[key]; seen {
			return fmt.Errorf("%w: %s:%s", ErrCrossProjectCycle, cd.projName, cd.cmdName)
		}
		visited[key] = struct{}{}

		proj, err := d.registry.Get(cd.projName)
		if err != nil {
			if errors.Is(err, project.ErrNotFound) {
				return fmt.Errorf("cross-project dep: project %q is not registered", cd.projName)
			}
			return fmt.Errorf("cross-project dep: get project %q: %w", cd.projName, err)
		}

		merged, err := d.loader.LoadForProject(proj.Path)
		if err != nil {
			return fmt.Errorf("cross-project dep: load config for project %q: %w", cd.projName, err)
		}

		cmd, ok := merged.Commands[cd.cmdName]
		if !ok {
			return fmt.Errorf("cross-project dep: command %q not found in project %q", cd.cmdName, cd.projName)
		}

		// Cross-project deps themselves may have cross-project deps — recurse.
		subCross, localDeps, err := partitionDeps(cmd.DependsOn)
		if err != nil {
			return fmt.Errorf("cross-project dep %q/%q: %w", cd.projName, cd.cmdName, err)
		}
		if err := runCrossProjectDeps(d, subCross, dryRun, visited); err != nil {
			return err
		}

		delete(visited, key)

		localCmd := cmd
		localCmd.DependsOn = localDeps

		t := runner.Target{
			Name:    fmt.Sprintf("%s/%s", cd.projName, cd.cmdName),
			Dir:     proj.Path,
			Command: localCmd,
			DryRun:  dryRun,
		}

		if len(localDeps) > 0 {
			localCommands := patchCommandMap(merged.Commands, cd.cmdName, localCmd)
			if err := d.runner.RunWithDeps(rootContext(), t, localCommands); err != nil {
				return fmt.Errorf("cross-project dep %q/%q: %w", cd.projName, cd.cmdName, err)
			}
		} else {
			if err := d.runner.Run(rootContext(), t); err != nil {
				return fmt.Errorf("cross-project dep %q/%q: %w", cd.projName, cd.cmdName, err)
			}
		}
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

// completeCommandNames provides shell completion for command names.
func completeCommandNames(d *deps) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			// After the command name, complete project names.
			return completeProjectNamesSlice(d), cobra.ShellCompDirectiveNoFileComp
		}
		pwd, err := d.pwd()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		merged, err := d.loader.Load(pwd)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		names := make([]string, 0, len(merged.Commands))
		for name := range merged.Commands {
			names = append(names, name)
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeProjectNamesSlice returns a slice of all registered project names.
func completeProjectNamesSlice(d *deps) []string {
	projects, err := d.registry.List()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	return names
}
