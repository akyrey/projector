package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DotEnvName is the filename searched for in each project directory.
const DotEnvName = ".env"

// LoadDotEnv reads a .env file from dir and returns a map of key→value pairs.
// Lines beginning with # (after trimming whitespace) and blank lines are ignored.
// Inline comments (# after value) are NOT stripped — the full value after = is used,
// which matches how most shells treat .env files.
//
// Returns an empty map (not an error) when the file does not exist.
func LoadDotEnv(dir string) (map[string]string, error) {
	path := filepath.Join(dir, DotEnvName)
	return LoadDotEnvFile(path)
}

// LoadDotEnvFile reads the .env file at the given path and returns a key→value map.
// Returns an empty map (not an error) when the file does not exist.
func LoadDotEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	result := make(map[string]string)
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip blank lines and comments.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Require KEY=VALUE format.
		idx := strings.IndexByte(line, '=')
		if idx < 1 {
			return nil, fmt.Errorf("%s line %d: expected KEY=VALUE, got %q", path, lineNum, line)
		}

		key := strings.TrimSpace(line[:idx])
		val := line[idx+1:] // preserve value exactly, including surrounding whitespace

		// Strip optional surrounding quotes (single or double) if balanced.
		val = stripQuotes(val)

		if key == "" {
			return nil, fmt.Errorf("%s line %d: empty key", path, lineNum)
		}

		result[key] = val
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}

	return result, nil
}

// MergeEnv merges base and override maps, returning a new map where override
// values take precedence over base values for the same key.
func MergeEnv(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	result := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range override {
		result[k] = v
	}
	return result
}

// stripQuotes removes surrounding single or double quotes from s if they match.
func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
