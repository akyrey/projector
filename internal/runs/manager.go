// Package runs tracks background command runs for the web UI: their status,
// a bounded tail of their output, and live subscribers to that output.
package runs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

// Status is the lifecycle state of a run.
type Status string

// Run states.
const (
	Running   Status = "running"
	Succeeded Status = "succeeded"
	Failed    Status = "failed"
	Cancelled Status = "cancelled"
)

// Start errors.
var (
	ErrClosed         = errors.New("run manager is shut down")
	ErrTooManyRunning = errors.New("too many runs in progress")
)

const (
	defaultMaxOutput  = 2 << 20 // bytes of output kept per run
	defaultMaxRuns    = 50      // finished runs kept in memory
	defaultMaxRunning = 16      // runs in progress at once

	// subscriberBuffer is how many chunks a subscriber may lag behind before
	// it is dropped, so a slow client never blocks the command's output.
	subscriberBuffer = 1024
)

// Func is the work of a run. It must return when ctx is cancelled and write
// all output to w.
type Func func(ctx context.Context, w io.Writer) error

// Spec describes what a run executes, for display.
type Spec struct {
	Project string   `json:"project"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// Info is a snapshot of a run's state.
type Info struct {
	Spec
	ID        string    `json:"id"`
	Status    Status    `json:"status"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt,omitzero"`
}

// Option configures a Manager.
type Option func(*Manager)

// WithMaxOutput sets how many bytes of output are kept per run.
func WithMaxOutput(n int) Option { return func(m *Manager) { m.maxOutput = n } }

// WithMaxRuns sets how many finished runs are kept; running ones are never evicted.
func WithMaxRuns(n int) Option { return func(m *Manager) { m.maxRuns = n } }

// WithMaxRunning sets how many runs may be in progress at once.
func WithMaxRunning(n int) Option { return func(m *Manager) { m.maxRunning = n } }

// Manager starts runs and keeps recent ones. It is safe for concurrent use.
type Manager struct {
	maxOutput  int
	maxRuns    int
	maxRunning int

	mu     sync.Mutex
	closed bool
	nextID int
	runs   []*Run // oldest first
}

// NewManager creates an empty Manager.
func NewManager(opts ...Option) *Manager {
	m := &Manager{maxOutput: defaultMaxOutput, maxRuns: defaultMaxRuns, maxRunning: defaultMaxRunning}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Start runs fn in a new goroutine and returns its Run. It fails with
// ErrClosed after Shutdown and ErrTooManyRunning at the running limit.
func (m *Manager) Start(spec Spec, fn Func) (*Run, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	running := 0
	for _, r := range m.runs {
		if !r.finished() {
			running++
		}
	}
	if running >= m.maxRunning {
		m.mu.Unlock()
		return nil, ErrTooManyRunning
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.nextID++
	id := strconv.Itoa(m.nextID)
	r := &Run{
		id:        id,
		info:      Info{Spec: spec, ID: id, Status: Running, StartedAt: time.Now()},
		maxOutput: m.maxOutput,
		cancel:    cancel,
		done:      make(chan struct{}),
		subs:      make(map[chan []byte]struct{}),
	}
	m.runs = append(m.runs, r)
	m.mu.Unlock()

	go func() {
		defer cancel()
		err := fn(ctx, r)
		r.finish(err, ctx.Err() != nil)
		m.evict()
		close(r.done) // last, so waiters observe the final state and list
	}()
	return r, nil
}

// Get returns the run with the given id.
func (m *Manager) Get(id string) (*Run, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.runs {
		if r.id == id {
			return r, true
		}
	}
	return nil, false
}

// List returns all kept runs, newest first.
func (m *Manager) List() []Info {
	m.mu.Lock()
	runs := slices.Clone(m.runs)
	m.mu.Unlock()

	infos := make([]Info, len(runs))
	for i, r := range runs {
		infos[len(runs)-1-i] = r.Info()
	}
	return infos
}

// Shutdown stops every run and waits for them to finish or for ctx to end.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	runs := slices.Clone(m.runs)
	m.mu.Unlock()

	for _, r := range runs {
		r.Stop()
	}
	for _, r := range runs {
		select {
		case <-r.Done():
		case <-ctx.Done():
			return errors.Join(errors.New("runs still active"), ctx.Err())
		}
	}
	return nil
}

// evict drops the oldest finished runs beyond maxRuns.
func (m *Manager) evict() {
	m.mu.Lock()
	defer m.mu.Unlock()

	finished := 0
	for _, r := range m.runs {
		if r.finished() {
			finished++
		}
	}
	excess := finished - m.maxRuns
	if excess <= 0 {
		return
	}
	m.runs = slices.DeleteFunc(m.runs, func(r *Run) bool {
		if excess > 0 && r.finished() {
			excess--
			return true
		}
		return false
	})
}

// Run is one execution. Its methods are safe for concurrent use.
type Run struct {
	id        string // immutable copy of info.ID, readable without mu
	maxOutput int
	cancel    context.CancelFunc
	done      chan struct{}

	mu     sync.Mutex
	info   Info
	output []byte
	subs   map[chan []byte]struct{}
}

// Info returns a snapshot of the run's state.
func (r *Run) Info() Info {
	r.mu.Lock()
	defer r.mu.Unlock()
	info := r.info
	info.Args = slices.Clone(info.Args)
	return info
}

// Done is closed when the run has finished.
func (r *Run) Done() <-chan struct{} { return r.done }

// Stop cancels the run's context. It does not wait for the run to finish.
func (r *Run) Stop() { r.cancel() }

// Subscribe returns the output so far and a channel of later output. The
// channel is closed when the run finishes, when the subscriber falls too far
// behind, or after unsubscribe is called.
func (r *Run) Subscribe() (snapshot []byte, ch <-chan []byte, unsubscribe func()) {
	r.mu.Lock()
	defer r.mu.Unlock()

	c := make(chan []byte, subscriberBuffer)
	snapshot = bytes.Clone(trimToTail(r.output, r.maxOutput))
	if r.info.Status != Running {
		close(c)
		return snapshot, c, func() {}
	}
	r.subs[c] = struct{}{}
	return snapshot, c, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.dropLocked(c)
	}
}

// Write appends p to the output and forwards it to subscribers.
func (r *Run) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.output = append(r.output, p...)
	if len(r.output) > 2*r.maxOutput {
		// Trim in batches: trimming on every write would copy the whole
		// buffer per write once it is full.
		r.output = trimToTail(r.output, r.maxOutput)
	}
	if len(r.subs) == 0 {
		return len(p), nil
	}
	chunk := bytes.Clone(p)
	for c := range r.subs {
		select {
		case c <- chunk:
		default:
			r.dropLocked(c) // too slow: it can resubscribe for a fresh snapshot
		}
	}
	return len(p), nil
}

// finish records the outcome and closes all subscriptions.
func (r *Run) finish(err error, cancelled bool) {
	r.mu.Lock()
	r.info.EndedAt = time.Now()
	switch {
	case err == nil:
		r.info.Status = Succeeded
	case cancelled:
		r.info.Status = Cancelled
	default:
		r.info.Status = Failed
		r.info.Error = err.Error()
	}
	for c := range r.subs {
		r.dropLocked(c)
	}
	r.mu.Unlock()
}

func (r *Run) finished() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.info.Status != Running
}

func (r *Run) dropLocked(c chan []byte) {
	if _, ok := r.subs[c]; ok {
		delete(r.subs, c)
		close(c)
	}
}

// trimToTail keeps at most limit trailing bytes of buf, starting at a line
// boundary when one is available, or else at a UTF-8 rune boundary.
func trimToTail(buf []byte, limit int) []byte {
	if len(buf) <= limit {
		return buf
	}
	tail := buf[len(buf)-limit:]
	if i := bytes.IndexByte(tail, '\n'); i >= 0 && i < len(tail)-1 {
		tail = tail[i+1:]
	} else {
		for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
			tail = tail[1:]
		}
	}
	return slices.Clone(tail)
}
