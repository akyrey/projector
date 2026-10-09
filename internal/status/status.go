// Package status reports the state of registered projects: git branch and
// changes, docker compose containers, and pending once steps.
package status

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
)

// composeDirLabel is set by docker compose on every container it creates; it
// holds the project directory, which is how containers are mapped to projects.
const composeDirLabel = "com.docker.compose.project.working_dir"

// Project is a project to report on.
type Project struct {
	Name string
	Dir  string
}

// Git summarises a working tree.
type Git struct {
	IsRepo      bool
	Branch      string // "(detached)" when HEAD is detached
	HasUpstream bool
	Ahead       int
	Behind      int
	Changed     int // modified, staged, and untracked entries
}

// Containers counts the compose containers of one project directory.
type Containers struct {
	Running int
	Total   int
}

// PendingFunc returns how many once steps are pending in a project directory.
type PendingFunc func(dir string) (int, error)

// Row is the collected status of one project.
type Row struct {
	Name       string
	Dir        string
	Git        Git
	GitErr     error
	Containers Containers
	Pending    int
	PendingErr error
}

// ReadGit reads the git state of dir. A directory outside any repository
// yields Git{IsRepo: false} and no error.
func ReadGit(ctx context.Context, dir string) (Git, error) {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain=v2", "--branch")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C") // stable messages to match below
	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if strings.Contains(stderr.String(), "not a git repository") {
			return Git{}, nil
		}
		return Git{}, fmt.Errorf("git status in %s: %w: %s", dir, err, strings.TrimSpace(stderr.String()))
	}
	return parseGitStatus(string(out)), nil
}

// parseGitStatus parses `git status --porcelain=v2 --branch` output.
func parseGitStatus(out string) Git {
	g := Git{IsRepo: true}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			g.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.upstream "):
			g.HasUpstream = true
		case strings.HasPrefix(line, "# branch.ab "):
			// Format: "# branch.ab +<ahead> -<behind>"
			fields := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
			if len(fields) == 2 {
				g.Ahead, _ = strconv.Atoi(strings.TrimPrefix(fields[0], "+"))
				g.Behind, _ = strconv.Atoi(strings.TrimPrefix(fields[1], "-"))
			}
		case line != "" && !strings.HasPrefix(line, "#"):
			g.Changed++
		}
	}
	return g
}

// ListContainers returns compose containers grouped by project directory,
// using a single `docker ps` call.
func ListContainers(ctx context.Context) (map[string]Containers, error) {
	format := fmt.Sprintf(`{{.Label %q}}	{{.State}}`, composeDirLabel)
	out, err := exec.CommandContext(ctx, "docker", "ps", "--all", "--format", format).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("docker ps: %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	byDir := make(map[string]Containers)
	for dir, c := range ParseContainers(string(out)) {
		key := canonical(dir)
		prev := byDir[key]
		byDir[key] = Containers{Running: prev.Running + c.Running, Total: prev.Total + c.Total}
	}
	return byDir, nil
}

// canonical cleans path and resolves symlinks, falling back to the cleaned
// path when it cannot be resolved (e.g. it no longer exists).
func canonical(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// ParseContainers parses "<working_dir>\t<state>" lines. Containers without a
// compose working directory are ignored.
func ParseContainers(out string) map[string]Containers {
	byDir := make(map[string]Containers)
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		dir, state, ok := strings.Cut(sc.Text(), "\t")
		if !ok || dir == "" {
			continue
		}
		c := byDir[dir]
		c.Total++
		if state == "running" {
			c.Running++
		}
		byDir[dir] = c
	}
	return byDir
}

// Collect gathers the status of each project concurrently, preserving order.
// containers may be nil when docker is unavailable; its keys must be canonical
// paths (as returned by ListContainers).
func Collect(ctx context.Context, projects []Project, containers map[string]Containers, pending PendingFunc) []Row {
	rows := make([]Row, len(projects))
	var wg sync.WaitGroup
	for i, p := range projects {
		wg.Add(1)
		go func(i int, p Project) {
			defer wg.Done()
			row := Row{Name: p.Name, Dir: p.Dir, Containers: containers[canonical(p.Dir)]}
			row.Git, row.GitErr = ReadGit(ctx, p.Dir)
			row.Pending, row.PendingErr = pending(p.Dir)
			rows[i] = row
		}(i, p)
	}
	wg.Wait()
	return rows
}

// Render writes rows as an aligned table. dockerOK reports whether container
// counts are known; when false the column shows "?".
func Render(w io.Writer, rows []Row, dockerOK bool) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "PROJECT\tBRANCH\tGIT\tCONTAINERS\tONCE STEPS"); err != nil {
		return err
	}
	for _, r := range rows {
		branch, gitState := describeGit(r.Git, r.GitErr)
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			r.Name, branch, gitState, describeContainers(r.Containers, dockerOK), describePending(r.Pending, r.PendingErr),
		); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func describeGit(g Git, err error) (branch, state string) {
	if err != nil {
		return "-", "error: " + err.Error()
	}
	if !g.IsRepo {
		return "-", "not a git repo"
	}

	var parts []string
	if g.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("↑%d", g.Ahead))
	}
	if g.Behind > 0 {
		parts = append(parts, fmt.Sprintf("↓%d", g.Behind))
	}
	if !g.HasUpstream {
		parts = append(parts, "no upstream")
	}
	if g.Changed > 0 {
		parts = append(parts, fmt.Sprintf("%d changed", g.Changed))
	}
	if len(parts) == 0 {
		return g.Branch, "clean"
	}
	return g.Branch, strings.Join(parts, " ")
}

func describeContainers(c Containers, dockerOK bool) string {
	switch {
	case !dockerOK:
		return "?"
	case c.Total == 0:
		return "-"
	default:
		return fmt.Sprintf("%d/%d running", c.Running, c.Total)
	}
}

func describePending(n int, err error) string {
	switch {
	case err != nil:
		return "error: " + err.Error()
	case n == 0:
		return "-"
	default:
		return fmt.Sprintf("%d pending", n)
	}
}
