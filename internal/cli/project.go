package cli

import (
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/akyrey/projector/internal/project"
)

func newProjectCmd(d *deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage registered projects",
		Long: `Add, remove, and list named projects stored in the global config.

Registered projects can be referenced by name in 'projector run' to execute
commands across multiple directories concurrently.`,
	}

	cmd.AddCommand(newProjectAddCmd(d))
	cmd.AddCommand(newProjectRemoveCmd(d))
	cmd.AddCommand(newProjectListCmd(d))

	return cmd
}

func newProjectAddCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "add <name> <path>",
		Short: "Register a new project",
		Long: `Register a named project pointing to the given directory path.

The path is resolved to an absolute path and stored in the global config.

Example:
  projector project add my-api ~/work/my-api
  projector project add frontend /home/user/projects/frontend`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, path := args[0], args[1]
			if err := d.registry.Add(name, path); err != nil {
				if errors.Is(err, project.ErrAlreadyExists) {
					return fmt.Errorf("project %q already exists; use 'projector project remove %s' first", name, name)
				}
				return fmt.Errorf("add project: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Project %q registered.\n", name)
			return nil
		},
	}
}

func newProjectRemoveCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:               "remove <name>",
		Short:             "Remove a registered project",
		Aliases:           []string{"rm"},
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: projectNamesCompletion(d),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := d.registry.Remove(name); err != nil {
				if errors.Is(err, project.ErrNotFound) {
					return fmt.Errorf("project %q is not registered", name)
				}
				return fmt.Errorf("remove project: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Project %q removed.\n", name)
			return nil
		},
	}
}

func newProjectListCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all registered projects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			projects, err := d.registry.List()
			if err != nil {
				return fmt.Errorf("list projects: %w", err)
			}

			if len(projects) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No projects registered. Use 'projector project add <name> <path>' to register one.")
				return nil
			}

			// Sort names for deterministic output.
			names := make([]string, 0, len(projects))
			for name := range projects {
				names = append(names, name)
			}
			sort.Strings(names)

			fmt.Fprintln(cmd.OutOrStdout(), "Registered projects:")
			for _, name := range names {
				fmt.Fprintf(cmd.OutOrStdout(), "  %-20s %s\n", name, projects[name].Path)
			}

			return nil
		},
	}
}

// projectNamesCompletion returns a completion function for registered project names.
func projectNamesCompletion(d *deps) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completeProjectNamesSlice(d), cobra.ShellCompDirectiveNoFileComp
	}
}
