package config

// Command represents a runnable command definition.
// It stores the shell command string along with optional metadata like
// environment variables, a description, and dependencies.
type Command struct {
	// Cmd is the shell command string to execute.
	Cmd string `yaml:"cmd"`

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
