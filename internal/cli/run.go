package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/project"
	"github.com/akyrey/projector/internal/runner"
)

func newRunCmd(d *deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <command> [project...]",
		Short: "Run a named command in one or more projects",
		Long: `Run a named command in one or more registered projects concurrently.

If no project names are given, the command runs in the current working directory
(or the directory specified by --pwd).

When multiple project names are provided each project's directory is used to
resolve its own version of the command (from its local .projector.yaml), so
different projects can use different shell commands for the same logical action.

Examples:
  projector run start                      # run 'start' in cwd
  projector run start api frontend worker  # run 'start' in three projects concurrently`,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeCommandNames(d),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmdName := args[0]
			projectNames := args[1:]
			return runCommand(d, cmdName, projectNames)
		},
	}

	return cmd
}

// runCommand is the shared logic between `projector run` and the shorthand dispatch.
func runCommand(d *deps, cmdName string, projectNames []string) error {
	// Single-project (or cwd) path.
	if len(projectNames) == 0 {
		return runInCwd(d, cmdName)
	}

	return runInProjects(d, cmdName, projectNames)
}

// runInCwd resolves and runs cmdName in the effective working directory.
// If the command has depends_on entries they are executed first in topological order.
func runInCwd(d *deps, cmdName string) error {
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

	t := runner.Target{
		Name:    cmdName,
		Dir:     pwd,
		Command: cmd,
	}

	if len(cmd.DependsOn) > 0 {
		return d.runner.RunWithDeps(rootContext(), t, merged.Commands)
	}

	return d.runner.Run(rootContext(), t)
}

// runInProjects resolves and runs cmdName in each named project concurrently.
// Dependencies (depends_on) for each project are resolved and executed sequentially
// within that project before the main command runs.
func runInProjects(d *deps, cmdName string, projectNames []string) error {
	type projectTarget struct {
		target   runner.Target
		commands map[string]config.Command
	}

	pts := make([]projectTarget, 0, len(projectNames))

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

		pts = append(pts, projectTarget{
			target: runner.Target{
				Name:    name,
				Dir:     proj.Path,
				Command: cmd,
			},
			commands: merged.Commands,
		})
	}

	// If any project has depends_on, we need per-project dep resolution.
	// We still run across projects concurrently, but each project's dep chain
	// is handled internally by RunWithDeps (sequential within one project).
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
