package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Loader resolves the merged config for a given working directory.
// It walks from the global config up through ancestor directories to the cwd,
// merging configs so that closer (more specific) values override farther ones.
type Loader struct {
	// globalPath is the path to the global config file.
	globalPath string
}

// NewLoader creates a Loader using the default global config path.
func NewLoader() (*Loader, error) {
	globalPath, err := GlobalConfigPath()
	if err != nil {
		return nil, fmt.Errorf("resolve global config path: %w", err)
	}
	return &Loader{globalPath: globalPath}, nil
}

// NewLoaderWithGlobal creates a Loader with an explicit global config path.
// Primarily useful in tests.
func NewLoaderWithGlobal(globalPath string) *Loader {
	return &Loader{globalPath: globalPath}
}

// Load returns the merged config for the given working directory.
// Resolution order (first = lowest priority):
//  1. Global config (~/.config/projector/config.yaml)
//  2. Ancestor directory .projector.yaml files, from / down to pwd
//  3. pwd/.projector.yaml (highest priority)
func (l *Loader) Load(pwd string) (*MergedConfig, error) {
	merged := NewMergedConfig()

	// 1. Load global config.
	if err := l.applyFile(l.globalPath, merged); err != nil {
		return nil, fmt.Errorf("load global config: %w", err)
	}

	// 2. Collect directory chain from / down to pwd.
	dirs, err := dirChain(pwd)
	if err != nil {
		return nil, fmt.Errorf("build dir chain: %w", err)
	}

	// 3. Apply each directory's local config in order (ancestors first).
	for _, dir := range dirs {
		path := filepath.Join(dir, LocalConfigName)
		if err := l.applyFile(path, merged); err != nil {
			return nil, fmt.Errorf("load config %s: %w", path, err)
		}
	}

	return merged, nil
}

// LoadForProject returns the merged config for a project's directory.
// This is the same as Load but provides a more descriptive call site.
func (l *Loader) LoadForProject(projectPath string) (*MergedConfig, error) {
	return l.Load(projectPath)
}

// GlobalPath returns the path to the global config file.
func (l *Loader) GlobalPath() string {
	return l.globalPath
}

// LoadGlobal reads (only) the global config file.
// Returns an empty Config if the file does not exist.
func (l *Loader) LoadGlobal() (*Config, error) {
	cfg, err := LoadFile(l.globalPath)
	if err != nil {
		if errors.Is(err, ErrConfigNotFound) {
			return &Config{}, nil
		}
		return nil, err
	}
	return cfg, nil
}

// SaveGlobal writes cfg to the global config file.
func (l *Loader) SaveGlobal(cfg *Config) error {
	return SaveFile(l.globalPath, cfg)
}

// LoadLocal reads the local config in pwd (.projector.yaml).
// Returns an empty Config if the file does not exist.
func (l *Loader) LoadLocal(pwd string) (*Config, error) {
	path := filepath.Join(pwd, LocalConfigName)
	cfg, err := LoadFile(path)
	if err != nil {
		if errors.Is(err, ErrConfigNotFound) {
			return &Config{}, nil
		}
		return nil, err
	}
	return cfg, nil
}

// LocalPath returns the path to the local config in pwd.
func (l *Loader) LocalPath(pwd string) string {
	return filepath.Join(pwd, LocalConfigName)
}

// SaveLocal writes cfg to the local config in pwd.
func (l *Loader) SaveLocal(pwd string, cfg *Config) error {
	return SaveFile(filepath.Join(pwd, LocalConfigName), cfg)
}

// applyFile loads a config file and merges it into merged.
// If the file does not exist it is silently skipped.
func (l *Loader) applyFile(path string, merged *MergedConfig) error {
	cfg, err := LoadFile(path)
	if err != nil {
		if errors.Is(err, ErrConfigNotFound) {
			return nil // Missing files are fine.
		}
		return err
	}
	mergeInto(merged, cfg)
	return nil
}

// dirChain returns the chain of directories from the filesystem root down to pwd.
// e.g. for /home/user/work/my-api it returns:
//
//	["/", "/home", "/home/user", "/home/user/work", "/home/user/work/my-api"]
func dirChain(pwd string) ([]string, error) {
	abs, err := filepath.Abs(pwd)
	if err != nil {
		return nil, fmt.Errorf("abs path: %w", err)
	}

	// Walk from abs up to root, collecting each component.
	var parts []string
	curr := abs
	for {
		parts = append(parts, curr)
		parent := filepath.Dir(curr)
		if parent == curr {
			break // Reached the filesystem root.
		}
		curr = parent
	}

	// Reverse so we go root → pwd.
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}

	return parts, nil
}

// mergeInto applies src on top of dst.
// Commands and projects from src override those in dst with the same key.
func mergeInto(dst *MergedConfig, src *Config) {
	if src == nil {
		return
	}

	for name, proj := range src.Projects {
		dst.Projects[name] = proj
	}

	for name, cmd := range src.Commands {
		dst.Commands[name] = cmd
	}
}

// CurrentDir returns os.Getwd(), for use when no explicit pwd is provided.
func CurrentDir() (string, error) {
	pwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return pwd, nil
}
