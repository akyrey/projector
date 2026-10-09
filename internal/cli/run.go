package cli

import (
	"fmt"
	"path"
	"sort"

	"github.com/spf13/cobra"

	"github.com/akyrey/projector/internal/app"
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
// Without project names the command runs in the effective working directory.
func runCommand(d *deps, cmdName string, projectNames []string, extraArgs []string, dryRun bool) error {
	req := app.RunRequest{
		Command:   cmdName,
		Projects:  projectNames,
		ExtraArgs: extraArgs,
		DryRun:    dryRun,
	}
	if len(projectNames) == 0 {
		pwd, err := d.pwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		req.Dir = pwd
	}
	return d.app.Run(rootContext(), d.runner, req)
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

// ErrCrossProjectCycle is returned when cross-project depends_on entries form a cycle.
var ErrCrossProjectCycle = app.ErrCrossProjectCycle

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
