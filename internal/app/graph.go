package app

import (
	"fmt"
	"sort"
)

// DepEdge is a cross-project dependency: command FromCommand in project From
// depends on ToCommand in project To (`^To:ToCommand`).
type DepEdge struct {
	From        string `json:"from"`
	FromCommand string `json:"fromCommand"`
	To          string `json:"to"`
	ToCommand   string `json:"toCommand"`
}

// Graph is the cross-project dependency graph of the registered projects.
type Graph struct {
	// Projects are the registered project names, sorted.
	Projects []string `json:"projects"`

	// Edges are sorted by From, FromCommand, To, ToCommand. An edge may point
	// to a project that is not registered.
	Edges []DepEdge `json:"edges"`

	// Errors maps a project to the reason its config could not be read.
	Errors map[string]string `json:"errors"`
}

// Graph collects every cross-project dependency declared by the registered
// projects' commands (aliases excluded). A project whose config fails to load
// or declares a malformed dependency is reported in Errors, not as an error.
func (a *App) Graph() (Graph, error) {
	projects, err := a.Projects(nil)
	if err != nil {
		return Graph{}, err
	}

	g := Graph{Projects: make([]string, 0, len(projects)), Edges: []DepEdge{}, Errors: map[string]string{}}
	for _, p := range projects {
		g.Projects = append(g.Projects, p.Name)
		edges, err := a.projectEdges(p.Name, p.Dir)
		if err != nil {
			g.Errors[p.Name] = err.Error()
			continue
		}
		g.Edges = append(g.Edges, edges...)
	}

	sort.Slice(g.Edges, func(i, j int) bool {
		x, y := g.Edges[i], g.Edges[j]
		if x.From != y.From {
			return x.From < y.From
		}
		if x.FromCommand != y.FromCommand {
			return x.FromCommand < y.FromCommand
		}
		if x.To != y.To {
			return x.To < y.To
		}
		return x.ToCommand < y.ToCommand
	})
	return g, nil
}

func (a *App) projectEdges(name, dir string) ([]DepEdge, error) {
	merged, err := a.loader.LoadForProject(dir)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	var edges []DepEdge
	for cmdName, cmd := range merged.Commands {
		if cmd.AliasOf != "" {
			continue
		}
		cross, _, err := partitionDeps(cmd.DependsOn)
		if err != nil {
			return nil, fmt.Errorf("command %q: %w", cmdName, err)
		}
		for _, cd := range cross {
			edges = append(edges, DepEdge{From: name, FromCommand: cmdName, To: cd.projName, ToCommand: cd.cmdName})
		}
	}
	return edges, nil
}
