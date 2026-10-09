package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akyrey/projector/internal/config"
)

func info(name, cmd string) config.InfoItem {
	return config.InfoItem{Name: name, Cmd: config.NewStringOrList(cmd)}
}

func infoSummary(items []config.InfoItem) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = it.Name + "=" + it.Cmd.String()
	}
	return strings.Join(parts, ",")
}

func TestLoadFile_ParsesInfo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	yaml := `info:
  - name: Migrations
    description: Pending database migrations
    cmd: ./xenv artisan migrate:status --pending
  - name: Versions
    cmd: [php -v, node -v]
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got := infoSummary(cfg.Info); got != "Migrations=./xenv artisan migrate:status --pending,Versions=php -v && node -v" {
		t.Errorf("Info = %s", got)
	}
	if cfg.Info[0].Description != "Pending database migrations" {
		t.Errorf("Description = %q", cfg.Info[0].Description)
	}
}

func TestLoad_InfoMerge(t *testing.T) {
	tests := []struct {
		name string
		root []config.InfoItem
		leaf []config.InfoItem
		want string
	}{
		{
			name: "closer layer appends new items",
			root: []config.InfoItem{info("a", "echo a")},
			leaf: []config.InfoItem{info("b", "echo b")},
			want: "a=echo a,b=echo b",
		},
		{
			name: "same name replaces in place",
			root: []config.InfoItem{info("a", "echo a"), info("b", "echo b")},
			leaf: []config.InfoItem{info("a", "echo A")},
			want: "a=echo A,b=echo b",
		},
		{
			name: "same name without cmd removes it",
			root: []config.InfoItem{info("a", "echo a"), info("b", "echo b")},
			leaf: []config.InfoItem{{Name: "a"}},
			want: "b=echo b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, _, leaf := nestedDirs(t)
			writeConfig(t, filepath.Join(root, config.LocalConfigName), &config.Config{Info: tt.root})
			writeConfig(t, filepath.Join(leaf, config.LocalConfigName), &config.Config{Info: tt.leaf})

			merged, err := config.NewLoaderWithGlobal(filepath.Join(t.TempDir(), "g.yaml")).Load(leaf)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := infoSummary(merged.Info); got != tt.want {
				t.Errorf("Info = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestLoad_InfoRequiresName(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, filepath.Join(dir, config.LocalConfigName), &config.Config{
		Info: []config.InfoItem{{Cmd: config.NewStringOrList("echo x")}},
	})
	_, err := config.NewLoaderWithGlobal(filepath.Join(t.TempDir(), "g.yaml")).Load(dir)
	if !errors.Is(err, config.ErrInvalidInfoItem) {
		t.Errorf("Load err = %v, want ErrInvalidInfoItem", err)
	}
}
