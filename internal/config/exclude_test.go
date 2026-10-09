package config_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/akyrey/projector/internal/config"
)

// commandNames returns the sorted command names of a merged config.
func commandNames(m *config.MergedConfig) []string {
	names := make([]string, 0, len(m.Commands))
	for name := range m.Commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// nestedDirs creates root/a/b and returns (root, root/a, root/a/b).
func nestedDirs(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	mid := filepath.Join(root, "a")
	leaf := filepath.Join(mid, "b")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, mid, leaf
}

func TestLoad_Exclude(t *testing.T) {
	tests := []struct {
		name   string
		root   *config.Config
		mid    *config.Config
		leaf   *config.Config
		want   []string
		wantOK map[string]string // command name → expected cmd string
	}{
		{
			name: "removes an inherited command",
			root: &config.Config{Commands: map[string]config.Command{
				"start":  {Cmd: config.NewStringOrList("up")},
				"xdebug": {Cmd: config.NewStringOrList("toggle")},
			}},
			leaf: &config.Config{Exclude: []string{"xdebug"}},
			want: []string{"start"},
		},
		{
			name: "removes a service-generated command",
			root: &config.Config{Services: map[string]config.Service{
				"x": {Exec: "./xenv", Commands: map[string]string{"artisan": "artisan", "composer": "composer"}},
			}},
			leaf: &config.Config{Exclude: []string{"artisan"}},
			want: []string{"composer"},
		},
		{
			name: "a closer layer can define the command again",
			root: &config.Config{Commands: map[string]config.Command{
				"start": {Cmd: config.NewStringOrList("up")},
			}},
			mid: &config.Config{Exclude: []string{"start"}},
			leaf: &config.Config{Commands: map[string]config.Command{
				"start": {Cmd: config.NewStringOrList("pnpm next")},
			}},
			want:   []string{"start"},
			wantOK: map[string]string{"start": "pnpm next"},
		},
		{
			name: "a closer service-generated command is kept when defined explicitly",
			root: &config.Config{Services: map[string]config.Service{
				"x": {Exec: "./xenv", Commands: map[string]string{"shell": "shell"}},
			}},
			mid: &config.Config{Exclude: []string{"shell"}},
			leaf: &config.Config{Commands: map[string]config.Command{
				"shell": {Description: "metadata only"},
			}},
			want:   []string{"shell"},
			wantOK: map[string]string{"shell": "./xenv shell"},
		},
		{
			name: "excluding an unknown name is a no-op",
			root: &config.Config{Commands: map[string]config.Command{
				"start": {Cmd: config.NewStringOrList("up")},
			}},
			leaf: &config.Config{Exclude: []string{"nope"}},
			want: []string{"start"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, mid, leaf := nestedDirs(t)
			for dir, cfg := range map[string]*config.Config{root: tt.root, mid: tt.mid, leaf: tt.leaf} {
				if cfg != nil {
					writeConfig(t, filepath.Join(dir, config.LocalConfigName), cfg)
				}
			}

			loader := config.NewLoaderWithGlobal(filepath.Join(t.TempDir(), "global.yaml"))
			merged, err := loader.Load(leaf)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			got := commandNames(merged)
			if len(got) != len(tt.want) {
				t.Fatalf("commands = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("commands = %v, want %v", got, tt.want)
				}
			}
			for name, wantCmd := range tt.wantOK {
				if c := merged.Commands[name].Cmd.String(); c != wantCmd {
					t.Errorf("%s cmd = %q, want %q", name, c, wantCmd)
				}
			}
		})
	}
}

func TestLoadFile_ParsesExclude(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte("exclude: [xdebug, artisan]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(cfg.Exclude) != 2 || cfg.Exclude[0] != "xdebug" || cfg.Exclude[1] != "artisan" {
		t.Errorf("Exclude = %v, want [xdebug artisan]", cfg.Exclude)
	}
}

func TestLoad_ExcludeRemovesAliases(t *testing.T) {
	root, _, leaf := nestedDirs(t)
	writeConfig(t, filepath.Join(root, config.LocalConfigName), &config.Config{Commands: map[string]config.Command{
		"artisan": {Cmd: config.NewStringOrList("php artisan"), Aliases: []string{"art"}},
		"start":   {Cmd: config.NewStringOrList("up")},
	}})
	writeConfig(t, filepath.Join(leaf, config.LocalConfigName), &config.Config{Exclude: []string{"artisan"}})

	merged, err := config.NewLoaderWithGlobal(filepath.Join(t.TempDir(), "g.yaml")).Load(leaf)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := commandNames(merged); len(got) != 1 || got[0] != "start" {
		t.Errorf("commands = %v, want [start]", got)
	}
}

func TestLoad_AliasRecordsCanonicalName(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, filepath.Join(dir, config.LocalConfigName), &config.Config{Commands: map[string]config.Command{
		"update": {Cmd: config.NewStringOrList("true"), Aliases: []string{"u"}},
	}})
	merged, err := config.NewLoaderWithGlobal(filepath.Join(t.TempDir(), "g.yaml")).Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := merged.Commands["u"].AliasOf; got != "update" {
		t.Errorf("u.AliasOf = %q, want update", got)
	}
	if got := merged.Commands["update"].AliasOf; got != "" {
		t.Errorf("update.AliasOf = %q, want empty", got)
	}
}

func TestLoader_ProjectRoot(t *testing.T) {
	root, mid, leaf := nestedDirs(t)
	writeConfig(t, filepath.Join(mid, config.LocalConfigName), &config.Config{})
	loader := config.NewLoaderWithGlobal(filepath.Join(t.TempDir(), "g.yaml"))

	realMid, err := filepath.EvalSymlinks(mid)
	if err != nil {
		t.Fatal(err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ from, want string }{
		{leaf, realMid},
		{mid, realMid},
		{root, realRoot}, // no config at or above: the dir itself
	} {
		got, err := loader.ProjectRoot(tt.from)
		if err != nil {
			t.Fatalf("ProjectRoot(%s): %v", tt.from, err)
		}
		if got != tt.want {
			t.Errorf("ProjectRoot(%s) = %q, want %q", tt.from, got, tt.want)
		}
	}
}
