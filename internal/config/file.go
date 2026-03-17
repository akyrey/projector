package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	// LocalConfigName is the filename searched in each directory when walking up.
	LocalConfigName = ".projector.yaml"

	// GlobalConfigDir is the directory (within XDG_CONFIG_HOME or ~/.config) for global config.
	GlobalConfigDir = "projector"

	// GlobalConfigName is the filename for the global config.
	GlobalConfigName = "config.yaml"
)

// ErrConfigNotFound is returned when a config file does not exist.
var ErrConfigNotFound = errors.New("config file not found")

// LoadFile reads and parses a single config file at path.
// Returns ErrConfigNotFound if the file does not exist.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrConfigNotFound
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	return &cfg, nil
}

// SaveFile writes cfg to path, creating parent directories as needed.
func SaveFile(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}

	return nil
}

// GlobalConfigPath returns the path to the global config file.
// It respects XDG_CONFIG_HOME, falling back to ~/.config.
func GlobalConfigPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home dir: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, GlobalConfigDir, GlobalConfigName), nil
}
