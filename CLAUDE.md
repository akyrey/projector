# CLAUDE.md — projector

Guidelines for working on this codebase with Claude Code.

## What this project is

`projector` is a Go CLI tool that lets users define named commands per project
and run them across multiple directories concurrently. Commands are resolved
from a hierarchy of YAML config files (global → ancestor dirs → cwd).

## Build & test

```bash
make build        # build ./bin/projector
make test         # go test ./...
make test-js      # node tests for the dashboard's graph layout
make test-race    # go test -race ./...
make test-cover   # coverage report
make vet          # go vet ./...
make fmt          # gofmt -w -s .
make tidy         # go mod tidy && go mod verify
```

Always run `make test-race` before committing. All tests must pass.

## Project layout

```
cmd/projector/main.go       # entry point only — no logic here
internal/
  cli/                      # cobra command wiring (no business logic)
  app/                      # use cases shared by cli and web: run, once steps, info
  config/                   # types, file I/O, hierarchical loader + merge
  project/                  # project registry (reads/writes global config)
  runner/                   # command execution + depends_on resolution
  state/                    # machine-local state (applied once steps)
  status/                   # git / docker / once-step status for `projector status`
  runs/                     # background runs for the dashboard: output buffer + subscribers
  web/                      # `projector serve`: JSON API, SSE, auth, embedded frontend
  editor/                   # $EDITOR integration
```

Packages follow standard Go layout: `cmd/` for binaries, `internal/` for all
application code (not intended to be imported by external packages).

## Architecture rules

- **`internal/cli`** contains only cobra wiring. All real logic lives in the
  other packages. Commands construct dependencies and call into them; they do
  not implement algorithms.
- **`internal/app`** holds the use cases both front ends share: `App.Run`
  (cross-project deps, local deps, once steps), `Projects`, `PendingOnceCount`,
  `Info`. Each `Run` call takes the `*runner.Runner` to write to, so the CLI
  uses the terminal and the dashboard a per-run buffer.
- **`internal/web`** is HTTP only: it validates input, calls `app`, and shapes
  JSON. Its frontend in `static/` is embedded, vanilla JS with no build step;
  render data with `textContent`, never `innerHTML`.
- **`internal/runs`** tracks runs without knowing what they execute (`runs.Func`).
- **`internal/config`** is pure data: types, YAML serialisation, and the merge
  algorithm. It has no knowledge of CLI flags or execution.
- **`internal/runner`** is pure execution: no config loading, no project
  registry lookups. It receives already-resolved `Target` structs.
- **`internal/state`** stores machine-local data outside repos, one file per
  project dir under `$XDG_STATE_HOME/projector`. It knows nothing of config.
- **`internal/status`** gathers git and docker state; pending once-step counts
  come in through an injected `PendingFunc`, so it imports neither config nor state.
- Dependencies flow inward: `cli → app, web, config, project, runner, editor,
  state, status`; `web → app, runs, runner, status`; `app → config, project,
  runner, state, status`. Inner packages must not import `cli`, `web` or `app`.

## Config types

```go
// StringOrList holds a single shell command string or an ordered list of them.
// Multiple commands run sequentially; the chain aborts on the first non-zero exit.
// YAML accepts both forms:
//   cmd: "single command"
//   cmd:
//     - "first command"
//     - "second command"
type StringOrList struct { /* unexported */ }

func NewStringOrList(cmds ...string) StringOrList
func (s StringOrList) IsEmpty() bool   // true when no non-empty command strings
func (s StringOrList) IsMulti() bool   // true when more than one command string
func (s StringOrList) Values() []string
func (s StringOrList) String() string  // joins with " && " for display

// A single config file
type Config struct {
    Projects map[string]Project `yaml:"projects,omitempty"` // global only
    Commands map[string]Command `yaml:"commands,omitempty"`
    Services map[string]Service `yaml:"services,omitempty"`
    Exclude  []string           `yaml:"exclude,omitempty"` // drop inherited commands
    Info     []InfoItem         `yaml:"info,omitempty"`    // dashboard info items
}

// InfoItem merges by Name: closer replaces in place, no Cmd removes it.
type InfoItem struct {
    Name        string       `yaml:"name"`
    Description string       `yaml:"description,omitempty"`
    Cmd         StringOrList `yaml:"cmd"`
}

type Command struct {
    Cmd           StringOrList      `yaml:"cmd"`
    Description   string            `yaml:"description,omitempty"`
    Env           map[string]string `yaml:"env,omitempty"`
    DependsOn     []string          `yaml:"depends_on,omitempty"`
    Aliases       []string          `yaml:"aliases,omitempty"`
    Preconditions []string          `yaml:"preconditions,omitempty"`
    Once          []OnceStep        `yaml:"once,omitempty"`
}

// OnceStep runs after Cmd at most once per machine (see "Once steps").
type OnceStep struct {
    ID          string       `yaml:"id"`
    Description string       `yaml:"description,omitempty"`
    Cmd         StringOrList `yaml:"cmd"`
}

type Project struct {
    Path string `yaml:"path"`
}

// Service auto-generates commands by prepending Exec to each command suffix.
type Service struct {
    Exec     string            `yaml:"exec"`
    Commands map[string]string `yaml:"commands,omitempty"`
}

// The merged result of all config layers
type MergedConfig struct {
    Projects map[string]Project
    Commands map[string]Command
    Services map[string]Service
    Info     []InfoItem
}
```

Merge semantics: later (closer) entries override earlier ones for the same key.
Merging happens in `config.mergeInto` (`internal/config/loader.go`).

Alias entries in the merged map carry `AliasOf` (the canonical name, `yaml:"-"`).

`Exclude` names are applied after the same file's commands: they delete the
command and its aliases from the merged map and are remembered in
`MergedConfig.excluded`, so `expandServices` skips them too. Only an explicit
command in a closer layer clears the exclusion; a closer service does not.

After all layers are merged, `expandServices` is called in `Load` to expand
`Services` entries into `Commands`, then `validateOnceSteps` rejects once steps
without an id or cmd, or with duplicate ids (`ErrInvalidOnceStep`), and
`validateInfo` rejects nameless info items (`ErrInvalidInfoItem`). See the
services section below.

### StringOrList serialisation notes

- `MarshalYAML`: single-element list → scalar (round-trip clean); multi-element
  list → YAML sequence; empty → `nil` (field omitted in output).
- `UnmarshalYAML`: scalar `""` or `!!null` tag → empty (not `[]string{""}`).
  This is the correct sentinel for "no cmd set" used by `expandServices`.
- `IsEmpty()` considers a list empty if it contains no non-empty strings.
- Use `existing.Cmd.IsEmpty()` (not `== ""`) to detect unset `cmd` in
  `expandServices` and anywhere else that checks whether a command has been
  given an explicit shell string.

## Config hierarchy

Resolution order (lowest → highest priority):

1. `~/.config/projector/config.yaml` (global)
2. `<ancestor>/.projector.yaml` files, walking from `/` down to cwd
3. `<cwd>/.projector.yaml`

`XDG_CONFIG_HOME` is respected. Missing files are silently skipped.

The `Loader` type (`internal/config/loader.go`) handles all of this.
`NewLoaderWithGlobal(path)` is provided for use in tests.

## Services

`services` is a top-level config key that auto-generates commands from a shared
execution prefix (e.g. `docker compose exec <container>`). This avoids repeating
the prefix in every command definition.

```yaml
services:
  sail:
    exec: "docker compose exec laravel.test"
    commands:
      artisan:  "php artisan"   # → docker compose exec laravel.test php artisan
      composer: "composer"      # → docker compose exec laravel.test composer
      pnpm:     "pnpm"          # → docker compose exec laravel.test pnpm
```

`projector artisan cache:clear` then runs
`docker compose exec laravel.test php artisan cache:clear`.

### Merge semantics for services

Service expansion runs **after** all config layers are merged. Interaction with
an explicit `commands` entry of the same name:

| Explicit entry has `cmd`? | Result |
|---------------------------|--------|
| No explicit entry          | Generated command is inserted as-is. |
| Yes (`cmd` is set)         | Explicit command wins; generated one is discarded. |
| No (`cmd` is empty)        | Generated `cmd` is used; explicit metadata (description, env, depends_on, etc.) is preserved. |

The third case lets you annotate a service-generated command without repeating
the exec prefix:

```yaml
services:
  sail:
    exec: "docker compose exec laravel.test"
    commands:
      artisan: "php artisan"

commands:
  artisan:
    # No cmd: field — keeps the service-generated cmd
    description: "Run artisan inside the container"
    depends_on: [build-assets]
    aliases: [art]
```

Services follow the same config hierarchy as commands: a local service definition
overrides a global one with the same key.

## depends_on

`depends_on` entries come in two forms:

**Local deps** — plain command names resolved within the current project:

```yaml
depends_on: [install, build]
```

`ResolveDependencyOrder` in `internal/runner/deps.go` performs a topological
sort (Kahn's algorithm) over the transitive closure of a root command's
`depends_on` entries. It returns:

- `ErrCyclicDependency` with a human-readable cycle path on cycles
- `ErrUnknownDependency` when a dep references a non-existent command

`Runner.RunWithDeps` runs the returned order sequentially. A failing step
aborts the chain. It uses `t.Name` (not `t.Command.Cmd`) as the root key.

**Cross-project deps** — entries prefixed with `^` that reference a command in
another registered project:

```yaml
depends_on:
  - "^lib:build"      # run 'build' in the registered project named 'lib'
  - run-migrations    # local dep, unchanged
```

Cross-project resolution lives entirely in `internal/app/run.go` (not in the
runner) to keep the runner pure. `partitionDeps` splits the `DependsOn` slice
into cross-project and local lists. `runCrossProjectDeps` loads each referenced
project's config and executes the command before the local dep chain runs.

Cross-project deps run **before** local deps. Transitive cross-project deps are
resolved recursively by `runCrossProjectDeps` with a `crossDepWalk`: its
`inProgress` set (the recursion path) detects cycles, and its `done` set makes
each `proj:cmd` run at most once per invocation, including diamonds and deps
shared by several named projects.

Sentinel: `ErrCrossProjectCycle` (defined in `internal/app/run.go`, re-exported by `cli`) — returned
when a cycle is detected across project boundaries.

## Runner

```go
// Single target, no prefix
runner.Run(ctx, target)

// Single target with dep chain (sequential)
runner.RunWithDeps(ctx, target, commands)

// Multiple targets, concurrent, prefixed output
runner.RunConcurrent(ctx, targets)

// Multiple targets, concurrent, each with its own dep chain
runner.RunConcurrentWithDeps(ctx, depTargets)
```

`RunConcurrent` and `RunConcurrentWithDeps` use `errgroup` for cancellation
and a shared `sync.Mutex` to serialise writes to the prefix writer.

For a single target, both concurrent variants delegate to the non-concurrent
equivalents (no prefix, cleaner output).

`runner.New(stdin, stdout, stderr, runner.WithProcessGroup())` starts every
command in its own process group: cancelling the context sends SIGTERM to the
whole group, then SIGKILL after `killGrace`, and `WaitDelay` (`killGrace`)
stops Wait from hanging on pipes held by grandchildren. The dashboard and info items use it; the CLI does not, since
a command outside the terminal's foreground group cannot read from it.

### Follow-ups

`Target.FollowUps` run in order after the target's own command succeeds (also
when `Cmd` is empty), in the same dir; `ExtraArgs` don't apply to them and
`RunWithDeps` attaches them to the root command only. After each step succeeds
the runner calls `OnSuccess` (skipped in dry-run); any failure stops the rest.
The runner stays stateless: callers persist what they need in `OnSuccess`.

### Multi-step commands

When `t.Command.Cmd` contains more than one string (`IsMulti() == true`),
`runCommand` loops over `t.Command.Cmd.Values()` and executes each step via
`sh -c` in sequence. The chain aborts on the first non-zero exit. `ExtraArgs`
are appended only to the **last** step. Preconditions run once before the
first step. In dry-run mode each step is printed separately.

## Once steps

`App.onceFollowUps` (`internal/app/once.go`) reads applied step IDs from
`state.Store` keyed by `(Loader.ProjectRoot(dir), canonical command name)`:
the nearest dir with a `.projector.yaml`, symlinks resolved, and `AliasOf` when
invoked through an alias. It turns the pending steps into
follow-ups (inheriting the command's `Env`), and records each with
`MarkApplied` in `OnSuccess`. It is used on all three run paths: cwd, named
projects, and cross-project deps. The store is created lazily (`lazyStore`,
`sync.Once`, safe for concurrent `status` and dashboard lookups), so commands without once
steps never touch the state dir. Only the invoked command's once steps run, not
those of its local `depends_on` commands. Tests set `XDG_STATE_HOME` with
`t.Setenv`.

## Web dashboard

`projector serve` (`internal/cli/serve.go`) listens via `web.ListenLocal`, which
refuses non-loopback addresses, with a fresh `web.NewToken()` per start, and
prints `http://<addr>/#token=<token>`. `web.Server` wraps every route in `guard`
(`internal/web/auth.go`): CSP and security headers on every response, loopback
`Host` only (DNS rebinding), and `Authorization: Bearer <token>` on `/api/`.
The page and `/static/` are public. Never accept the token from a cookie or the
query string: cookies are shared by every localhost port (dev servers would get
them), and a header-only credential is what makes CSRF checks unnecessary. The
page keeps the token in `localStorage` and streams with `fetch`, because
EventSource cannot send headers.

`POST /api/runs` decodes strictly, validates the project and command,
shell-quotes each arg (`shellQuoteAll`: args are literal words, never shell
syntax), then starts a `runs.Run` (`ErrTooManyRunning` → 429, `ErrClosed` →
503) that calls `App.Run` with a process-group runner writing to the run. Its
output is streamed by `GET /api/runs/{id}/stream` as SSE: `reset` (the buffered
output; sent first, after lagging, and before `end`), `output` (a chunk), `end`
(final `runs.Info`). Chunks are cut on UTF-8 rune boundaries (`splitUTF8`).
`GET /api/graph` returns `App.Graph`: every `^project:command` edge of the
registered projects (aliases skipped, unregistered targets kept, config errors
per project). The frontend lays it out in `static/graph.js` (`layoutGraph`, a
pure function also loaded by node in `testdata/graph_test.js`; keep it free of
DOM access). `App.Info` caps output (`cappedWriter`, stops the command when full) and
concurrency (`infoSlots`). Tests use `web.WithContainerLister` to avoid docker
and `web.WithMaxRunning` for limits.

## Error handling

- Always wrap errors with context: `fmt.Errorf("load config: %w", err)`
- Sentinel errors (`ErrNotFound`, `ErrAlreadyExists`, `ErrCyclicDependency`,
  `ErrUnknownDependency`, `ErrCrossProjectCycle`, `ErrInvalidOnceStep`,
  `ErrInvalidInfoItem`) are
  defined in their respective packages and must be checked with `errors.Is`.
- `internal/cli` commands return errors; `main.go` prints them and sets exit
  code. Never call `os.Exit` inside library code.

## Testing conventions

- Test files use `package foo_test` (black-box testing).
- Use `t.TempDir()` for all temporary file I/O — never hardcode `/tmp`.
- Use `t.Setenv` to override environment variables in tests.
- `config.NewLoaderWithGlobal(path)` is the seam for injecting a custom global
  config path in tests without touching the real `~/.config`.
- Table-driven tests are preferred for multiple input cases.
- The race detector must pass: `go test -race ./...`.

## Adding a new CLI command

1. Create `internal/cli/<name>.go` with a `newXxxCmd(d *deps) *cobra.Command` function.
2. Register it in `internal/cli/root.go` with `root.AddCommand(newXxxCmd(d))`.
3. Put all non-trivial logic in the appropriate `internal/` package, not in the
   command's `RunE`.
4. Add shell completion via `ValidArgsFunction` if the command takes named args.

## Adding a new config field

1. Add the field to the appropriate type in `internal/config/types.go` with a
   `yaml:"..."` tag and `omitempty` unless the field is always required.
2. If the field should merge (not just replace), update `mergeInto` in
   `internal/config/loader.go`.
3. Update tests in `internal/config/loader_test.go`.

## Dependencies

Keep the dependency count low. Current direct deps:

| Package | Use |
|---------|-----|
| `github.com/spf13/cobra` | CLI |
| `gopkg.in/yaml.v3` | Config parsing |
| `golang.org/x/sync/errgroup` | Concurrent execution |
| `github.com/fatih/color` | Colored output |

Do not add a dependency when the standard library suffices.

## Release

Releases are cut with goreleaser. The version is injected at build time:

```bash
-ldflags "-X main.version=<tag>"
```

Release notes come from `CHANGELOG.md`: add a `## [vX.Y.Z] - date` section
(Keep a Changelog groups: Added, Changed, Fixed...) and its compare link before
tagging. The release workflow extracts it with `scripts/release-notes.sh <tag>`
and passes it to goreleaser via `--release-notes`; a tag without a section
fails the release.

Dry-run: `goreleaser release --snapshot --clean`
Full release: add the CHANGELOG section, tag (`git tag -a v1.2.3`) and push the tag; CI runs goreleaser. Locally: `goreleaser release --clean --release-notes=<(scripts/release-notes.sh v1.2.3)`
