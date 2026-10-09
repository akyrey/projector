package runs_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akyrey/projector/internal/runs"
)

const waitTimeout = 5 * time.Second

func start(t *testing.T, m *runs.Manager, spec runs.Spec, fn runs.Func) *runs.Run {
	t.Helper()
	r, err := m.Start(spec, fn)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return r
}

func waitDone(t *testing.T, r *runs.Run) {
	t.Helper()
	select {
	case <-r.Done():
	case <-time.After(waitTimeout):
		t.Fatal("run did not finish")
	}
}

// drain collects chunks until ch is closed.
func drain(t *testing.T, ch <-chan []byte) string {
	t.Helper()
	var b strings.Builder
	timeout := time.After(waitTimeout)
	for {
		select {
		case chunk, ok := <-ch:
			if !ok {
				return b.String()
			}
			b.Write(chunk)
		case <-timeout:
			t.Fatal("subscription not closed")
		}
	}
}

func TestRun_StatusAndOutput(t *testing.T) {
	tests := []struct {
		name       string
		fn         runs.Func
		wantStatus runs.Status
		wantErr    string
	}{
		{
			name: "success",
			fn: func(_ context.Context, w io.Writer) error {
				_, err := io.WriteString(w, "hello\n")
				return err
			},
			wantStatus: runs.Succeeded,
		},
		{
			name: "failure",
			fn: func(_ context.Context, w io.Writer) error {
				_, _ = io.WriteString(w, "hello\n")
				return errors.New("exit status 1")
			},
			wantStatus: runs.Failed,
			wantErr:    "exit status 1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := runs.NewManager()
			r := start(t, m, runs.Spec{Project: "api", Command: "test"}, tt.fn)
			waitDone(t, r)

			info := r.Info()
			if info.Status != tt.wantStatus || info.Error != tt.wantErr {
				t.Errorf("info = %+v, want status %s err %q", info, tt.wantStatus, tt.wantErr)
			}
			if info.Project != "api" || info.Command != "test" || info.ID == "" {
				t.Errorf("info = %+v, want project/command/id set", info)
			}
			if info.EndedAt.IsZero() || info.EndedAt.Before(info.StartedAt) {
				t.Errorf("EndedAt %v not after StartedAt %v", info.EndedAt, info.StartedAt)
			}
			snapshot, ch, _ := r.Subscribe()
			if string(snapshot) != "hello\n" {
				t.Errorf("snapshot = %q, want hello", snapshot)
			}
			if rest := drain(t, ch); rest != "" {
				t.Errorf("finished run streamed %q", rest)
			}
		})
	}
}

func TestRun_SubscribeGetsSnapshotThenLiveOutput(t *testing.T) {
	m := runs.NewManager()
	release := make(chan struct{})
	r := start(t, m, runs.Spec{Command: "logs"}, func(_ context.Context, w io.Writer) error {
		_, _ = io.WriteString(w, "before\n")
		<-release
		_, _ = io.WriteString(w, "after\n")
		return nil
	})

	// Wait until "before" is buffered.
	var snapshot []byte
	var ch <-chan []byte
	deadline := time.Now().Add(waitTimeout)
	for {
		var unsubscribe func()
		snapshot, ch, unsubscribe = r.Subscribe()
		if len(snapshot) > 0 || time.Now().After(deadline) {
			break
		}
		unsubscribe()
		time.Sleep(5 * time.Millisecond)
	}
	close(release)

	if string(snapshot) != "before\n" {
		t.Errorf("snapshot = %q, want before", snapshot)
	}
	if live := drain(t, ch); live != "after\n" {
		t.Errorf("live = %q, want after", live)
	}
}

func TestRun_StopCancelsContext(t *testing.T) {
	m := runs.NewManager()
	r := start(t, m, runs.Spec{Command: "logs"}, func(ctx context.Context, _ io.Writer) error {
		<-ctx.Done()
		return ctx.Err()
	})
	r.Stop()
	waitDone(t, r)

	if got := r.Info().Status; got != runs.Cancelled {
		t.Errorf("status = %s, want %s", got, runs.Cancelled)
	}
}

func TestRun_OutputKeepsTailWithinLimit(t *testing.T) {
	m := runs.NewManager(runs.WithMaxOutput(20))
	r := start(t, m, runs.Spec{}, func(_ context.Context, w io.Writer) error {
		for i := 0; i < 10; i++ {
			if _, err := fmt.Fprintf(w, "line %d\n", i); err != nil {
				return err
			}
		}
		return nil
	})
	waitDone(t, r)

	snapshot, _, _ := r.Subscribe()
	if len(snapshot) > 20 {
		t.Errorf("snapshot is %d bytes, want <= 20: %q", len(snapshot), snapshot)
	}
	if !strings.HasSuffix(string(snapshot), "line 9\n") || !strings.HasPrefix(string(snapshot), "line") {
		t.Errorf("snapshot = %q, want whole trailing lines", snapshot)
	}
}

func TestRun_SlowSubscriberDoesNotBlockWriter(t *testing.T) {
	m := runs.NewManager()
	r := start(t, m, runs.Spec{}, func(_ context.Context, w io.Writer) error {
		for i := 0; i < 10_000; i++ {
			if _, err := io.WriteString(w, "x\n"); err != nil {
				return err
			}
		}
		return nil
	})
	_, ch, _ := r.Subscribe() // never read until the run ends
	waitDone(t, r)
	drain(t, ch) // dropped subscribers are closed, not left hanging
}

func TestManager_ListNewestFirstAndEvictsFinished(t *testing.T) {
	m := runs.NewManager(runs.WithMaxRuns(2))
	block := make(chan struct{})
	defer close(block)

	running := start(t, m, runs.Spec{Command: "running"}, func(context.Context, io.Writer) error {
		<-block
		return nil
	})
	for _, name := range []string{"a", "b", "c"} {
		waitDone(t, start(t, m, runs.Spec{Command: name}, func(context.Context, io.Writer) error { return nil }))
	}

	var got []string
	for _, info := range m.List() {
		got = append(got, info.Command)
	}
	// "a" is evicted; the running run is kept even though it is the oldest.
	if strings.Join(got, ",") != "c,b,running" {
		t.Errorf("List = %v, want c,b,running", got)
	}
	if _, ok := m.Get(running.Info().ID); !ok {
		t.Error("running run not found by id")
	}
	if _, ok := m.Get("nope"); ok {
		t.Error("Get(nope) found a run")
	}
}

func TestManager_ShutdownStopsRunsAndWaits(t *testing.T) {
	m := runs.NewManager()
	var stopped sync.WaitGroup
	for i := 0; i < 3; i++ {
		stopped.Add(1)
		start(t, m, runs.Spec{}, func(ctx context.Context, _ io.Writer) error {
			defer stopped.Done()
			<-ctx.Done()
			return ctx.Err()
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	if err := m.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	stopped.Wait()
}

func TestManager_StartLimits(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	wait := func(context.Context, io.Writer) error { <-block; return nil }

	m := runs.NewManager(runs.WithMaxRunning(1))
	if _, err := m.Start(runs.Spec{}, wait); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if _, err := m.Start(runs.Spec{}, wait); !errors.Is(err, runs.ErrTooManyRunning) {
		t.Errorf("second Start err = %v, want ErrTooManyRunning", err)
	}
}

func TestManager_StartAfterShutdown(t *testing.T) {
	m := runs.NewManager()
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(runs.Spec{}, func(context.Context, io.Writer) error { return nil }); !errors.Is(err, runs.ErrClosed) {
		t.Errorf("Start err = %v, want ErrClosed", err)
	}
}
