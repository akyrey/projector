package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/akyrey/projector/internal/config"
)

func TestLoadFile_ParsesOnceSteps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	yaml := `commands:
  update:
    cmd: composer install
    once:
      - id: 2026-10-01-backfill
        description: Backfill wallets
        cmd: artisan wallets:backfill
      - id: 2026-10-05-reindex
        cmd: [artisan scout:flush, artisan scout:import]
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	once := cfg.Commands["update"].Once
	if len(once) != 2 {
		t.Fatalf("len(Once) = %d, want 2", len(once))
	}
	if once[0].ID != "2026-10-01-backfill" || once[0].Description != "Backfill wallets" ||
		once[0].Cmd.String() != "artisan wallets:backfill" {
		t.Errorf("Once[0] = %+v", once[0])
	}
	if once[1].ID != "2026-10-05-reindex" || len(once[1].Cmd.Values()) != 2 {
		t.Errorf("Once[1] = %+v", once[1])
	}
}

func TestLoad_ValidatesOnceSteps(t *testing.T) {
	step := func(id, cmd string) config.OnceStep {
		return config.OnceStep{ID: id, Cmd: config.NewStringOrList(cmd)}
	}

	tests := []struct {
		name    string
		once    []config.OnceStep
		wantErr bool
	}{
		{name: "valid", once: []config.OnceStep{step("a", "echo a"), step("b", "echo b")}},
		{name: "missing id", once: []config.OnceStep{step("", "echo a")}, wantErr: true},
		{name: "duplicate id", once: []config.OnceStep{step("a", "echo a"), step("a", "echo b")}, wantErr: true},
		{name: "empty cmd", once: []config.OnceStep{step("a", "")}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, filepath.Join(dir, config.LocalConfigName), &config.Config{
				Commands: map[string]config.Command{
					"update": {Cmd: config.NewStringOrList("true"), Once: tt.once},
				},
			})

			loader := config.NewLoaderWithGlobal(filepath.Join(t.TempDir(), "global.yaml"))
			_, err := loader.Load(dir)

			if tt.wantErr {
				if !errors.Is(err, config.ErrInvalidOnceStep) {
					t.Fatalf("err = %v, want ErrInvalidOnceStep", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
		})
	}
}

func TestPendingOnceSteps(t *testing.T) {
	steps := []config.OnceStep{
		{ID: "a", Cmd: config.NewStringOrList("echo a")},
		{ID: "b", Cmd: config.NewStringOrList("echo b")},
		{ID: "c", Cmd: config.NewStringOrList("echo c")},
	}

	tests := []struct {
		name    string
		applied map[string]bool
		want    []string
	}{
		{name: "none applied", applied: nil, want: []string{"a", "b", "c"}},
		{name: "some applied keeps order", applied: map[string]bool{"b": true}, want: []string{"a", "c"}},
		{name: "all applied", applied: map[string]bool{"a": true, "b": true, "c": true}, want: nil},
		{name: "unknown applied ids ignored", applied: map[string]bool{"zzz": true}, want: []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := config.PendingOnceSteps(steps, func(id string) bool { return tt.applied[id] })
			if len(got) != len(tt.want) {
				t.Fatalf("pending = %v, want %v", got, tt.want)
			}
			for i, s := range got {
				if s.ID != tt.want[i] {
					t.Errorf("pending[%d] = %q, want %q", i, s.ID, tt.want[i])
				}
			}
		})
	}
}
