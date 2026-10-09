package state_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/akyrey/projector/internal/state"
)

var at = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func TestStore_AppliedEmptyWhenNothingRecorded(t *testing.T) {
	s := state.NewStoreAt(t.TempDir())

	applied, err := s.Applied("/work/api", "update")
	if err != nil {
		t.Fatalf("Applied: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("Applied = %v, want empty", applied)
	}
}

func TestStore_MarkAppliedIsScopedByProjectAndCommand(t *testing.T) {
	s := state.NewStoreAt(t.TempDir())

	if err := s.MarkApplied("/work/api", "update", "step-1", at); err != nil {
		t.Fatalf("MarkApplied: %v", err)
	}

	tests := []struct {
		name, dir, command string
		want               bool
	}{
		{"same project and command", "/work/api", "update", true},
		{"other command", "/work/api", "sync", false},
		{"other project", "/work/web", "update", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applied, err := s.Applied(tt.dir, tt.command)
			if err != nil {
				t.Fatalf("Applied: %v", err)
			}
			got, ok := applied["step-1"]
			if ok != tt.want {
				t.Fatalf("step-1 applied = %v, want %v", ok, tt.want)
			}
			if ok && !got.Equal(at) {
				t.Errorf("applied at %v, want %v", got, at)
			}
		})
	}
}

func TestStore_PersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	if err := state.NewStoreAt(dir).MarkApplied("/work/api", "update", "step-1", at); err != nil {
		t.Fatalf("MarkApplied: %v", err)
	}

	applied, err := state.NewStoreAt(dir).Applied("/work/api", "update")
	if err != nil {
		t.Fatalf("Applied: %v", err)
	}
	if _, ok := applied["step-1"]; !ok {
		t.Errorf("step-1 not persisted: %v", applied)
	}
}

func TestStore_ConcurrentMarkApplied(t *testing.T) {
	s := state.NewStoreAt(t.TempDir())
	const n = 20

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- s.MarkApplied("/work/api", "update", fmt.Sprintf("step-%d", i), at)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("MarkApplied: %v", err)
		}
	}

	applied, err := s.Applied("/work/api", "update")
	if err != nil {
		t.Fatalf("Applied: %v", err)
	}
	if len(applied) != n {
		t.Errorf("recorded %d steps, want %d", len(applied), n)
	}
}

func TestStore_CorruptFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	s := state.NewStoreAt(dir)
	if err := s.MarkApplied("/work/api", "update", "step-1", at); err != nil {
		t.Fatalf("MarkApplied: %v", err)
	}

	files, err := filepath.Glob(filepath.Join(dir, "*", "*.yaml"))
	if err != nil || len(files) != 1 {
		t.Fatalf("state files = %v (err %v), want exactly 1", files, err)
	}
	if err := os.WriteFile(files[0], []byte("commands: [not, a, map"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Applied("/work/api", "update"); err == nil {
		t.Error("Applied on corrupt file: want error, got nil")
	}
}

func TestDefaultDir(t *testing.T) {
	t.Run("respects XDG_STATE_HOME", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "/xdg/state")
		got, err := state.DefaultDir()
		if err != nil {
			t.Fatalf("DefaultDir: %v", err)
		}
		if want := filepath.Join("/xdg/state", "projector"); got != want {
			t.Errorf("DefaultDir = %q, want %q", got, want)
		}
	})

	t.Run("falls back to ~/.local/state", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("HOME", "/home/me")
		got, err := state.DefaultDir()
		if err != nil {
			t.Fatalf("DefaultDir: %v", err)
		}
		if want := filepath.Join("/home/me", ".local", "state", "projector"); got != want {
			t.Errorf("DefaultDir = %q, want %q", got, want)
		}
	})
}
