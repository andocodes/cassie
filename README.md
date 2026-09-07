# Cassie

Cassie runs local applications with Infisical secrets and stable Portless HTTPS
URLs. It uses each application's existing development commands.

## Requirements

- Docker with Docker Compose
- Node.js 24 or later

## Install

On macOS or Linux, install the latest release to `~/.local/bin`:

```bash
curl -fsSL https://raw.githubusercontent.com/andocodes/cassie/main/scripts/install.sh | sh
```

Set `CASSIE_INSTALL_DIR` to use another directory. Windows builds are available
on the [Releases](https://github.com/andocodes/cassie/releases) page.

## Quick start

```bash
cassie up
cd path/to/app
cassie link
cassie run
```

`cassie up` starts the local platform and handles Infisical login. `cassie link`
stores app configuration in `~/.config/cassie/config.yaml`. It does not change
the repository. Use `cassie link --repo` to create a shared `.cassie.yaml`.

Run `cassie` without a subcommand to open the interactive dashboard.

## Configuration

Use the user configuration when you do not want Cassie files in a repository:

```yaml
version: 1

defaults:
  grace: 10s
  secrets:
    environment: dev
    path: /

workspace:
  depth: 2
  ignore: [archive]
  groups:
    atlas: [atlas-api, atlas-ui]

apps:
  atlas-ui:
    match:
      repo: github.com/acme/atlas-ui
    domain: atlas-ui
    commands:
      - bun run generate
      - bun run dev
    cleanup:
      - docker compose down
    secrets:
      project: atlas
```

Match apps by Git remote with `match.repo`. Add `match.dir` for a monorepo. Use
an absolute `match.path` for a directory that is not a Git repository.

A repository `.cassie.yaml` uses the same app fields without `match`:

```yaml
version: 1
name: atlas-ui
domain: atlas-ui
port: 3000

commands:
  - bun run generate
  - run: docker compose up
    dir: infra

secrets:
  project: atlas
  environment: dev
  path: /

compose:
  services: [web, worker]
```

Set `domain` without `.localhost`. `atlas-ui` becomes
`https://atlas-ui.localhost`.

### Precedence

The first defined value wins:

```text
CLI flags
  → repository .cassie.yaml
  → matched user app
  → workspace .cassie.yaml
  → user defaults
  → inference
```

Maps merge by key. Scalars and lists replace inherited values. `null` clears an
inherited value. Run `cassie config show` to inspect the result and its sources.

### Runtime behavior

```text
configuration → secrets → HTTPS route → commands → cleanup
```

- Commands run in order and in separate shells with the same environment.
- The last command owns the foreground process.
- Cleanup runs in reverse order after success, failure, or interruption.
- `dir` is relative to the app root.
- Without `port`, Portless supplies `PORT` to the last command.
- With `port`, Cassie routes the configured hostname to that fixed port.

Keep concurrent process management in Compose, Turbo, `concurrently`, or the
application's existing scripts.

Set `secrets.project` to load secrets into each command's environment. For
Compose, only services in `compose.services` receive secrets. Cassie uses
temporary mode `0600` files outside the repository and removes them after the
run.

## Workspaces

From a directory that contains several repositories, run:

```bash
cassie run --all
cassie run --group atlas
```

Cassie scans to `workspace.depth`. It runs selected apps concurrently and
prefixes their output with the app name. A failure stops the other apps.

## Platform and trust

`cassie up` installs pinned Portless and Infisical CLI versions in Cassie's data
directory. It runs Infisical Server, PostgreSQL, and Redis through Docker
Compose at `https://infisical.localhost`. `cassie down` preserves the volumes.

Repository commands, cleanup, secrets, and Compose settings require trust. A
change to those fields requires new trust. User-owned app configuration does
not.

Use `cassie ca` for corporate CAs. Use `cassie backup` and `cassie restore` to
protect the Infisical database and encryption keys. Restore creates a safety
backup first.

## Commands

| Command | Purpose |
| --- | --- |
| `cassie` | Open the dashboard. |
| `cassie up`, `cassie down` | Start or stop the local platform. |
| `cassie link` | Configure the current app. |
| `cassie run [app]` | Run one app. |
| `cassie run --all`, `cassie run --group <name>` | Run workspace apps. |
| `cassie status`, `cassie logs [-f]` | Inspect sessions or platform logs. |
| `cassie doctor` | Check local dependencies. |
| `cassie trust [app]` | Trust executable repository configuration. |
| `cassie prune` | Remove stale Portless routes. |
| `cassie ca` | Manage corporate CAs. |
| `cassie backup`, `cassie restore` | Back up or restore Infisical. |
| `cassie config show [app]` | Show effective configuration and sources. |

Run `cassie <command> --help` for full usage.

## Develop

Cassie requires Go 1.25 for development.

```bash
go test ./...
go run ./cmd/cassie --help
```

See [Cassie architecture](docs/architecture.md) for package boundaries and
testing guidance.
