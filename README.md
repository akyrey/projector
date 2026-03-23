# projector

A command runner for developers who work across multiple projects.

Define named commands once, run them across multiple directories concurrently — each project using its own local variant of the command. Inspired by [ThePrimeagen's ts-go-rust-projector](https://github.com/ThePrimeagen/ts-go-rust-projector) course project, rebuilt as a production-grade Go CLI tool.

## The problem it solves

You have several projects. Each has its own way to start, stop, build, or deploy:

```
~/work/my-api        → sail up -d
~/work/my-frontend   → npm run dev
~/work/my-worker     → docker compose up -d worker
```

With projector you define a `start` command in each project's local config and then run them all at once:

```
$ projector start my-api my-frontend my-worker
[my-api]      ... sail output ...
[my-frontend] ... npm output ...
[my-worker]   ... docker output ...
```

Each project's output is prefixed with its name and colored distinctly. All three run concurrently.

## Installation

### From source

```bash
git clone https://github.com/akyrey/projector
cd projector
make install   # installs to $(go env GOPATH)/bin
```

### Build locally

```bash
make build     # produces ./bin/projector
```

### goreleaser (cross-platform)

```bash
goreleaser release --snapshot --clean   # dry run, no tag required
goreleaser release --clean              # requires a git tag
```

Pre-built binaries for Linux, macOS (Intel + Apple Silicon), and Windows are published on the [Releases](https://github.com/akyrey/projector/releases) page.

## Quick start

**1. Define a global default command:**

```bash
projector config set start "docker compose up -d" --global --description "Start services"
projector config set stop  "docker compose down"  --global
```

**2. Override the command for a specific project:**

```bash
cd ~/work/my-api
projector config set start "sail up -d"
```

**3. Register named projects:**

```bash
projector project add my-api      ~/work/my-api
projector project add my-frontend ~/work/my-frontend
```

**4. Run across all of them:**

```bash
projector run start my-api my-frontend
# or with the shorthand:
projector start my-api my-frontend
```

## Configuration

### File locations

Projector merges configs from least to most specific. A closer (more specific) definition always wins:

| Priority | Location |
|----------|----------|
| Lowest   | `~/.config/projector/config.yaml` — global defaults and project registry |
| …        | `<ancestor>/.projector.yaml` — for each directory walking up from cwd |
| Highest  | `<cwd>/.projector.yaml` — the current directory |

`XDG_CONFIG_HOME` is respected; if unset it falls back to `~/.config`.

### File format

```yaml
# Global config: ~/.config/projector/config.yaml

projects:
  my-api:
    path: /home/user/work/my-api
  my-frontend:
    path: /home/user/work/my-frontend

commands:
  start:
    cmd: "docker compose up -d"
    description: "Start all services"
  stop:
    cmd: "docker compose down"
  logs:
    cmd: "docker compose logs -f"
    description: "Follow service logs"
```

```yaml
# Local override: ~/work/my-api/.projector.yaml

commands:
  start:
    cmd: "sail up -d"
    description: "Start Laravel via Sail"
    env:
      SAIL_XDEBUG: "1"
  build:
    cmd: "sail artisan build"
    depends_on:
      - start
```

### Command fields

| Field | Type | Description |
|-------|------|-------------|
| `cmd` | string | Shell command to execute (via `sh -c` on Unix, `cmd /C` on Windows) |
| `description` | string | Optional human-readable summary |
| `env` | map | Extra environment variables set for this command only |
| `depends_on` | list | Other command names that must complete (successfully) before this one runs |
| `aliases` | list | Alternative names that invoke the same command |
| `preconditions` | list | Shell expressions that must exit 0 before the command runs |

### Aliases

A command can declare one or more aliases. Each alias is registered as its own
entry and resolves to the same command definition:

```yaml
commands:
  down:
    cmd: "docker compose down"
    aliases: [stop]
```

Both `projector down` and `projector stop` run `docker compose down`.

### Services

`services` auto-generates commands from a shared execution prefix, eliminating
the need to repeat container boilerplate across many command definitions.

```yaml
services:
  sail:
    exec: "docker compose exec laravel.test"
    commands:
      artisan:  "php artisan"
      composer: "composer"
      pnpm:     "pnpm"
      node:     "node"
      npm:      "npm"
```

This is equivalent to defining each command explicitly:

```yaml
commands:
  artisan:  { cmd: "docker compose exec laravel.test php artisan" }
  composer: { cmd: "docker compose exec laravel.test composer" }
  pnpm:     { cmd: "docker compose exec laravel.test pnpm" }
  node:     { cmd: "docker compose exec laravel.test node" }
  npm:      { cmd: "docker compose exec laravel.test npm" }
```

Then use them naturally, passing extra arguments after the command name:

```bash
projector artisan cache:clear
# → docker compose exec laravel.test php artisan cache:clear

projector composer install
# → docker compose exec laravel.test composer install

projector pnpm i --frozen-lockfile
# → docker compose exec laravel.test pnpm i --frozen-lockfile
```

**Layering metadata on service-generated commands**

You can annotate a service-generated command (add a description, `depends_on`,
`aliases`, `env`, etc.) without repeating the exec prefix. Leave the `cmd`
field empty — projector fills it in from the service:

```yaml
services:
  sail:
    exec: "docker compose exec laravel.test"
    commands:
      artisan: "php artisan"

commands:
  artisan:
    # No cmd: field — the generated cmd is used
    description: "Run artisan inside the Laravel container"
    depends_on: [build-assets]
    aliases: [art]
```

If you do set `cmd`, it fully overrides the service-generated command:

```yaml
commands:
  composer:
    cmd: "composer"           # run locally, not inside the container
    description: "Run composer locally"
```

Multiple services can be defined, and they follow the same config hierarchy as
commands — a closer (more specific) service definition overrides a farther one
with the same name.

## Usage

### Running commands

```bash
# Run in the current directory
projector run start
projector start           # shorthand — any unrecognised subcommand is treated as `run`

# Run in specific registered projects (concurrently)
projector run start my-api my-frontend my-worker
projector start my-api my-frontend my-worker    # shorthand

# Override the working directory
projector run start --pwd /path/to/project
```

When multiple projects are given, each one resolves its own version of the command from its own `.projector.yaml` hierarchy. Output lines are prefixed `[project-name]` and colored distinctly.

### depends_on

Dependencies are resolved into a topological order and run sequentially before the main command. Cycles are detected and reported clearly.

```yaml
commands:
  install:
    cmd: "npm ci"
  build:
    cmd: "npm run build"
    depends_on: [install]
  deploy:
    cmd: "./scripts/deploy.sh"
    depends_on: [build]
```

```bash
$ projector run deploy
# executes: install → build → deploy
```

Dependency execution happens per-project when running across multiple projects, so each project's chain is isolated.

#### Cross-project dependencies

A `depends_on` entry prefixed with `^` references a command in another registered project:

```yaml
commands:
  deploy:
    cmd: "./scripts/deploy.sh"
    depends_on:
      - "^lib:build"     # run 'build' in the registered project named 'lib' first
      - run-migrations   # local command, unchanged
```

The syntax is `^<project-name>:<command-name>`. The project must be registered via `projector project add`.

Cross-project deps run **before** the local dep chain and the main command. When multiple projects are run concurrently and they share the same cross-project dep, it is deduplicated and runs only once.

Transitive cross-project deps are supported: if `^lib:build` itself depends on `^shared:compile`, the chain resolves correctly (`shared:compile → lib:build → deploy`). Cycles across projects are detected and reported as an error.

### Managing projects

```bash
projector project add    <name> <path>   # register a project
projector project remove <name>          # alias: rm
projector project list                   # list all registered projects
```

Projects are stored in the global config (`~/.config/projector/config.yaml`).

### Managing configuration

```bash
# Add or update a command
projector config set start "sail up -d"
projector config set start "sail up -d" --description "Start Laravel"
projector config set start "sail up -d" --env SAIL_XDEBUG=1 --env APP_ENV=local
projector config set start "sail up -d" --depends-on install
projector config set start "docker compose up -d" --global   # write to global config

# Remove a command
projector config remove start
projector config remove start --global

# Open in $EDITOR (creates the file with a starter skeleton if absent)
projector config edit           # local .projector.yaml
projector config edit --global  # global config

# Inspect the fully resolved (merged) config for the current directory
projector config show
```

### Listing resolved commands

```bash
projector list
```

Shows all commands available in the current context after merging all applicable config files.

### Shell completions

```bash
# Bash
source <(projector completion bash)

# Zsh (add to ~/.zshrc for permanent setup)
source <(projector completion zsh)

# Fish
projector completion fish | source

# PowerShell
projector completion powershell | Out-String | Invoke-Expression
```

## Development

### Prerequisites

- Go 1.21+
- [golangci-lint](https://golangci-lint.run/) (optional, for `make lint`)
- [goreleaser](https://goreleaser.com/) (optional, for release builds)

### Common tasks

```bash
make build        # build ./bin/projector
make install      # install to $GOPATH/bin
make test         # run tests
make test-race    # run tests with the race detector
make test-cover   # run tests and print coverage
make vet          # run go vet
make fmt          # gofmt -w -s .
make lint         # golangci-lint run
make tidy         # go mod tidy && go mod verify
make clean        # remove build artifacts
```

### Project layout

```
projector/
├── cmd/projector/main.go       # entry point; sets version via -ldflags
├── internal/
│   ├── cli/                    # all cobra command definitions
│   │   ├── root.go             # root command, global --pwd flag, shorthand dispatch
│   │   ├── run.go              # `projector run` + shared runCommand logic
│   │   ├── list.go             # `projector list`
│   │   ├── project.go          # `projector project add/remove/list`
│   │   ├── config_cmd.go       # `projector config edit/set/remove/show`
│   │   ├── completion.go       # `projector completion`
│   │   └── context.go          # SIGINT/SIGTERM cancellation context
│   ├── config/
│   │   ├── types.go            # Config, Command, Project, MergedConfig types
│   │   ├── file.go             # LoadFile / SaveFile / GlobalConfigPath
│   │   └── loader.go           # hierarchical load + merge (global → ancestors → cwd)
│   ├── project/
│   │   └── registry.go         # add/remove/get/list projects in global config
│   ├── runner/
│   │   ├── runner.go           # Run, RunConcurrent, RunWithDeps, RunConcurrentWithDeps
│   │   ├── deps.go             # topological sort + cycle detection for depends_on
│   │   └── output.go           # thread-safe prefixed + colored output writer
│   └── editor/
│       └── editor.go           # $VISUAL / $EDITOR / fallback resolution
├── .goreleaser.yaml
├── Makefile
├── go.mod
└── go.sum
```

### Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/spf13/cobra` | CLI framework |
| `gopkg.in/yaml.v3` | YAML parsing and serialization |
| `golang.org/x/sync/errgroup` | Concurrent execution with error propagation |
| `github.com/fatih/color` | Colored terminal output |

## License

GNU General Public License
