# Changelog

All notable changes to projector are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

Releases take their notes from the matching `## [vX.Y.Z]` section below, so
add a section before pushing a tag.

## [v0.1.0] - 2026-10-09

### Added

- **Web dashboard (`projector serve`)**: a local dashboard to see and drive all
  registered projects.
  - Project status: git branch, ahead/behind, uncommitted changes, compose
    containers and pending once steps, refreshed every 15s.
  - Run any resolved command with optional extra args, with live output and a
    stop button, so long-running commands (`logs`, dev servers) work too.
  - **Dependencies** tab: the `^project:command` graph, filterable by command,
    with container state per project. `/#view=graph` links to it.
  - Desktop notification when a run you started finishes in a background tab.
  - `--open` opens the browser; `--addr` sets the (loopback-only) address.
  - Local only: loopback listener and `Host` check, a per-start token carried
    in the URL fragment, bearer-only API (no cookies), strict
    Content-Security-Policy, and extra args passed as literal words.
- **`projector status`**: a table of git, container and once-step state for
  all or the named projects.
- **Once steps** (`once:`): per-command steps that run after the command at
  most once per machine, for one-off actions after a pull (e.g. a backfill).
  Recorded under `$XDG_STATE_HOME/projector`; subdirectories, symlinks, project
  names and aliases share one record.
- **`exclude:`**: drop inherited or service-generated commands (and their
  aliases) from a project.
- **`info:`**: named read-only commands whose output the dashboard shows per
  project (pending migrations, recent commits...). Merged by name across config
  files; an item with no `cmd` removes an inherited one. Each runs for at most
  30s and keeps at most 256 KB of output.

### Fixed

- Cross-project dependencies reachable through several paths run once per
  invocation. Before, `webshop → core-api` ran shared dependencies such as
  `core` and `traefik` several times.

### Changed

- The run, cross-project dependency and once-step logic moved from
  `internal/cli` to a new `internal/app` layer shared by the CLI and the
  dashboard.
- New `runner.WithProcessGroup` option: stopping a command terminates its whole
  process tree (SIGTERM, then SIGKILL after 5s). Used by the dashboard, not by
  the CLI.
- `make test-js` runs the dashboard's graph layout tests in Node; CI runs them.
- Releases use this file for their notes.

## [v0.0.2] - 2026-04-01

### Added

- Command sequences: `cmd` accepts a list of commands run in order, stopping at
  the first failure.

## [v0.0.1] - 2026-03-24

### Added

- Named commands per project, resolved from the global config, ancestor
  directories and the current directory.
- Running a command in several registered projects concurrently, with
  prefixed output, `--filter` glob patterns and `--dry-run`.
- `depends_on`, including cross-project dependencies (`^project:command`).
- `services` to generate commands from a shared exec prefix.
- CI workflows and goreleaser release builds.

[v0.1.0]: https://github.com/akyrey/projector/compare/v0.0.2...v0.1.0
[v0.0.2]: https://github.com/akyrey/projector/compare/v0.0.1...v0.0.2
[v0.0.1]: https://github.com/akyrey/projector/releases/tag/v0.0.1
