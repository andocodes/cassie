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
cassie up # one-time interactive setup
cd path/to/app
cassie link
cassie
```

`cassie up` installs the managed tools, starts Portless and Infisical, and sets
up a fresh Infisical instance without a browser. Portless reuses its local CA
but requests elevation each time it starts on port 443. Cassie does not open
Docker Desktop, OrbStack, or Colima.

For unattended first-time setup, provide Infisical's bootstrap variables:

```bash
export INFISICAL_ADMIN_EMAIL=you@example.com
export INFISICAL_ADMIN_PASSWORD='use-a-password-manager'
export INFISICAL_ADMIN_ORGANIZATION=Personal
cassie up
unset INFISICAL_ADMIN_PASSWORD
```

Without these variables, `cassie up` asks for the same values in the terminal.
It sends the password only to Infisical and lets the Infisical CLI store the
resulting session in the system credential vault.

`cassie link` stores app configuration for the current checkout in
`~/.config/cassie/config.yaml`. It does not change the repository. Use
`cassie link --repo` to create a shared `.cassie.yaml`.

Run `cassie` without a subcommand to open the workspace dashboard. Use `r` to
run an app, `s` to stop it, `o` to open its URL, and `/` to filter. Apps keep
running when the dashboard closes. Reopen Cassie to reconnect to their logs.

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
    product: [api, web]

apps:
  web:
    match:
      repo: github.com/example/web
    domain: web
    commands:
      - bun run generate
      - bun run dev
    cleanup:
      - docker compose down
    secrets:
      project: web
```

Match apps by Git remote with `match.repo`. Add `match.dir` for a monorepo. Use
an absolute `match.path` for a directory that is not a Git repository.

A repository `.cassie.yaml` uses the same app fields without `match`:

```yaml
version: 1
name: web
domain: web
port: 3000

commands:
  - bun run generate
  - run: docker compose up
    dir: infra

secrets:
  project: web
  environment: dev
  path: /

compose:
  services: [web, worker]
```

Set `domain` without `.localhost`. `web` becomes
`https://web.localhost`.

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
cassie run --group product
```

Cassie scans to `workspace.depth`. It runs selected apps concurrently and
prefixes their output with the app name. A failure stops the other apps.

The dashboard shows linked apps first. Press `d` to inspect other detected
repositories, then press Enter to choose an app name and link one. Cassie
caches the workspace index in SQLite and refreshes it in the background, so
large workspaces open without waiting for a full scan.

## Platform and trust

`cassie up` installs pinned Portless and Infisical CLI versions in Cassie's data
directory. It starts the Portless HTTPS proxy and runs Infisical Server,
PostgreSQL, and Redis through Docker Compose at
`https://infisical.localhost`. `cassie down` stops all of them and preserves
their local data.

The dashboard uses a per-user local daemon for application processes. The
daemon stores process metadata and bounded logs in Cassie's state directory.
It does not store secret values. Closing the dashboard does not stop apps.

`cassie doctor` checks local prerequisites. In an interactive terminal, it
offers to install missing managed tools. Use `cassie doctor --fix` in scripts.
Different tool versions produce warnings and do not fail the check.

Repository commands, cleanup, secrets, and Compose settings require trust. A
change to those fields requires new trust. User-owned app configuration does
not.

Use `cassie ca` for corporate CAs. Use `cassie backup` and `cassie restore` to
protect the Infisical database and encryption keys. Restore creates a safety
backup first.

## Commands

| Command | Purpose |
| --- | --- |
| `cassie` | Open the dashboard and reconnect to managed apps. |
| `cassie up`, `cassie down` | Start or stop the local platform. |
| `cassie link` | Configure the current app. |
| `cassie run [app]` | Run one app. |
| `cassie run --all`, `cassie run --group <name>` | Run workspace apps. |
| `cassie status`, `cassie logs [-f]` | Inspect sessions or platform logs. |
| `cassie doctor [--fix]` | Check dependencies and install missing managed tools. |
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
