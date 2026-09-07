# Cassie architecture

Cassie uses domain-driven boundaries and CQRS. It does not use event sourcing.

## Boundaries

```text
cmd/cassie
  → internal/ui
  → internal/application
  → internal/domain + internal/ports
  ← internal/adapters
```

| Package | Responsibility |
| --- | --- |
| `cmd/cassie` | Connect dependencies. |
| `internal/ui` | Provide the Cobra CLI and Charm TUI. |
| `internal/application` | Coordinate commands and queries. |
| `internal/domain` | Define catalog and runtime behavior. |
| `internal/ports` | Define external capability contracts. |
| `internal/adapters` | Connect Git, YAML, processes, SQLite, Infisical, Portless, Compose, and certificates. |

Domain and application code do not depend on concrete infrastructure. Adapters
implement the interfaces in `internal/ports`.

## Run lifecycle

```text
validate app
  → load secrets
  → prepare Compose files
  → start session
  → create route
  → run commands in order
  → run cleanup in reverse order
  → remove temporary resources
  → finish session
```

The application handler owns this sequence. Adapters perform the external work.
Command handlers change state. Query handlers read configuration and sessions.

For a dynamic port, Portless wraps the last command and supplies `PORT`. For a
fixed port, Cassie creates and later removes an explicit route.

## Configuration and trust

The resolver applies sources in this order:

```text
inference
  → user defaults
  → workspace defaults and app
  → matched user app
  → repository .cassie.yaml
```

Each source overrides the sources before it. Maps merge by key. Scalars and
lists replace earlier values. `null` clears a value.

User apps match a normalized Git remote, a monorepo directory, or an absolute
path. SSH and HTTPS remotes resolve to the same identity.

Cassie hashes the repository `commands`, `cleanup`, `secrets`, and `compose`
fields. The user must trust a new hash before Cassie runs it. User configuration
does not require repository trust.

## Runtime data

| Default path | Contents |
| --- | --- |
| `~/.config/cassie` | User configuration. |
| `~/.local/share/cassie` | Tools, platform files, CAs, and backups. |
| `~/.local/state/cassie` | SQLite state and temporary runtime files. |

`CASSIE_CONFIG`, `CASSIE_DATA`, and `CASSIE_STATE` override these paths.

## Tests

Test observable behavior at application and adapter boundaries. Cover config
resolution, process order, cleanup, route lifecycle, trust, sessions, secret
isolation, platform versions, backup, and restore. Keep Docker integration tests
separate from the fast suite.
