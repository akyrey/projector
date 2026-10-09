package app_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/akyrey/projector/internal/app"
	"github.com/akyrey/projector/internal/config"
)

func edgeStrings(edges []app.DepEdge) string {
	out := make([]string, len(edges))
	for i, e := range edges {
		out[i] = fmt.Sprintf("%s:%s>%s:%s", e.From, e.FromCommand, e.To, e.ToCommand)
	}
	return strings.Join(out, ",")
}

func TestGraph(t *testing.T) {
	f := newFixture(t)
	f.addProject(t, "web", &config.Config{Commands: map[string]config.Command{
		"start": {Cmd: config.NewStringOrList("true"), DependsOn: []string{"^api:start", "build"}, Aliases: []string{"s"}},
		"build": {Cmd: config.NewStringOrList("true")},
	}})
	f.addProject(t, "api", &config.Config{Commands: map[string]config.Command{
		"start":  {Cmd: config.NewStringOrList("true"), DependsOn: []string{"^proxy:start", "^ghost:start"}},
		"update": {Cmd: config.NewStringOrList("true"), DependsOn: []string{"^proxy:update"}},
	}})
	f.addProject(t, "proxy", &config.Config{Commands: map[string]config.Command{"start": {Cmd: config.NewStringOrList("true")}}})
	f.addProject(t, "broken", &config.Config{Commands: map[string]config.Command{
		"start": {Cmd: config.NewStringOrList("true"), DependsOn: []string{"^nope"}},
	}})

	g, err := f.app.Graph()
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}

	if got := strings.Join(g.Projects, ","); got != "api,broken,proxy,web" {
		t.Errorf("Projects = %s", got)
	}
	// Sorted by project then command; aliases do not duplicate edges; edges
	// to unregistered projects are kept so the UI can flag them.
	want := "api:start>ghost:start,api:start>proxy:start,api:update>proxy:update,web:start>api:start"
	if got := edgeStrings(g.Edges); got != want {
		t.Errorf("Edges = %s, want %s", got, want)
	}
	if !strings.Contains(g.Errors["broken"], "^nope") {
		t.Errorf("Errors = %v, want the malformed dep reported for broken", g.Errors)
	}
}
