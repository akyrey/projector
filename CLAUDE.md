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
  config/                   # types, file I/O, hierarchical loader + merge
  project/                  # project registry (reads/writes global config)
  runner/                   # command execution + depends_on resolution
  editor/                   # $EDITOR integration
```

Packages follow standard Go layout: `cmd/` for binaries, `internal/` for all
application code (not intended to be imported by external packages).

## Architecture rules

- **`internal/cli`** contains only cobra wiring. All real logic lives in the
  other packages. Commands construct dependencies and call into them; they do
  not implement algorithms.
- **`internal/config`** is pure data: types, YAML serialisation, and the merge
  algorithm. It has no knowledge of CLI flags or execution.
- **`internal/runner`** is pure execution: no config loading, no project
  registry lookups. It receives already-resolved `Target` structs.
- Dependencies flow inward: `cli → config, project, runner, editor`. Inner
  packages (`config`, `runner`) must not import `cli`.

## Config types

```go
// A single config file
type Config struct {
    Projects map[string]Project `yaml:"projects,omitempty"` // global only
    Commands map[string]Command `yaml:"commands,omitempty"`
}

type Command struct {
    Cmd         string            `yaml:"cmd"`
    Description string            `yaml:"description,omitempty"`
    Env         map[string]string `yaml:"env,omitempty"`
    DependsOn   []string          `yaml:"depends_on,omitempty"`
}

type Project struct {
    Path string `yaml:"path"`
}

// The merged result of all config layers
type MergedConfig struct {
    Projects map[string]Project
    Commands map[string]Command
}
```

Merge semantics: later (closer) entries override earlier ones for the same key.
Merging happens in `config.mergeInto` (`internal/config/loader.go`).

## Config hierarchy

Resolution order (lowest → highest priority):

1. `~/.config/projector/config.yaml` (global)
2. `<ancestor>/.projector.yaml` files, walking from `/` down to cwd
3. `<cwd>/.projector.yaml`

`XDG_CONFIG_HOME` is respected. Missing files are silently skipped.

The `Loader` type (`internal/config/loader.go`) handles all of this.
`NewLoaderWithGlobal(path)` is provided for use in tests.

## depends_on

`ResolveDependencyOrder` in `internal/runner/deps.go` performs a topological
sort (Kahn's algorithm) over the transitive closure of a root command's
`depends_on` entries. It returns:

- `ErrCyclicDependency` with a human-readable cycle path on cycles
- `ErrUnknownDependency` when a dep references a non-existent command

`Runner.RunWithDeps` runs the returned order sequentially. A failing step
aborts the chain. It uses `t.Name` (not `t.Command.Cmd`) as the root key.

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

## Error handling

- Always wrap errors with context: `fmt.Errorf("load config: %w", err)`
- Sentinel errors (`ErrNotFound`, `ErrAlreadyExists`, `ErrCyclicDependency`,
  `ErrUnknownDependency`) are defined in their respective packages and must be
  checked with `errors.Is`.
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

Dry-run: `goreleaser release --snapshot --clean`
Full release: tag a commit (`git tag v1.2.3`) then `goreleaser release --clean`
