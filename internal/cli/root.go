// Package cli contains all Cobra command definitions for projector.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/project"
	"github.com/akyrey/projector/internal/runner"
)

// globalFlags holds flag values shared across commands.
type globalFlags struct {
	// pwd overrides the working directory used for config resolution.
	pwd string

	// dryRun prints what would be executed without actually running anything.
	dryRun bool

	// choose opens an interactive picker to select a command before running.
	choose bool
}

// deps bundles shared dependencies injected into command handlers.
type deps struct {
	loader   *config.Loader
	registry *project.Registry
	runner   *runner.Runner
	flags    *globalFlags
}

// NewRootCmd builds and returns the root cobra.Command for projector.
func NewRootCmd(version string) *cobra.Command {
	loader, err := config.NewLoader()
	if err != nil {
		// Loader construction only fails when we cannot determine the home dir,
		// which is a fatal misconfiguration.
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	return newRootCmdWithLoader(version, loader)
}

// NewRootCmdWithGlobal builds the root command using an explicit global config
// path. Primarily useful in tests so they don't touch the real ~/.config.
func NewRootCmdWithGlobal(version, globalPath string) *cobra.Command {
	loader := config.NewLoaderWithGlobal(globalPath)
	return newRootCmdWithLoader(version, loader)
}

// newRootCmdWithLoader is the internal constructor shared by NewRootCmd and
// NewRootCmdWithGlobal.
func newRootCmdWithLoader(version string, loader *config.Loader) *cobra.Command {
	flags := &globalFlags{}

	d := &deps{
		loader:   loader,
		registry: project.NewRegistry(loader),
		runner:   runner.NewDefault(),
		flags:    flags,
	}

	root := &cobra.Command{
		Use:     "projector",
		Short:   "Run project commands across multiple directories",
		Version: version,
		Long: `projector lets you define named commands per project and run them
across one or more projects concurrently.

Commands are resolved from a hierarchy of config files:
  ~/.config/projector/config.yaml   (global)
  <dir>/.projector.yaml             (per-directory, walking up from cwd)

Examples:
  projector run start               # run 'start' in the current directory
  projector run start api frontend  # run 'start' in named projects concurrently
  projector start api frontend      # shorthand for the above
  projector list                    # show all resolved commands for current dir`,
		// Silence cobra's default error printing so we control the format.
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	root.PersistentFlags().StringVarP(
		&flags.pwd, "pwd", "p", "",
		"working directory (default: current directory)",
	)
	root.PersistentFlags().BoolVar(
		&flags.dryRun, "dry-run", false,
		"print what would be executed without actually running",
	)
	root.PersistentFlags().BoolVar(
		&flags.choose, "choose", false,
		"interactively pick a command from the resolved list before running",
	)

	// Register subcommands.
	root.AddCommand(newRunCmd(d))
	root.AddCommand(newListCmd(d))
	root.AddCommand(newProjectCmd(d))
	root.AddCommand(newConfigCmd(d))
	root.AddCommand(newCompletionCmd(root))

	// Shorthand: treat any unknown subcommand as `projector run <cmd> [projects...]`.
	// Extra args after -- are forwarded to the command.
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 && !d.flags.choose {
			return cmd.Help()
		}
		dashIdx := cmd.ArgsLenAtDash()
		var projectArgs, extraArgs []string
		if dashIdx == -1 {
			projectArgs = args
		} else {
			projectArgs = args[:dashIdx]
			extraArgs = args[dashIdx:]
		}

		if d.flags.choose && len(projectArgs) == 0 {
			// Launch interactive picker from root shorthand.
			pwd, err := d.pwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			merged, err := d.loader.Load(pwd)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			chosen, err := pickCommand(merged.Commands, os.Stdin, cmd.OutOrStdout())
			if err != nil {
				return fmt.Errorf("pick command: %w", err)
			}
			return runCommand(d, chosen, nil, extraArgs, d.flags.dryRun)
		}

		return runCommand(d, projectArgs[0], projectArgs[1:], extraArgs, d.flags.dryRun)
	}

	// Allow arbitrary args so the shorthand dispatch can intercept them before
	// cobra raises "unknown command" errors.
	root.Args = cobra.ArbitraryArgs

	return root
}

// pwd returns the effective working directory: the --pwd flag if set, else os.Getwd().
func (d *deps) pwd() (string, error) {
	if d.flags.pwd != "" {
		return d.flags.pwd, nil
	}
	return config.CurrentDir()
}
