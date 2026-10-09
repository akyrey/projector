package config

import (
	"errors"
	"fmt"
	"sort"
)

// ErrInvalidOnceStep is returned when a command's once steps are malformed.
var ErrInvalidOnceStep = errors.New("invalid once step")

// validateOnceSteps checks every command's once steps: each needs a non-empty,
// unique ID and a command to run.
func validateOnceSteps(commands map[string]Command) error {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic error for multiple invalid commands

	for _, name := range names {
		seen := make(map[string]struct{}, len(commands[name].Once))
		for i, step := range commands[name].Once {
			switch {
			case step.ID == "":
				return fmt.Errorf("%w: command %q step #%d has no id", ErrInvalidOnceStep, name, i+1)
			case step.Cmd.IsEmpty():
				return fmt.Errorf("%w: command %q step %q has no cmd", ErrInvalidOnceStep, name, step.ID)
			}
			if _, dup := seen[step.ID]; dup {
				return fmt.Errorf("%w: command %q has duplicate step id %q", ErrInvalidOnceStep, name, step.ID)
			}
			seen[step.ID] = struct{}{}
		}
	}
	return nil
}

// PendingOnceSteps returns the steps not yet applied, in declaration order.
func PendingOnceSteps(steps []OnceStep, applied func(id string) bool) []OnceStep {
	var pending []OnceStep
	for _, step := range steps {
		if !applied(step.ID) {
			pending = append(pending, step)
		}
	}
	return pending
}
