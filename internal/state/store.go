// Package state records machine-local execution state that must not live in a
// project's repository, such as which once steps have already been applied.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// stateDirName is the directory (within XDG_STATE_HOME or ~/.local/state).
	stateDirName = "projector"

	// onceDirName holds one file per project directory.
	onceDirName = "once"

	// fileKeyLen is the number of hex characters of the path hash used as file name.
	fileKeyLen = 16
)

// projectFile is the on-disk format: the steps applied in one project directory.
type projectFile struct {
	// Path is the project directory, kept for humans inspecting the file.
	Path string `yaml:"path"`

	// Commands maps command name → step ID → time the step succeeded.
	Commands map[string]map[string]time.Time `yaml:"commands"`
}

// Store records applied once steps, one file per project directory so that
// concurrent projector processes working on different projects never write
// the same file.
type Store struct {
	dir string
	mu  sync.Mutex // serialises read-modify-write within this process
}

// DefaultDir returns the state directory, respecting XDG_STATE_HOME and
// falling back to ~/.local/state.
func DefaultDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home dir: %w", err)
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, stateDirName), nil
}

// NewStore creates a Store in DefaultDir.
func NewStore() (*Store, error) {
	dir, err := DefaultDir()
	if err != nil {
		return nil, fmt.Errorf("resolve state dir: %w", err)
	}
	return NewStoreAt(dir), nil
}

// NewStoreAt creates a Store rooted at dir. Primarily useful in tests.
func NewStoreAt(dir string) *Store {
	return &Store{dir: dir}
}

// Applied returns the IDs of the once steps applied for command in projectDir,
// with the time each one succeeded.
func (s *Store) Applied(projectDir, command string) (map[string]time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pf, err := s.read(projectDir)
	if err != nil {
		return nil, err
	}
	applied := make(map[string]time.Time, len(pf.Commands[command]))
	for id, at := range pf.Commands[command] {
		applied[id] = at
	}
	return applied, nil
}

// MarkApplied records that step stepID of command succeeded in projectDir.
func (s *Store) MarkApplied(projectDir, command, stepID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	pf, err := s.read(projectDir)
	if err != nil {
		return err
	}
	if pf.Commands[command] == nil {
		pf.Commands[command] = make(map[string]time.Time)
	}
	pf.Commands[command][stepID] = at.UTC()

	return s.write(projectDir, pf)
}

// read loads the file for projectDir; a missing file yields an empty record.
func (s *Store) read(projectDir string) (*projectFile, error) {
	path, err := s.filePath(projectDir)
	if err != nil {
		return nil, err
	}

	pf := &projectFile{Path: projectDir, Commands: make(map[string]map[string]time.Time)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return pf, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, pf); err != nil {
		return nil, fmt.Errorf("parse state %s: %w", path, err)
	}
	if pf.Commands == nil {
		pf.Commands = make(map[string]map[string]time.Time)
	}
	return pf, nil
}

// write saves pf atomically (temp file + rename) so a crash never leaves a
// truncated state file behind.
func (s *Store) write(projectDir string, pf *projectFile) error {
	path, err := s.filePath(projectDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	data, err := yaml.Marshal(pf)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after a successful rename

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close state: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("save state %s: %w", path, err)
	}
	return nil
}

// filePath maps a project directory to its state file, keyed by a hash of the
// absolute path so any path is a valid file name.
func (s *Store) filePath(projectDir string) (string, error) {
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return "", fmt.Errorf("abs path: %w", err)
	}
	sum := sha256.Sum256([]byte(abs))
	name := hex.EncodeToString(sum[:])[:fileKeyLen] + ".yaml"
	return filepath.Join(s.dir, onceDirName, name), nil
}
