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
//
// After all layers are merged, service definitions are expanded into commands.
// See expandServices for expansion and merge semantics.
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

	// 4. Expand service definitions into commands now that all layers are merged.
	expandServices(merged)

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
// Commands, projects, and services from src override those in dst with the same key.
// For each command that declares aliases, each alias is also registered in dst
// pointing to the same definition (with Aliases cleared to avoid confusion).
func mergeInto(dst *MergedConfig, src *Config) {
	if src == nil {
		return
	}

	for name, proj := range src.Projects {
		dst.Projects[name] = proj
	}

	for name, cmd := range src.Commands {
		dst.Commands[name] = cmd

		// Register each alias as its own entry so `projector run <alias>` works.
		for _, alias := range cmd.Aliases {
			if alias == name {
				continue // skip self-alias
			}
			aliasCmd := cmd
			aliasCmd.Aliases = nil // aliases of aliases are not expanded
			dst.Commands[alias] = aliasCmd
		}
	}

	for name, svc := range src.Services {
		dst.Services[name] = svc
	}
}

// expandServices expands service definitions in merged into commands.
// It is called once, after all config layers have been merged.
//
// For each service, each entry in Service.Commands produces a Command whose
// Cmd is "<service.Exec> <suffix>". The expansion interacts with any explicit
// commands already in merged.Commands as follows:
//
//   - If no explicit command exists with that name, the generated command is
//     inserted as-is.
//   - If an explicit command exists and has a non-empty Cmd field, it fully
//     overrides the generated command (the user chose a different implementation).
//   - If an explicit command exists but has an empty Cmd field, the generated
//     Cmd is filled in and all other fields (description, env, depends_on, etc.)
//     from the explicit entry are preserved. This lets callers add metadata to
//     a service-generated command without repeating the exec prefix.
func expandServices(merged *MergedConfig) {
	for _, svc := range merged.Services {
		if svc.Exec == "" {
			continue // nothing to expand
		}
		for cmdName, suffix := range svc.Commands {
			generatedCmd := svc.Exec
			if suffix != "" {
				generatedCmd = svc.Exec + " " + suffix
			}

			existing, exists := merged.Commands[cmdName]
			switch {
			case !exists:
				// No explicit command — insert the generated one.
				merged.Commands[cmdName] = Command{Cmd: NewStringOrList(generatedCmd)}
			case existing.Cmd.IsEmpty():
				// Explicit entry has no cmd — fill in the generated cmd, keep metadata.
				existing.Cmd = NewStringOrList(generatedCmd)
				merged.Commands[cmdName] = existing
			default:
				// Explicit entry has its own cmd — leave it untouched.
			}
		}
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
