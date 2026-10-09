package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/akyrey/projector/internal/app"
	"github.com/akyrey/projector/internal/runner"
	"github.com/akyrey/projector/internal/runs"
)

// maxRequestBody bounds JSON request bodies.
const maxRequestBody = 64 << 10

type startRunRequest struct {
	Project string   `json:"project"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func (s *Server) listRuns(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.runs.List())
}

func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	var req startRunRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if req.Project == "" || req.Command == "" {
		writeError(w, http.StatusBadRequest, "project and command are required")
		return
	}

	// Validate up front so typos fail the request instead of starting a run.
	proj, ok := s.lookupProject(w, req.Project)
	if !ok {
		return
	}
	merged, err := s.app.Loader().LoadForProject(proj.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("load config: %v", err))
		return
	}
	if _, ok := merged.Commands[req.Command]; !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("command %q not found for project %q", req.Command, req.Project))
		return
	}

	args := nonNil(req.Args)
	appReq := app.RunRequest{Command: req.Command, Projects: []string{req.Project}, ExtraArgs: shellQuoteAll(args)}
	run, err := s.runs.Start(runs.Spec{Project: req.Project, Command: req.Command, Args: args},
		func(ctx context.Context, out io.Writer) error {
			// No stdin: commands run detached from any terminal, in their own
			// process group so Stop reaches the whole tree.
			return s.app.Run(ctx, runner.New(nil, out, out, runner.WithProcessGroup()), appReq)
		})
	switch {
	case errors.Is(err, runs.ErrTooManyRunning):
		writeError(w, http.StatusTooManyRequests, "too many runs in progress; stop one first")
		return
	case errors.Is(err, runs.ErrClosed):
		writeError(w, http.StatusServiceUnavailable, "server is shutting down")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, run.Info())
}

func (s *Server) stopRun(w http.ResponseWriter, r *http.Request) {
	run, ok := s.runs.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	run.Stop()
	writeJSON(w, http.StatusAccepted, run.Info())
}

// streamRun sends a run's output as server-sent events:
//
//	reset   data: JSON string with the whole buffered output; sent first,
//	        after lagging behind, and just before end
//	output  data: JSON string chunk to append
//	end     data: the run's final Info
//
// Chunks are split on UTF-8 rune boundaries so multibyte characters written
// across two writes are not garbled.
func (s *Server) streamRun(w http.ResponseWriter, r *http.Request) {
	run, ok := s.runs.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	rc := http.NewResponseController(w)

	snapshot, ch, unsubscribe := run.Subscribe()
	// A closure: unsubscribe is replaced when resubscribing below.
	defer func() { unsubscribe() }()
	text, pending := splitUTF8(snapshot)
	if err := sendEvent(w, rc, "reset", text); err != nil {
		return
	}

	for {
		select {
		case chunk, open := <-ch:
			if open {
				text, pending = splitUTF8(append(pending, chunk...))
				if text == "" {
					continue
				}
				if err := sendEvent(w, rc, "output", text); err != nil {
					return
				}
				continue
			}
			// Closed: the run finished, or we lagged behind and were dropped.
			// Either way a fresh snapshot makes the client's copy complete.
			unsubscribe()
			snapshot, ch, unsubscribe = run.Subscribe()
			if run.Info().Status != runs.Running {
				_ = sendEvent(w, rc, "reset", string(snapshot))
				_ = sendEvent(w, rc, "end", run.Info())
				return
			}
			text, pending = splitUTF8(snapshot)
			if err := sendEvent(w, rc, "reset", text); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		case <-s.closing:
			return
		}
	}
}

func sendEvent(w io.Writer, rc *http.ResponseController, name string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, payload); err != nil {
		return err
	}
	if err := rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

// splitUTF8 returns b up to its last complete UTF-8 rune, as a string, and the
// incomplete trailing bytes to prepend to the next chunk.
func splitUTF8(b []byte) (string, []byte) {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if !utf8.RuneStart(b[i]) {
			continue
		}
		if utf8.FullRune(b[i:]) {
			return string(b), nil
		}
		return string(b[:i]), bytes.Clone(b[i:])
	}
	return string(b), nil
}

// shellQuoteAll single-quotes each arg so the shell receives it as one literal
// word: the dashboard only runs configured commands, never shell syntax.
func shellQuoteAll(args []string) []string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return quoted
}
