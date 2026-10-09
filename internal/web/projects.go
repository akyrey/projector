package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/akyrey/projector/internal/config"
	"github.com/akyrey/projector/internal/project"
	"github.com/akyrey/projector/internal/status"
)

type gitJSON struct {
	IsRepo      bool   `json:"isRepo"`
	Branch      string `json:"branch,omitempty"`
	HasUpstream bool   `json:"hasUpstream"`
	Ahead       int    `json:"ahead"`
	Behind      int    `json:"behind"`
	Changed     int    `json:"changed"`
}

type containersJSON struct {
	Running int `json:"running"`
	Total   int `json:"total"`
}

type projectJSON struct {
	Name     string  `json:"name"`
	Dir      string  `json:"dir"`
	Git      gitJSON `json:"git"`
	GitError string  `json:"gitError,omitempty"`
	// Containers is null when docker is unavailable.
	Containers   *containersJSON `json:"containers"`
	Pending      int             `json:"pending"`
	PendingError string          `json:"pendingError,omitempty"`
}

type onceJSON struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	Cmd         string `json:"cmd"`
	Pending     bool   `json:"pending"`
}

type commandJSON struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Cmd         string     `json:"cmd"`
	Aliases     []string   `json:"aliases"`
	DependsOn   []string   `json:"dependsOn"`
	Once        []onceJSON `json:"once"`
}

type detailJSON struct {
	Name     string        `json:"name"`
	Dir      string        `json:"dir"`
	Commands []commandJSON `json:"commands"`
	HasInfo  bool          `json:"hasInfo"`
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.app.Projects(nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	dockerCtx, cancel := context.WithTimeout(r.Context(), status.DockerTimeout)
	defer cancel()
	containers, dockerErr := s.listContainers(dockerCtx)

	rows := status.Collect(r.Context(), projects, containers, s.app.PendingOnceCount)
	out := make([]projectJSON, len(rows))
	for i, row := range rows {
		out[i] = toProjectJSON(row, dockerErr == nil)
	}
	writeJSON(w, http.StatusOK, out)
}

func toProjectJSON(row status.Row, dockerOK bool) projectJSON {
	p := projectJSON{
		Name: row.Name,
		Dir:  row.Dir,
		Git: gitJSON{
			IsRepo:      row.Git.IsRepo,
			Branch:      row.Git.Branch,
			HasUpstream: row.Git.HasUpstream,
			Ahead:       row.Git.Ahead,
			Behind:      row.Git.Behind,
			Changed:     row.Git.Changed,
		},
		Pending: row.Pending,
	}
	if row.GitErr != nil {
		p.GitError = row.GitErr.Error()
	}
	if row.PendingErr != nil {
		p.PendingError = row.PendingErr.Error()
	}
	if dockerOK {
		p.Containers = &containersJSON{Running: row.Containers.Running, Total: row.Containers.Total}
	}
	return p
}

// lookupProject resolves a registered project, writing a 404 when unknown.
func (s *Server) lookupProject(w http.ResponseWriter, name string) (config.Project, bool) {
	proj, err := s.app.Registry().Get(name)
	switch {
	case errors.Is(err, project.ErrNotFound):
		writeError(w, http.StatusNotFound, fmt.Sprintf("project %q is not registered", name))
		return config.Project{}, false
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return config.Project{}, false
	}
	return proj, true
}

func (s *Server) projectDetail(w http.ResponseWriter, r *http.Request) {
	proj, ok := s.lookupProject(w, r.PathValue("name"))
	if !ok {
		return
	}
	merged, err := s.app.Loader().LoadForProject(proj.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("load config: %v", err))
		return
	}

	names := make([]string, 0, len(merged.Commands))
	for name, cmd := range merged.Commands {
		if cmd.AliasOf == "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	detail := detailJSON{Name: r.PathValue("name"), Dir: proj.Path, Commands: make([]commandJSON, 0, len(names)), HasInfo: len(merged.Info) > 0}
	for _, name := range names {
		c, err := s.toCommandJSON(proj.Path, name, merged.Commands[name])
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		detail.Commands = append(detail.Commands, c)
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) toCommandJSON(dir, name string, cmd config.Command) (commandJSON, error) {
	pending, err := s.app.PendingOnceSteps(dir, name, cmd)
	if err != nil {
		return commandJSON{}, err
	}
	isPending := make(map[string]bool, len(pending))
	for _, step := range pending {
		isPending[step.ID] = true
	}

	c := commandJSON{
		Name:        name,
		Description: cmd.Description,
		Cmd:         cmd.Cmd.String(),
		Aliases:     nonNil(cmd.Aliases),
		DependsOn:   nonNil(cmd.DependsOn),
		Once:        make([]onceJSON, len(cmd.Once)),
	}
	for i, step := range cmd.Once {
		c.Once[i] = onceJSON{ID: step.ID, Description: step.Description, Cmd: step.Cmd.String(), Pending: isPending[step.ID]}
	}
	return c, nil
}

func (s *Server) projectInfo(w http.ResponseWriter, r *http.Request) {
	proj, ok := s.lookupProject(w, r.PathValue("name"))
	if !ok {
		return
	}
	results, err := s.app.Info(r.Context(), proj.Path, infoTimeout)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// nonNil returns s, or an empty slice so JSON gets [] instead of null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
