# Agent guide

This file applies to the whole repository. Read [README.md](README.md) for user
behavior and [docs/architecture.md](docs/architecture.md) for system boundaries.

## Project

Cassie is a Go CLI and TUI for local development. It runs existing application
commands with secrets from a self-hosted Infisical instance and HTTPS routes
from Portless.

- Module: `github.com/andocodes/cassie`
- Go version: 1.25
- CLI: Cobra
- TUI: Charmbracelet Bubble Tea, Bubbles, Huh, and Lip Gloss
- State: SQLite
- Configuration: YAML

## Repository map

```text
cmd/cassie/                 composition root
internal/domain/            catalog and runtime rules
internal/application/       command and query handlers
internal/ports/             infrastructure contracts
internal/adapters/          external system implementations
internal/ui/cli/            Cobra commands
internal/ui/tui/            interactive dashboard
docs/                       maintainer documentation
scripts/                    installation scripts
```

Put business rules in `internal/domain`. Put use-case sequencing in
`internal/application`. Define external capabilities in `internal/ports`, then
implement them in `internal/adapters`. Keep Cobra and Bubble Tea concerns in
`internal/ui`.

## Commands

Run these checks before handoff:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/cassie
```

Use these commands during development:

```bash
go run ./cmd/cassie --help
go run ./cmd/cassie <command> --help
gofmt -w <changed-go-files>
```

Run focused tests while editing, then run the full checks. Add behavior tests
for changed behavior. Do not add tests that only mirror implementation details.

## Architecture rules

- Use CQRS without event sourcing.
- Keep domain and application packages independent of concrete infrastructure.
- Inject external behavior through interfaces in `internal/ports`.
- Keep command handlers responsible for state changes and query handlers
  responsible for reads.
- Preserve cancellation, exit codes, cleanup, and persisted session state.
- Keep daemon requests free of secret values. Start the complete application
  lifecycle when work must survive a TUI disconnect.
- Prefer small adapters and explicit data flow over shared mutable state.

## Behavior invariants

Preserve these contracts unless the task explicitly changes them:

- Configuration precedence is CLI flags, repository `.cassie.yaml`, matched
  user app, workspace `.cassie.yaml`, user defaults, then inference.
- Maps merge by key. Scalars and lists replace inherited values. YAML `null`
  clears inherited values.
- Commands run sequentially in separate shells with one injected environment.
- The last command owns the foreground process.
- Cleanup runs in reverse order after success, failure, or interruption.
- A dynamic-port run lets Portless supply `PORT` to the last command.
- A fixed-port run creates an alias and removes it during cleanup.
- Compose secrets go only to named services through temporary mode `0600`
  files outside the repository.
- Repository `commands`, `cleanup`, `secrets`, and `compose` fields require
  trust. User configuration does not.
- `cassie down` and `cassie prune` do not delete application or platform
  volumes.
- Restore creates a safety backup before it replaces the Infisical database.
- System CA installation and Docker Desktop restart remain explicit actions.

Never print, persist, or add secrets to diagnostics. Application output passes
through unchanged, so do not claim that Cassie can redact secrets printed by a
child process.

## Change guide

| Change | Start here |
| --- | --- |
| Configuration or discovery | `internal/adapters/config` |
| App validation or command schema | `internal/domain/catalog` |
| Run lifecycle | `internal/application/command/run_application.go` |
| Process and signal behavior | `internal/adapters/process` |
| Infisical access | `internal/adapters/secrets` |
| Portless or managed platform | `internal/adapters/platform` |
| Compose secret injection | `internal/adapters/compose` |
| Sessions or trust storage | `internal/adapters/sqlite` |
| CLI commands | `internal/ui/cli/root.go` |
| Dashboard | `internal/ui/tui` |
| Release packaging | `.goreleaser.yaml`, `.github/workflows` |

When a CLI contract changes, update command help, README examples, and tests in
the same change. When a configuration field changes, update decoding, merging,
validation, examples, and precedence tests.

## Style

- Follow standard Go conventions and run `gofmt`.
- Return errors with operation context. Preserve wrapped errors with `%w`.
- Prefer table-driven tests for input variants and behavior-focused tests for
  workflows.
- Avoid new dependencies unless they remove more complexity than they add.
- Keep documentation concise. Use active voice, direct headings, and verified
  examples.
- Do not commit, tag, push, or publish unless the user explicitly requests it.
