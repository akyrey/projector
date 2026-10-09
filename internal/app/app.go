// Package app holds the use cases shared by the CLI and the web server:
// running commands (with cross-project deps and once steps), listing
// projects, and counting pending once steps.
package app

import (
	"fmt"
	"sort"

	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/project"
	"github.com/akyrey/projector/internal/status"
)

// App wires config loading, the project registry and machine-local state.
// It is safe for concurrent use.
type App struct {
	loader   *config.Loader
	registry *project.Registry

	// state holds applied once steps; created on first use.
	state lazyStore

	// infoSlots bounds info commands running at once.
	infoSlots chan struct{}
}

// New creates an App backed by loader.
func New(loader *config.Loader) *App {
	return &App{
		loader:    loader,
		registry:  project.NewRegistry(loader),
		infoSlots: make(chan struct{}, maxInfoConcurrency),
	}
}

// Loader returns the config loader.
func (a *App) Loader() *config.Loader { return a.loader }

// Registry returns the project registry.
func (a *App) Registry() *project.Registry { return a.registry }

// Projects resolves the named projects, or all registered ones sorted by name.
// An unknown name yields project.ErrNotFound.
func (a *App) Projects(names []string) ([]status.Project, error) {
	all, err := a.registry.List()
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

// PendingOnceCount counts the once steps not yet applied in dir, across all
// commands resolved for it (aliases excluded).
func (a *App) PendingOnceCount(dir string) (int, error) {
	merged, err := a.loader.LoadForProject(dir)
	if err != nil {
		return 0, fmt.Errorf("load config: %w", err)
	}
	total := 0
	for name, cmd := range merged.Commands {
		if cmd.AliasOf != "" {
			continue // counted under its canonical name
		}
		pending, err := a.PendingOnceSteps(dir, name, cmd)
		if err != nil {
			return 0, err
		}
		total += len(pending)
	}
	return total, nil
}
