package project_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/project"
)

func newTestRegistry(t *testing.T) (*project.Registry, string) {
	t.Helper()
	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	loader := config.NewLoaderWithGlobal(globalPath)
	return project.NewRegistry(loader), globalPath
}

// TestRegistry_Add adds a project and verifies it can be retrieved.
func TestRegistry_Add(t *testing.T) {
	reg, _ := newTestRegistry(t)
	dir := t.TempDir()

	if err := reg.Add("my-api", dir); err != nil {
		t.Fatalf("Add: %v", err)
	}

	proj, err := reg.Get("my-api")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Path is resolved to absolute.
	abs, _ := filepath.Abs(dir)
	if proj.Path != abs {
		t.Errorf("path: got %q, want %q", proj.Path, abs)
	}
}

// TestRegistry_Add_DuplicateReturnsError verifies ErrAlreadyExists.
func TestRegistry_Add_DuplicateReturnsError(t *testing.T) {
	reg, _ := newTestRegistry(t)
	dir := t.TempDir()

	if err := reg.Add("my-api", dir); err != nil {
		t.Fatalf("first Add: %v", err)
	}

	err := reg.Add("my-api", dir)
	if !errors.Is(err, project.ErrAlreadyExists) {
		t.Errorf("expected ErrAlreadyExists, got %v", err)
	}
}

// TestRegistry_Remove deletes a project.
func TestRegistry_Remove(t *testing.T) {
	reg, _ := newTestRegistry(t)
	dir := t.TempDir()

	if err := reg.Add("my-api", dir); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := reg.Remove("my-api"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	_, err := reg.Get("my-api")
	if !errors.Is(err, project.ErrNotFound) {
		t.Errorf("expected ErrNotFound after remove, got %v", err)
	}
}

// TestRegistry_Remove_Missing returns ErrNotFound.
func TestRegistry_Remove_Missing(t *testing.T) {
	reg, _ := newTestRegistry(t)

	err := reg.Remove("ghost")
	if !errors.Is(err, project.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestRegistry_List returns all registered projects.
func TestRegistry_List(t *testing.T) {
	reg, _ := newTestRegistry(t)
	dirs := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	names := []string{"alpha", "beta", "gamma"}

	for i, name := range names {
		if err := reg.Add(name, dirs[i]); err != nil {
			t.Fatalf("Add %q: %v", name, err)
		}
	}

	projects, err := reg.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(projects) != 3 {
		t.Errorf("len: got %d, want 3", len(projects))
	}

	for _, name := range names {
		if _, ok := projects[name]; !ok {
			t.Errorf("missing project %q", name)
		}
	}
}

// TestRegistry_List_Empty returns empty map when no projects registered.
func TestRegistry_List_Empty(t *testing.T) {
	reg, _ := newTestRegistry(t)

	projects, err := reg.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(projects) != 0 {
		t.Errorf("expected empty, got %d", len(projects))
	}
}
