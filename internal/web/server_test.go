package web_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akyrey/projector/internal/app"
	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/project"
	"github.com/akyrey/projector/internal/runs"
	"github.com/akyrey/projector/internal/status"
	"github.com/akyrey/projector/internal/web"
)

const token = "s3cret"

type env struct {
	t      *testing.T
	srv    *httptest.Server
	loader *config.Loader
}

// newEnv starts a server whose docker lister reports 1/2 containers for every
// registered project dir (or fails when dockerErr is set).
func newEnv(t *testing.T, dockerErr error) *env {
	t.Helper()
	return newEnvWith(t, dockerErr)
}

func newEnvWith(t *testing.T, dockerErr error, opts ...web.Option) *env {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	loader := config.NewLoaderWithGlobal(filepath.Join(t.TempDir(), "global.yaml"))
	a := app.New(loader)

	lister := func(context.Context) (map[string]status.Containers, error) {
		if dockerErr != nil {
			return nil, dockerErr
		}
		all, err := a.Registry().List()
		if err != nil {
			return nil, err
		}
		out := make(map[string]status.Containers)
		for _, p := range all {
			dir, err := filepath.EvalSymlinks(p.Path)
			if err != nil {
				return nil, err
			}
			out[dir] = status.Containers{Running: 1, Total: 2}
		}
		return out, nil
	}

	s := web.New(a, token, append([]web.Option{web.WithContainerLister(lister)}, opts...)...)
	srv := httptest.NewServer(s)
	t.Cleanup(func() {
		srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return &env{t: t, srv: srv, loader: loader}
}

func (e *env) addProject(name string, cfg *config.Config) string {
	e.t.Helper()
	dir := e.t.TempDir()
	if err := config.SaveFile(filepath.Join(dir, config.LocalConfigName), cfg); err != nil {
		e.t.Fatal(err)
	}
	if err := project.NewRegistry(e.loader).Add(name, dir); err != nil {
		e.t.Fatal(err)
	}
	return dir
}

// do sends an authenticated request (bearer token) and returns the response.
func (e *env) do(method, path string, body string) *http.Response {
	e.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, r)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func decode[T any](t *testing.T, resp *http.Response, wantStatus int) T {
	t.Helper()
	var v T
	if resp.StatusCode != wantStatus {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d: %s", resp.StatusCode, wantStatus, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

func cmd(c string) config.Command { return config.Command{Cmd: config.NewStringOrList(c)} }

func TestAuth(t *testing.T) {
	e := newEnv(t, nil)

	get := func(t *testing.T, path string, header map[string]string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, e.srv.URL+path, nil)
		for k, v := range header {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}

	for _, tt := range []struct {
		name   string
		path   string
		header map[string]string
		want   int
	}{
		{"page needs no token", "/", nil, http.StatusOK},
		{"assets need no token", "/static/app.js", nil, http.StatusOK},
		{"api without token", "/api/projects", nil, http.StatusUnauthorized},
		{"api with wrong token", "/api/projects", map[string]string{"Authorization": "Bearer nope"}, http.StatusUnauthorized},
		{"api with token", "/api/projects", map[string]string{"Authorization": "Bearer " + token}, http.StatusOK},
		// Cookies are sent to every localhost port, so they are never a credential.
		{"api ignores cookies", "/api/projects", map[string]string{"Cookie": "projector_token=" + token}, http.StatusUnauthorized},
		{"token in query is ignored", "/api/projects?token=" + token, nil, http.StatusUnauthorized},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resp := get(t, tt.path, tt.header)
			if resp.StatusCode != tt.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.want)
			}
			if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
				t.Errorf("CSP = %q, want a restrictive policy on every response", csp)
			}
			if len(resp.Cookies()) != 0 {
				t.Errorf("cookies set: %v", resp.Cookies())
			}
		})
	}
}

func TestStartRunRejectsUnknownFields(t *testing.T) {
	e := newEnv(t, nil)
	e.addProject("api", &config.Config{Commands: map[string]config.Command{"hi": cmd("echo hi")}})
	if resp := e.do(http.MethodPost, "/api/runs", `{"project":"api","command":"hi","shell":"rm -rf /"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRunArgsAreQuoted(t *testing.T) {
	e := newEnv(t, nil)
	dir := e.addProject("api", &config.Config{Commands: map[string]config.Command{"say": cmd("printf '%s|'")}})

	body := `{"project":"api","command":"say","args":["a b","; touch pwned","$(touch pwned2)","it's"]}`
	info := decode[runs.Info](t, e.do(http.MethodPost, "/api/runs", body), http.StatusCreated)
	events := e.stream(info.ID)

	if got, want := outputOf(t, events), "a b|; touch pwned|$(touch pwned2)|it's|"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
	for _, f := range []string{"pwned", "pwned2"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("%s was created: args reached the shell unquoted", f)
		}
	}
}

func TestStreamKeepsMultibyteRunesWhole(t *testing.T) {
	e := newEnv(t, nil)
	// "é" is \303\251; the two bytes are written separately.
	e.addProject("api", &config.Config{Commands: map[string]config.Command{
		"accent": cmd(`printf '\303'; sleep 0.3; printf '\251\n'`),
	}})

	info := decode[runs.Info](t, e.do(http.MethodPost, "/api/runs", `{"project":"api","command":"accent"}`), http.StatusCreated)
	events := e.stream(info.ID)
	for _, ev := range events {
		if strings.Contains(ev.data, `�`) {
			t.Errorf("event %s carries a replacement char: %s", ev.name, ev.data)
		}
	}
	if got := outputOf(t, events); got != "é\n" {
		t.Errorf("output = %q, want %q", got, "é\n")
	}
}

func TestTooManyRunningRuns(t *testing.T) {
	e := newEnvWith(t, nil, web.WithMaxRunning(1))
	e.addProject("api", &config.Config{Commands: map[string]config.Command{"logs": cmd("sleep 30")}})

	first := decode[runs.Info](t, e.do(http.MethodPost, "/api/runs", `{"project":"api","command":"logs"}`), http.StatusCreated)
	if resp := e.do(http.MethodPost, "/api/runs", `{"project":"api","command":"logs"}`); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("second run status = %d, want 429", resp.StatusCode)
	}
	e.do(http.MethodPost, "/api/runs/"+first.ID+"/stop", "")
	e.stream(first.ID)
}

type projectJSON struct {
	Name       string                `json:"name"`
	Dir        string                `json:"dir"`
	Git        struct{ IsRepo bool } `json:"git"`
	Containers *struct {
		Running int `json:"running"`
		Total   int `json:"total"`
	} `json:"containers"`
	Pending int `json:"pending"`
}

func TestListProjects(t *testing.T) {
	for _, tt := range []struct {
		name           string
		dockerErr      error
		wantContainers bool
	}{
		{"docker available", nil, true},
		{"docker unavailable", errors.New("no daemon"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, tt.dockerErr)
			e.addProject("web", &config.Config{})
			e.addProject("api", &config.Config{Commands: map[string]config.Command{
				"update": {Cmd: config.NewStringOrList("true"), Once: []config.OnceStep{{ID: "s1", Cmd: config.NewStringOrList("true")}}},
			}})

			got := decode[[]projectJSON](t, e.do(http.MethodGet, "/api/projects", ""), http.StatusOK)

			if len(got) != 2 || got[0].Name != "api" || got[1].Name != "web" {
				t.Fatalf("projects = %+v, want api, web", got)
			}
			if got[0].Pending != 1 || got[0].Git.IsRepo {
				t.Errorf("api = %+v, want 1 pending and not a repo", got[0])
			}
			if tt.wantContainers != (got[0].Containers != nil) {
				t.Fatalf("containers = %+v, want present=%v", got[0].Containers, tt.wantContainers)
			}
			if tt.wantContainers && (got[0].Containers.Running != 1 || got[0].Containers.Total != 2) {
				t.Errorf("containers = %+v, want 1/2", got[0].Containers)
			}
		})
	}
}

type detailJSON struct {
	Name     string `json:"name"`
	Commands []struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Cmd         string   `json:"cmd"`
		Aliases     []string `json:"aliases"`
		DependsOn   []string `json:"dependsOn"`
		Once        []struct {
			ID      string `json:"id"`
			Pending bool   `json:"pending"`
		} `json:"once"`
	} `json:"commands"`
}

func TestProjectDetail(t *testing.T) {
	e := newEnv(t, nil)
	e.addProject("api", &config.Config{Commands: map[string]config.Command{
		"start": {Cmd: config.NewStringOrList("up"), Description: "Start", Aliases: []string{"s"}, DependsOn: []string{"^db:start"}},
		"update": {Cmd: config.NewStringOrList("true"), Once: []config.OnceStep{
			{ID: "s1", Cmd: config.NewStringOrList("true")},
		}},
	}})

	got := decode[detailJSON](t, e.do(http.MethodGet, "/api/projects/api", ""), http.StatusOK)

	if got.Name != "api" || len(got.Commands) != 2 {
		t.Fatalf("detail = %+v, want 2 commands (aliases folded)", got)
	}
	start, update := got.Commands[0], got.Commands[1]
	if start.Name != "start" || start.Cmd != "up" || start.Description != "Start" ||
		len(start.Aliases) != 1 || len(start.DependsOn) != 1 {
		t.Errorf("start = %+v", start)
	}
	if update.Name != "update" || len(update.Once) != 1 || !update.Once[0].Pending {
		t.Errorf("update = %+v, want a pending once step", update)
	}

	if resp := e.do(http.MethodGet, "/api/projects/ghost", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown project status = %d, want 404", resp.StatusCode)
	}
}

func TestProjectInfo(t *testing.T) {
	e := newEnv(t, nil)
	e.addProject("api", &config.Config{Info: []config.InfoItem{
		{Name: "greeting", Cmd: config.NewStringOrList("echo hello")},
	}})

	got := decode[[]app.InfoResult](t, e.do(http.MethodGet, "/api/projects/api/info", ""), http.StatusOK)
	if len(got) != 1 || got[0].Name != "greeting" || strings.TrimSpace(got[0].Output) != "hello" {
		t.Errorf("info = %+v", got)
	}
}

// sseEvent is one server-sent event.
type sseEvent struct{ name, data string }

// stream reads events from a run's stream until the "end" event.
func (e *env) stream(id string) []sseEvent {
	e.t.Helper()
	resp := e.do(http.MethodGet, "/api/runs/"+id+"/stream", "")
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		e.t.Fatalf("content type = %q", ct)
	}
	var events []sseEvent
	var cur sseEvent
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		case line == "":
			events = append(events, cur)
			if cur.name == "end" {
				return events
			}
			cur = sseEvent{}
		}
	}
	e.t.Fatalf("stream ended without an end event: %v (%v)", events, sc.Err())
	return nil
}

func outputOf(t *testing.T, events []sseEvent) string {
	t.Helper()
	var b strings.Builder
	for _, ev := range events {
		if ev.name != "output" && ev.name != "reset" {
			continue
		}
		if ev.name == "reset" {
			b.Reset()
		}
		var chunk string
		if err := json.Unmarshal([]byte(ev.data), &chunk); err != nil {
			t.Fatalf("output data %q: %v", ev.data, err)
		}
		b.WriteString(chunk)
	}
	return b.String()
}

func endInfo(t *testing.T, events []sseEvent) runs.Info {
	t.Helper()
	var info runs.Info
	if err := json.Unmarshal([]byte(events[len(events)-1].data), &info); err != nil {
		t.Fatalf("end data: %v", err)
	}
	return info
}

func TestRunAndStream(t *testing.T) {
	e := newEnv(t, nil)
	e.addProject("api", &config.Config{Commands: map[string]config.Command{"hi": cmd("echo hi")}})

	info := decode[runs.Info](t, e.do(http.MethodPost, "/api/runs", `{"project":"api","command":"hi","args":["there"]}`), http.StatusCreated)
	if info.ID == "" || info.Project != "api" || info.Command != "hi" {
		t.Fatalf("run = %+v", info)
	}

	events := e.stream(info.ID)
	if got := outputOf(t, events); got != "hi there\n" {
		t.Errorf("output = %q, want %q", got, "hi there\n")
	}
	if end := endInfo(t, events); end.Status != runs.Succeeded {
		t.Errorf("end = %+v, want succeeded", end)
	}

	list := decode[[]runs.Info](t, e.do(http.MethodGet, "/api/runs", ""), http.StatusOK)
	if len(list) != 1 || list[0].ID != info.ID || list[0].Status != runs.Succeeded {
		t.Errorf("runs = %+v", list)
	}
}

func TestRunStop(t *testing.T) {
	e := newEnv(t, nil)
	e.addProject("api", &config.Config{Commands: map[string]config.Command{"logs": cmd("echo started; sleep 30")}})

	info := decode[runs.Info](t, e.do(http.MethodPost, "/api/runs", `{"project":"api","command":"logs"}`), http.StatusCreated)
	if resp := e.do(http.MethodPost, "/api/runs/"+info.ID+"/stop", ""); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("stop status = %d, want 202", resp.StatusCode)
	}
	if end := endInfo(t, e.stream(info.ID)); end.Status != runs.Cancelled {
		t.Errorf("end = %+v, want cancelled", end)
	}
	if resp := e.do(http.MethodPost, "/api/runs/999/stop", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown run stop status = %d, want 404", resp.StatusCode)
	}
}

func TestStartRunValidation(t *testing.T) {
	e := newEnv(t, nil)
	e.addProject("api", &config.Config{Commands: map[string]config.Command{"hi": cmd("echo hi")}})

	for _, tt := range []struct {
		body string
		want int
	}{
		{`not json`, http.StatusBadRequest},
		{`{"project":"api"}`, http.StatusBadRequest},
		{`{"project":"ghost","command":"hi"}`, http.StatusNotFound},
		{`{"project":"api","command":"nope"}`, http.StatusNotFound},
	} {
		if resp := e.do(http.MethodPost, "/api/runs", tt.body); resp.StatusCode != tt.want {
			t.Errorf("%s: status = %d, want %d", tt.body, resp.StatusCode, tt.want)
		}
	}
}

func TestGraphEndpoint(t *testing.T) {
	e := newEnv(t, nil)
	e.addProject("api", &config.Config{Commands: map[string]config.Command{"start": cmd("true")}})
	e.addProject("web", &config.Config{Commands: map[string]config.Command{
		"start": {Cmd: config.NewStringOrList("true"), DependsOn: []string{"^api:start"}},
	}})

	g := decode[app.Graph](t, e.do(http.MethodGet, "/api/graph", ""), http.StatusOK)
	if len(g.Projects) != 2 || len(g.Edges) != 1 {
		t.Fatalf("graph = %+v", g)
	}
	if want := (app.DepEdge{From: "web", FromCommand: "start", To: "api", ToCommand: "start"}); g.Edges[0] != want {
		t.Errorf("edge = %+v, want %+v", g.Edges[0], want)
	}
}
