package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/editor"
)

func newConfigCmd(d *deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage projector configuration",
		Long: `View and edit projector configuration files.

Config files are YAML files that define commands and (globally) projects.
There is a hierarchy from least to most specific:
  ~/.config/projector/config.yaml   (global, use --global flag)
  .projector.yaml                   (local, in the current directory)`,
	}

	cmd.AddCommand(newConfigEditCmd(d))
	cmd.AddCommand(newConfigSetCmd(d))
	cmd.AddCommand(newConfigRemoveCmd(d))
	cmd.AddCommand(newConfigShowCmd(d))

	return cmd
}

// newConfigEditCmd opens the config file in $EDITOR.
func newConfigEditCmd(d *deps) *cobra.Command {
	var global bool

	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Open the config file in $EDITOR",
		Long: `Open the local .projector.yaml (or global config with --global) in $EDITOR.

If the file does not yet exist it is created with an empty skeleton.

Examples:
  projector config edit           # edit .projector.yaml in current directory
  projector config edit --global  # edit ~/.config/projector/config.yaml`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath(d, global)
			if err != nil {
				return err
			}

			// Ensure the file exists with a useful skeleton before opening.
			if err := ensureConfigExists(path, global); err != nil {
				return fmt.Errorf("prepare config file: %w", err)
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Opening %s\n", path); err != nil {
				return err
			}
			return editor.Open(path)
		},
	}

	cmd.Flags().BoolVarP(&global, "global", "g", false, "edit the global config instead of the local one")
	return cmd
}

// newConfigSetCmd sets a command in the config.
func newConfigSetCmd(d *deps) *cobra.Command {
	var (
		global        bool
		description   string
		envPairs      []string
		dependsOn     []string
		aliases       []string
		preconditions []string
	)

	cmd := &cobra.Command{
		Use:   "set <name> <shell-command>",
		Short: "Add or update a command in the config",
		Long: `Add or update a command definition in the local (or global) config.

Examples:
  projector config set start "docker compose up -d"
  projector config set start "sail up -d" --description "Start Laravel project"
  projector config set start "sail up -d" --env SAIL_XDEBUG=1 --env APP_ENV=local
  projector config set build "go build ./..." --alias b
  projector config set start "sail up -d" --global`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, shellCmd := args[0], args[1]

			// Parse --env KEY=VALUE pairs.
			env, err := parseEnvPairs(envPairs)
			if err != nil {
				return fmt.Errorf("parse --env: %w", err)
			}

			definition := config.Command{
				Cmd:           shellCmd,
				Description:   description,
				Env:           env,
				DependsOn:     dependsOn,
				Aliases:       aliases,
				Preconditions: preconditions,
			}

			if err := setCommand(d, global, name, definition); err != nil {
				return err
			}

			scope := "local"
			if global {
				scope = "global"
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Command %q set in %s config.\n", name, scope); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&global, "global", "g", false, "write to the global config")
	cmd.Flags().StringVarP(&description, "description", "d", "", "human-readable description")
	cmd.Flags().StringArrayVarP(&envPairs, "env", "e", nil, "environment variable in KEY=VALUE format (repeatable)")
	cmd.Flags().StringArrayVar(&dependsOn, "depends-on", nil, "command names that must run first (repeatable)")
	cmd.Flags().StringArrayVar(&aliases, "alias", nil, "alternative name for this command (repeatable)")
	cmd.Flags().StringArrayVar(&preconditions, "precondition", nil, "shell expression that must exit 0 before running (repeatable)")

	return cmd
}

// newConfigRemoveCmd removes a command from the config.
func newConfigRemoveCmd(d *deps) *cobra.Command {
	var global bool

	cmd := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a command from the config",
		Long: `Remove a command definition from the local (or global) config.

Examples:
  projector config remove start
  projector config remove start --global`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: commandNamesCompletion(d),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			cfg, path, err := loadTargetConfig(d, global)
			if err != nil {
				return err
			}

			if cfg.Commands == nil {
				return fmt.Errorf("command %q not found in config", name)
			}

			if _, exists := cfg.Commands[name]; !exists {
				return fmt.Errorf("command %q not found in config", name)
			}

			delete(cfg.Commands, name)

			if err := config.SaveFile(path, cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			scope := "local"
			if global {
				scope = "global"
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Command %q removed from %s config.\n", name, scope); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&global, "global", "g", false, "remove from the global config")
	return cmd
}

// newConfigShowCmd prints the fully resolved merged config.
func newConfigShowCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the fully resolved config for the current context",
		Long: `Print the merged config resolved for the current working directory.

This shows the final result after merging global + ancestor + local configs,
which is exactly what 'projector run' uses to find commands.

Example:
  projector config show`,
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

			// Re-serialize as YAML for a clean, readable output.
			out := config.Config{
				Projects: merged.Projects,
				Commands: merged.Commands,
			}

			data, err := yaml.Marshal(out)
			if err != nil {
				return fmt.Errorf("marshal config: %w", err)
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "# Resolved config for: %s\n", pwd); err != nil {
				return err
			}
			if _, err := fmt.Fprint(cmd.OutOrStdout(), string(data)); err != nil {
				return err
			}
			return nil
		},
	}
}

// ---- helpers ----------------------------------------------------------------

// configPath returns the path of the target config file.
func configPath(d *deps, global bool) (string, error) {
	if global {
		return d.loader.GlobalPath(), nil
	}
	pwd, err := d.pwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return d.loader.LocalPath(pwd), nil
}

// loadTargetConfig loads the config from the appropriate file (local or global).
func loadTargetConfig(d *deps, global bool) (*config.Config, string, error) {
	path, err := configPath(d, global)
	if err != nil {
		return nil, "", err
	}

	var cfg *config.Config
	if global {
		cfg, err = d.loader.LoadGlobal()
	} else {
		pwd, pwdErr := d.pwd()
		if pwdErr != nil {
			return nil, "", fmt.Errorf("get working directory: %w", pwdErr)
		}
		cfg, err = d.loader.LoadLocal(pwd)
	}
	if err != nil {
		return nil, "", fmt.Errorf("load config: %w", err)
	}

	return cfg, path, nil
}

// setCommand writes a command definition into the appropriate config file.
func setCommand(d *deps, global bool, name string, cmd config.Command) error {
	cfg, path, err := loadTargetConfig(d, global)
	if err != nil {
		return err
	}

	if cfg.Commands == nil {
		cfg.Commands = make(map[string]config.Command)
	}
	cfg.Commands[name] = cmd

	if err := config.SaveFile(path, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	return nil
}

// ensureConfigExists creates the config file with a starter skeleton if absent.
// If the file already exists it is left untouched.
func ensureConfigExists(path string, global bool) error {
	if _, err := os.Stat(path); err == nil {
		return nil // File already exists; don't overwrite.
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat config file: %w", err)
	}

	var skeleton config.Config

	if global {
		skeleton = config.Config{
			Projects: map[string]config.Project{
				"example": {Path: "/path/to/project"},
			},
			Commands: map[string]config.Command{
				"start": {Cmd: "echo hello", Description: "Start the project"},
			},
		}
	} else {
		skeleton = config.Config{
			Commands: map[string]config.Command{
				"start": {Cmd: "echo hello", Description: "Start the project"},
			},
		}
	}

	return config.SaveFile(path, &skeleton)
}

// parseEnvPairs converts ["KEY=VALUE", ...] into a map.
func parseEnvPairs(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	m := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		idx := strings.IndexByte(pair, '=')
		if idx < 1 {
			return nil, fmt.Errorf("expected KEY=VALUE, got %q", pair)
		}
		m[pair[:idx]] = pair[idx+1:]
	}
	return m, nil
}

// commandNamesCompletion returns a completion function for command names in the current config.
func commandNamesCompletion(d *deps) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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
		sort.Strings(names)
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}
