// Package web serves the projector dashboard: a JSON API over the app layer,
// live run output as server-sent events, and the embedded frontend.
//
// The server is meant for the local machine only: it rejects foreign Host
// headers and requires a per-start token as a bearer header on the API (see
// guard for why it is never a cookie).
package web

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/akyrey/projector/internal/app"
	"github.com/akyrey/projector/internal/runs"
	"github.com/akyrey/projector/internal/status"
)

//go:embed static
var embedded embed.FS

// infoTimeout bounds each info item of a project.
const infoTimeout = 30 * time.Second

// ContainerLister lists compose containers by canonical project dir.
type ContainerLister func(ctx context.Context) (map[string]status.Containers, error)

// Option configures a Server.
type Option func(*Server)

// WithContainerLister replaces the docker lookup (status.ListContainers).
func WithContainerLister(l ContainerLister) Option {
	return func(s *Server) { s.listContainers = l }
}

// WithMaxRunning sets how many runs may be in progress at once.
func WithMaxRunning(n int) Option {
	return func(s *Server) { s.runOpts = append(s.runOpts, runs.WithMaxRunning(n)) }
}

// Server is the dashboard's http.Handler.
type Server struct {
	app            *app.App
	runs           *runs.Manager
	token          string
	listContainers ContainerLister
	runOpts        []runs.Option
	handler        http.Handler

	closeOnce sync.Once
	closing   chan struct{} // closed by Shutdown to end open streams
}

// New creates a Server for a, accepting token as its credential.
func New(a *app.App, token string, opts ...Option) *Server {
	s := &Server{
		app:            a,
		token:          token,
		listContainers: status.ListContainers,
		closing:        make(chan struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.runs = runs.NewManager(s.runOpts...)
	s.handler = s.guard(s.routes())
	return s
}

func (s *Server) routes() http.Handler {
	static, err := fs.Sub(embedded, "static")
	if err != nil {
		panic(err) // the embedded tree is fixed at build time
	}

	mux := http.NewServeMux()
	mux.Handle("GET /{$}", serveFile(static, "index.html"))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))

	mux.HandleFunc("GET /api/projects", s.listProjects)
	mux.HandleFunc("GET /api/projects/{name}", s.projectDetail)
	mux.HandleFunc("GET /api/projects/{name}/info", s.projectInfo)
	mux.HandleFunc("GET /api/graph", s.graph)

	mux.HandleFunc("GET /api/runs", s.listRuns)
	mux.HandleFunc("POST /api/runs", s.startRun)
	mux.HandleFunc("POST /api/runs/{id}/stop", s.stopRun)
	mux.HandleFunc("GET /api/runs/{id}/stream", s.streamRun)
	return mux
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// Shutdown ends open output streams, stops all runs and waits for them to
// finish or for ctx to end.
func (s *Server) Shutdown(ctx context.Context) error {
	s.closeOnce.Do(func() { close(s.closing) })
	return s.runs.Shutdown(ctx)
}

func serveFile(fsys fs.FS, name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFileFS(w, r, fsys, name)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
