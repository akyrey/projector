// Package project provides project registration and lookup against the global config.
package project

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/akyrey/projector/internal/config"
)

// ErrNotFound is returned when a project name is not registered.
var ErrNotFound = errors.New("project not found")

// ErrAlreadyExists is returned when trying to add a project that is already registered.
var ErrAlreadyExists = errors.New("project already exists")

// Registry manages named projects stored in the global config.
type Registry struct {
	loader *config.Loader
}

// NewRegistry creates a Registry using the given Loader.
func NewRegistry(loader *config.Loader) *Registry {
	return &Registry{loader: loader}
}

// Add registers a new project with the given name and path.
// Returns ErrAlreadyExists if the name is already in use.
func (r *Registry) Add(name, path string) error {
	// Resolve to an absolute path for consistency.
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	cfg, err := r.loader.LoadGlobal()
	if err != nil {
		return fmt.Errorf("load global config: %w", err)
	}

	if cfg.Projects == nil {
		cfg.Projects = make(map[string]config.Project)
	}

	if _, exists := cfg.Projects[name]; exists {
		return fmt.Errorf("%w: %s", ErrAlreadyExists, name)
	}

	cfg.Projects[name] = config.Project{Path: abs}

	if err := r.loader.SaveGlobal(cfg); err != nil {
		return fmt.Errorf("save global config: %w", err)
	}

	return nil
}

// Remove deletes the project with the given name from the global config.
// Returns ErrNotFound if the project does not exist.
func (r *Registry) Remove(name string) error {
	cfg, err := r.loader.LoadGlobal()
	if err != nil {
		return fmt.Errorf("load global config: %w", err)
	}

	if cfg.Projects == nil {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}

	if _, exists := cfg.Projects[name]; !exists {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}

	delete(cfg.Projects, name)

	if err := r.loader.SaveGlobal(cfg); err != nil {
		return fmt.Errorf("save global config: %w", err)
	}

	return nil
}

// Get returns the Project for the given name.
// Returns ErrNotFound if the project is not registered.
func (r *Registry) Get(name string) (config.Project, error) {
	cfg, err := r.loader.LoadGlobal()
	if err != nil {
		return config.Project{}, fmt.Errorf("load global config: %w", err)
	}

	proj, exists := cfg.Projects[name]
	if !exists {
		return config.Project{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}

	return proj, nil
}

// List returns all registered projects sorted by name.
func (r *Registry) List() (map[string]config.Project, error) {
	cfg, err := r.loader.LoadGlobal()
	if err != nil {
		return nil, fmt.Errorf("load global config: %w", err)
	}

	if cfg.Projects == nil {
		return map[string]config.Project{}, nil
	}

	return cfg.Projects, nil
}
