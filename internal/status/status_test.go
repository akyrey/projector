package status_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akyrey/projector/internal/status"
)

// git runs a git command in dir, failing the test on error.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commit(t *testing.T, dir, file string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", file)
	git(t, dir, "commit", "-q", "-m", file)
}

// newRepo creates a repo on branch main with one commit.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	commit(t, dir, "a.txt")
	return dir
}

func TestReadGit_NotARepo(t *testing.T) {
	got, err := status.ReadGit(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("ReadGit: %v", err)
	}
	if got.IsRepo {
		t.Errorf("IsRepo = true for a plain directory")
	}
}

func TestReadGit_BranchAndChanges(t *testing.T) {
	dir := newRepo(t)
	// One modified tracked file and one untracked file.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := status.ReadGit(context.Background(), dir)
	if err != nil {
		t.Fatalf("ReadGit: %v", err)
	}
	want := status.Git{IsRepo: true, Branch: "main", Changed: 2}
	if got != want {
		t.Errorf("ReadGit = %+v, want %+v", got, want)
	}
}

func TestReadGit_AheadBehindUpstream(t *testing.T) {
	origin := newRepo(t)
	clone := filepath.Join(t.TempDir(), "clone")
	git(t, origin, "clone", "-q", origin, clone)

	commit(t, origin, "remote.txt") // clone is 1 behind once fetched
	commit(t, clone, "local1.txt")  // and 2 ahead
	commit(t, clone, "local2.txt")
	git(t, clone, "fetch", "-q")

	got, err := status.ReadGit(context.Background(), clone)
	if err != nil {
		t.Fatalf("ReadGit: %v", err)
	}
	want := status.Git{IsRepo: true, Branch: "main", HasUpstream: true, Ahead: 2, Behind: 1}
	if got != want {
		t.Errorf("ReadGit = %+v, want %+v", got, want)
	}
}

func TestParseContainers(t *testing.T) {
	out := strings.Join([]string{
		"/work/api\trunning",
		"/work/api\trunning",
		"/work/api\texited",
		"/work/web\tcreated",
		"\trunning", // not a compose container
		"",
	}, "\n")

	got := status.ParseContainers(out)

	want := map[string]status.Containers{
		"/work/api": {Running: 2, Total: 3},
		"/work/web": {Running: 0, Total: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("ParseContainers = %v, want %v", got, want)
	}
	for dir, w := range want {
		if got[dir] != w {
			t.Errorf("%s = %+v, want %+v", dir, got[dir], w)
		}
	}
}

func TestCollectAndRender(t *testing.T) {
	repo := newRepo(t)
	plain := t.TempDir()

	projects := []status.Project{
		{Name: "api", Dir: repo},
		{Name: "proxy", Dir: plain},
	}
	// ListContainers keys by canonical path; macOS temp dirs are symlinked.
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	containers := map[string]status.Containers{canonicalRepo: {Running: 2, Total: 3}}
	pending := func(dir string) (int, error) {
		if dir == plain {
			return 0, errors.New("bad config")
		}
		return 2, nil
	}

	rows := status.Collect(context.Background(), projects, containers, pending)

	if len(rows) != 2 || rows[0].Name != "api" || rows[1].Name != "proxy" {
		t.Fatalf("rows = %+v, want api then proxy", rows)
	}

	var out bytes.Buffer
	if err := status.Render(&out, rows, true); err != nil {
		t.Fatalf("Render: %v", err)
	}
	text := out.String()
	for _, want := range []string{"PROJECT", "api", "main", "2/3 running", "2 pending", "proxy", "not a git repo", "error: bad config"} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
}

func TestRender_DockerUnavailable(t *testing.T) {
	rows := []status.Row{{Name: "api", Dir: "/x"}}
	var out bytes.Buffer
	if err := status.Render(&out, rows, false); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out.String(), "?") {
		t.Errorf("want ? for unknown containers, got:\n%s", out.String())
	}
}
