package cli

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
)

func newListCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all resolved commands for the current context",
		Long: `List all commands resolved for the current working directory.

Commands are merged from the global config and all .projector.yaml files found
walking up from the current directory. More specific (closer) definitions win.

Example:
  projector list`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			pwd, err := d.pwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}

			merged, err := d.loader.Load(pwd)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if len(merged.Commands) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No commands defined. Use 'projector config set' to add one.")
				return nil
			}

			// Sort for deterministic output.
			names := make([]string, 0, len(merged.Commands))
			for name := range merged.Commands {
				names = append(names, name)
			}
			sort.Strings(names)

			fmt.Fprintln(cmd.OutOrStdout(), "Available commands:")
			for _, name := range names {
				c := merged.Commands[name]
				if c.Description != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  %-20s %s\n    cmd: %s\n", name, c.Description, c.Cmd)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "  %-20s %s\n", name, c.Cmd)
				}
				if len(c.Aliases) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "    aliases: %v\n", c.Aliases)
				}
				if len(c.Env) > 0 {
					envKeys := make([]string, 0, len(c.Env))
					for k := range c.Env {
						envKeys = append(envKeys, k)
					}
					sort.Strings(envKeys)
					for _, k := range envKeys {
						fmt.Fprintf(cmd.OutOrStdout(), "    env: %s=%s\n", k, c.Env[k])
					}
				}
				if len(c.Preconditions) > 0 {
					for _, pre := range c.Preconditions {
						fmt.Fprintf(cmd.OutOrStdout(), "    precondition: %s\n", pre)
					}
				}
				if len(c.DependsOn) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "    depends_on: %v\n", c.DependsOn)
				}
			}

			return nil
		},
	}
}
