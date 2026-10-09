package cli

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/akyrey/projector/internal/project"
	"github.com/akyrey/projector/internal/status"
)

// dockerTimeout bounds the single `docker ps` call so a stopped daemon does
// not hang the status table.
const dockerTimeout = 5 * time.Second

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
			projects, err := statusProjects(d, args)
			if err != nil {
				return err
			}

			ctx := rootContext()
			dockerCtx, cancel := context.WithTimeout(ctx, dockerTimeout)
			defer cancel()
			containers, dockerErr := status.ListContainers(dockerCtx)

			rows := status.Collect(ctx, projects, containers, d.pendingOnceCount)
			return status.Render(cmd.OutOrStdout(), rows, dockerErr == nil)
		},
	}
}

// statusProjects resolves the named projects, or all registered ones sorted by name.
func statusProjects(d *deps, names []string) ([]status.Project, error) {
	all, err := d.registry.List()
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}

	if len(names) == 0 {
		for name := range all {
			names = append(names, name)
		}
		sort.Strings(names)
	}

	projects := make([]status.Project, 0, len(names))
	for _, name := range names {
		p, ok := all[name]
		if !ok {
			return nil, fmt.Errorf("project %q: %w", name, project.ErrNotFound)
		}
		projects = append(projects, status.Project{Name: name, Dir: p.Path})
	}
	return projects, nil
}

// pendingOnceCount counts the once steps not yet applied in dir, across all
// commands resolved for it (aliases excluded).
func (d *deps) pendingOnceCount(dir string) (int, error) {
	merged, err := d.loader.LoadForProject(dir)
	if err != nil {
		return 0, fmt.Errorf("load config: %w", err)
	}
	total := 0
	for name, cmd := range merged.Commands {
		if cmd.AliasOf != "" {
			continue // counted under its canonical name
		}
		pending, err := d.pendingOnceSteps(dir, name, cmd)
		if err != nil {
			return 0, err
		}
		total += len(pending)
	}
	return total, nil
}
