package config

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// StringOrList holds either a single shell command string or an ordered list
// of shell command strings.  When multiple strings are provided they are
// executed sequentially; the chain aborts on the first non-zero exit.
//
// YAML accepts both forms:
//
//	cmd: "sail down && sail up -d"          # scalar — single command
//	cmd:                                    # sequence — executed in order
//	  - "./vendor/bin/sail down"
//	  - "./vendor/bin/sail up -d"
type StringOrList struct {
	values []string
}

// NewStringOrList creates a StringOrList from one or more command strings.
func NewStringOrList(cmds ...string) StringOrList {
	return StringOrList{values: cmds}
}

// IsEmpty reports whether there are no non-empty command strings.
func (s StringOrList) IsEmpty() bool {
	for _, v := range s.values {
		if v != "" {
			return false
		}
	}
	return true
}

// IsMulti reports whether the list contains more than one command string.
func (s StringOrList) IsMulti() bool { return len(s.values) > 1 }

// Values returns the underlying slice of command strings.
func (s StringOrList) Values() []string { return s.values }

// String returns a human-readable representation.  Multiple commands are
// joined with " && " so the output reads like a shell one-liner.
func (s StringOrList) String() string { return strings.Join(s.values, " && ") }

// UnmarshalYAML implements yaml.Unmarshaler.
// It accepts both a YAML scalar (single string) and a YAML sequence ([]string).
// A null node or an empty scalar is treated as an empty StringOrList.
func (s *StringOrList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		// null tag or empty value → stay empty
		if value.Tag == "!!null" || value.Value == "" {
			s.values = nil
			return nil
		}
		s.values = []string{value.Value}
	case yaml.SequenceNode:
		var list []string
		if err := value.Decode(&list); err != nil {
			return err
		}
		s.values = list
	default:
		return &yaml.TypeError{Errors: []string{
			"cmd: expected a string or a list of strings",
		}}
	}
	return nil
}

// MarshalYAML implements yaml.Marshaler.
// A single-element list is emitted as a scalar for round-trip cleanliness;
// multi-element lists are emitted as a YAML sequence.
// An empty list is emitted as nil so that omitempty suppresses the field.
func (s StringOrList) MarshalYAML() (interface{}, error) {
	switch len(s.values) {
	case 0:
		return nil, nil
	case 1:
		return s.values[0], nil
	default:
		return s.values, nil
	}
}

// Command represents a runnable command definition.
// It stores the shell command string along with optional metadata like
// environment variables, a description, and dependencies.
type Command struct {
	// Cmd is the shell command (or ordered list of commands) to execute.
	// When multiple commands are given they run sequentially; the chain
	// aborts on the first non-zero exit.
	Cmd StringOrList `yaml:"cmd"`

	// Description is a human-readable summary of what this command does.
	Description string `yaml:"description,omitempty"`

	// Env holds additional environment variables to set when running the command.
	Env map[string]string `yaml:"env,omitempty"`

	// DependsOn lists other command names that must complete before this one runs.
	DependsOn []string `yaml:"depends_on,omitempty"`

	// Aliases lists alternative names that resolve to this command.
	// Each alias is registered as a separate entry in the merged command map.
	Aliases []string `yaml:"aliases,omitempty"`

	// Preconditions is a list of shell expressions that must all exit 0 before
	// this command runs. If any fails, execution is aborted with an error.
	Preconditions []string `yaml:"preconditions,omitempty"`
}

// Service defines a container execution context that auto-generates commands.
// Each entry in Commands maps a projector command name to the suffix that is
// appended to Exec when building the final shell command string.
//
// Example:
//
//	services:
//	  sail:
//	    exec: "docker compose exec laravel.test"
//	    commands:
//	      artisan: "php artisan"
//	      composer: "composer"
//
// This auto-generates:
//
//	artisan: docker compose exec laravel.test php artisan
//	composer: docker compose exec laravel.test composer
type Service struct {
	// Exec is the prefix command used to enter the container, e.g.
	// "docker compose exec laravel.test".
	Exec string `yaml:"exec"`

	// Commands maps projector command names to the suffix executed inside the
	// container. The generated command is "<Exec> <suffix>".
	Commands map[string]string `yaml:"commands,omitempty"`
}

// Project represents a registered named project with a filesystem path.
type Project struct {
	// Path is the absolute filesystem path to the project root.
	Path string `yaml:"path"`
}

// Config represents the contents of a single projector config file.
// Multiple configs are merged hierarchically (global < ancestor dirs < cwd).
type Config struct {
	// Projects maps project names to their definitions.
	// Only meaningful in the global config; ignored in directory-local files.
	Projects map[string]Project `yaml:"projects,omitempty"`

	// Commands maps command names to their definitions.
	// Closer (more specific) configs override farther ones for the same name.
	Commands map[string]Command `yaml:"commands,omitempty"`

	// Services maps service names to their definitions.
	// Each service auto-generates commands by prepending Exec to each command suffix.
	// Closer (more specific) configs override farther ones for the same service name.
	Services map[string]Service `yaml:"services,omitempty"`
}

// MergedConfig is the result of merging multiple Config layers.
// It holds the fully resolved projects, commands, and services for a given context.
type MergedConfig struct {
	Projects map[string]Project
	Commands map[string]Command
	Services map[string]Service
}

// NewMergedConfig creates an empty MergedConfig with initialized maps.
func NewMergedConfig() *MergedConfig {
	return &MergedConfig{
		Projects: make(map[string]Project),
		Commands: make(map[string]Command),
		Services: make(map[string]Service),
	}
}
