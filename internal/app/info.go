package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/runner"
)

const (
	// maxInfoOutput bounds the output kept per info item; the command is
	// stopped once it is exceeded.
	maxInfoOutput = 256 << 10

	// maxInfoConcurrency bounds info commands running at once, across calls.
	maxInfoConcurrency = 8
)

// InfoResult is the outcome of one info item.
type InfoResult struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Output      string `json:"output"`
	Error       string `json:"error,omitempty"`
}

// Info runs the info items resolved for dir concurrently, each bounded by
// timeout, and returns their results in declaration order. A failing item is
// reported in its result, not as an error.
func (a *App) Info(ctx context.Context, dir string, timeout time.Duration) ([]InfoResult, error) {
	merged, err := a.loader.LoadForProject(dir)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	results := make([]InfoResult, len(merged.Info))
	var wg sync.WaitGroup
	for i, item := range merged.Info {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case a.infoSlots <- struct{}{}:
				defer func() { <-a.infoSlots }()
			case <-ctx.Done():
				results[i] = InfoResult{Name: item.Name, Description: item.Description, Error: ctx.Err().Error()}
				return
			}
			results[i] = runInfoItem(ctx, dir, item, timeout)
		}()
	}
	wg.Wait()
	return results, nil
}

func runInfoItem(ctx context.Context, dir string, item config.InfoItem, timeout time.Duration) InfoResult {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	out := &cappedWriter{limit: maxInfoOutput, onFull: cancel}
	r := runner.New(nil, out, out, runner.WithProcessGroup())
	err := r.Run(ctx, runner.Target{Name: item.Name, Dir: dir, Command: config.Command{Cmd: item.Cmd}})

	res := InfoResult{Name: item.Name, Description: item.Description, Output: out.String()}
	switch {
	case out.full():
		// Stopped on purpose; the note in the output explains it.
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.Error = fmt.Sprintf("timed out after %s", timeout)
	case err != nil:
		res.Error = err.Error()
	}
	return res
}

// cappedWriter keeps the first limit bytes written and calls onFull once when
// more arrive. Safe for concurrent use.
type cappedWriter struct {
	limit  int
	onFull func()

	mu        sync.Mutex
	buf       bytes.Buffer
	truncated bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.truncated {
		return len(p), nil
	}
	if room := w.limit - w.buf.Len(); len(p) > room {
		w.buf.Write(p[:room])
		w.truncated = true
		w.onFull()
		return len(p), nil
	}
	w.buf.Write(p)
	return len(p), nil
}

func (w *cappedWriter) full() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.truncated
}

func (w *cappedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.truncated {
		return w.buf.String() + fmt.Sprintf("\n[output truncated at %d KiB]\n", w.limit>>10)
	}
	return w.buf.String()
}
