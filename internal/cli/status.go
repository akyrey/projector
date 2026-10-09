package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/akyrey/projector/internal/status"
)

func newStatusCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "status [project...]",
		Short: "Show git, container, and pending once-step state of projects",
		Long: `Show the state of registered projects (all of them when none are named):

  BRANCH      current git branch
  GIT         commits ahead (↑) / behind (↓) upstream and uncommitted changes
  CONTAINERS  docker compose containers running in the project directory
  ONCE STEPS  once steps not yet applied on this machine (run the command to apply)

Examples:
  projector status
  projector status core-api webshop`,
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return completeProjectNamesSlice(d), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			projects, err := d.app.Projects(args)
			if err != nil {
				return err
			}

			ctx := rootContext()
			dockerCtx, cancel := context.WithTimeout(ctx, status.DockerTimeout)
			defer cancel()
			containers, dockerErr := status.ListContainers(dockerCtx)

			rows := status.Collect(ctx, projects, containers, d.app.PendingOnceCount)
			return status.Render(cmd.OutOrStdout(), rows, dockerErr == nil)
		},
	}
}
