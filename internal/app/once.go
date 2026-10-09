package app

import (
	"fmt"
	"sync"
	"time"

	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/runner"
	"github.com/akyrey/projector/internal/state"
)

// lazyStore creates the state store on first use, so commands without once
// steps never touch the state directory. Safe for concurrent use.
type lazyStore struct {
	once  sync.Once
	store *state.Store
	err   error
}

func (l *lazyStore) get() (*state.Store, error) {
	l.once.Do(func() { l.store, l.err = state.NewStore() })
	return l.store, l.err
}

// onceKey returns the state key for cmd run from dir: the project root
// (symlinks resolved) and the canonical command name, so subdirectories,
// symlinked paths and aliases share the same record.
func (a *App) onceKey(dir, cmdName string, cmd config.Command) (root, name string, err error) {
	root, err = a.loader.ProjectRoot(dir)
	if err != nil {
		return "", "", fmt.Errorf("resolve project root: %w", err)
	}
	if cmd.AliasOf != "" {
		cmdName = cmd.AliasOf
	}
	return root, cmdName, nil
}

// PendingOnceSteps returns cmd's once steps not yet applied in dir.
func (a *App) PendingOnceSteps(dir, cmdName string, cmd config.Command) ([]config.OnceStep, error) {
	if len(cmd.Once) == 0 {
		return nil, nil
	}
	store, err := a.state.get()
	if err != nil {
		return nil, fmt.Errorf("open state: %w", err)
	}
	root, name, err := a.onceKey(dir, cmdName, cmd)
	if err != nil {
		return nil, err
	}
	applied, err := store.Applied(root, name)
	if err != nil {
		return nil, fmt.Errorf("read applied once steps: %w", err)
	}
	return config.PendingOnceSteps(cmd.Once, func(id string) bool {
		_, ok := applied[id]
		return ok
	}), nil
}

// onceFollowUps turns cmd's pending once steps in dir into runner follow-ups
// that record themselves as applied when they succeed. The steps inherit the
// command's env.
func (a *App) onceFollowUps(dir, cmdName string, cmd config.Command) ([]runner.FollowUp, error) {
	pending, err := a.PendingOnceSteps(dir, cmdName, cmd)
	if err != nil || len(pending) == 0 {
		return nil, err
	}
	store, err := a.state.get()
	if err != nil {
		return nil, fmt.Errorf("open state: %w", err)
	}
	root, name, err := a.onceKey(dir, cmdName, cmd)
	if err != nil {
		return nil, err
	}

	followUps := make([]runner.FollowUp, len(pending))
	for i, step := range pending {
		label := fmt.Sprintf("%s once: %s", name, step.ID)
		if step.Description != "" {
			label += " (" + step.Description + ")"
		}
		followUps[i] = runner.FollowUp{
			Name:    label,
			Command: config.Command{Cmd: step.Cmd, Env: cmd.Env},
			OnSuccess: func() error {
				return store.MarkApplied(root, name, step.ID, time.Now())
			},
		}
	}
	return followUps, nil
}
