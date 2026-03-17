// Package editor opens a file in the user's preferred $EDITOR.
package editor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// ErrNoEditor is returned when no editor can be determined.
var ErrNoEditor = errors.New("no editor found; set $EDITOR or $VISUAL")

// Open opens path in the user's preferred editor, blocking until the editor exits.
// It checks $VISUAL first, then $EDITOR, then falls back to common editors.
func Open(path string) error {
	editor, err := resolve()
	if err != nil {
		return err
	}

	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor %q exited with error: %w", editor, err)
	}

	return nil
}

// resolve returns the editor binary to use, checking environment variables first
// and falling back to a list of commonly installed editors.
func resolve() (string, error) {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if v := os.Getenv(env); v != "" {
			return v, nil
		}
	}

	// Fallback candidates in preference order.
	candidates := []string{"vim", "nano", "vi", "notepad"}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}

	return "", ErrNoEditor
}
