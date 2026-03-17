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
}

// MergedConfig is the result of merging multiple Config layers.
// It holds the fully resolved projects and commands for a given context.
type MergedConfig struct {
	Projects map[string]Project
	Commands map[string]Command
}

// NewMergedConfig creates an empty MergedConfig with initialized maps.
func NewMergedConfig() *MergedConfig {
	return &MergedConfig{
		Projects: make(map[string]Project),
		Commands: make(map[string]Command),
	}
}
