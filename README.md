# Memento

Memento is a self-hosted portal for privately publishing selected photos and videos from one Curator's existing Immich library to family Recipients. Immich remains the media source. Memento owns People, Events, Audiences, Publications, Recipient access, interactions, and notifications.

The repository now includes the deployable application foundation: a React PWA shell, a Go API and in-process worker, PostgreSQL migrations, Caddy, and one production image. Product workflows are delivered in later implementation phases. See [the product and architecture specification](docs/product-architecture-spec.md) and [canonical domain language](CONTEXT.md).

## Deployment topology

Memento is designed for one household, one application instance, PostgreSQL, and an existing Immich v3.0.3 instance. The production image contains Caddy, the built frontend, the Go API, and the in-process PostgreSQL-backed worker.

Memento does not read Immich's PostgreSQL data. It connects to Immich only through a least-privilege server-side API key and the configured `MEMENTO_IMMICH_URL`.

## Operator prerequisites

The current foundation requires:

- an existing Immich v3.0.3 instance, which is the exact version supported by this release;
- PostgreSQL with permission to create a role, database, and extensions;
- the `unaccent` and `pg_trgm` extension files installed on that PostgreSQL server;
- an Immich API key limited to the permissions documented in the specification;
- HTTPS for public access;
- a backup location outside the PostgreSQL container.

Later MVP phases will also require generic SMTP credentials for email and HTTPS-capable devices for Web Push. Supporting a later Immich release requires a future Memento release that updates the hardcoded version pin after its connector contract suite passes.

The PostgreSQL image recommended by Immich v3.0.3 already contains `unaccent` and `pg_trgm`, but extensions must be created separately inside each logical database.

The current foundation does not yet expose first-time browser setup or Recipient content. When setup is delivered, complete it before public exposure. Setup will have no CLI token, and its first successful transaction will create the first Person with Curator and Recipient roles and then disable setup.

## Developer prerequisites

[Mise](https://mise.jdx.dev/) is the source of truth for development tool versions and project tasks. `mise.toml` pins Go 1.26.5, Node.js 24.18.0, pnpm 11.16.0, Air 1.64.2, and golangci-lint 2.12.2. Tygo 0.2.21 remains pinned as a Go tool in `go.mod`, and deployment files pin all container base tags.

Install mise and Docker with the Compose plugin, then install the pinned tools, project dependencies, and generated API types:

```sh
mise install
mise run setup
```

List the available development tasks with `mise tasks ls`. Common commands include:

```sh
mise run start
mise run format
mise run lint
mise run lint:js
mise run test
mise run build
mise run types:generate
```

`mise start` runs three foreground tasks together: the root Docker Compose development dependencies, the Go API through Air, and the Vite frontend. Compose starts PostgreSQL on port 54320 and an Immich stub on port 3001 by default. Air waits for both dependencies, supplies development-only runtime configuration, rebuilds the API, and regenerates Tygo types when Go files change. Vite starts after the API becomes live.

Stopping `mise start` stops the Compose services while preserving PostgreSQL data in its named volume. Run `docker compose down --volumes` to reset that data. `MEMENTO_DEV_POSTGRES_PORT` and `MEMENTO_DEV_IMMICH_PORT` override the dependency ports. The individual `mise start:deps`, `mise start:air`, and `mise start:web` tasks remain available when only part of the environment is needed.

Validate changes before pushing with the fast local gate:

```sh
mise check
```

`mise check` is safe to run concurrently from multiple worktrees. It generates API types, runs Go and frontend linters and unit tests, and builds the frontend. `mise lint` runs golangci-lint, while `mise lint:js` runs ESLint, Prettier, and TypeScript checks in parallel.

Run the complete suite used by CI when needed:

```sh
mise ci
```

`mise ci` includes `mise check`, then adds Go race detection, isolated PostgreSQL integration tests, development Compose validation, Caddy validation, and the production topology test. Docker-backed tests use unique names, images, and dynamic local ports so concurrent worktrees do not share test resources.

The integration task provisions an isolated PostgreSQL 17 database and removes it when the tests finish. It does not connect to an existing PostgreSQL server unless `MEMENTO_TEST_DATABASE_URL` is explicitly set. Set that variable to use an explicitly managed integration database instead of the disposable container.

Tygo output under `app/types/generated/` is gitignored. Mise generates it from Go before every frontend task that consumes it, so contributors never need to commit regenerated files with a PR. The production Docker build also generates its own copy instead of depending on the local working tree.

Individual checks are available through names such as `mise lint:eslint`, `mise lint:prettier`, `mise lint:types`, `mise types:generate`, `mise test:integration`, `mise compose:validate`, `mise caddy:validate`, and `mise test:production`. Docker-backed test harnesses live under `tests/`.

## Provision PostgreSQL beside Immich

Memento may use the same PostgreSQL server or container as Immich, but it requires a separate logical database and separate login role.

> **Never point the Memento runtime at Immich's logical database or configure it with Immich's database role. Memento must never access Immich tables.**
>
> The administrative examples below use Immich's `DB_USERNAME` only to provision, back up, or restore the separate Memento database. Those credentials MUST NOT become Memento runtime configuration.

The examples below use placeholders. Replace every value in angle brackets. Use a new, randomly generated password and do not paste a real secret into shell history, source control, issue comments, or logs. If a password is placed in a URL, percent-encode URL-reserved characters.

These commands require a PostgreSQL cluster superuser because they create a login role, assign database ownership, install extensions, terminate connections, and read every table during backup. Immich's recommended container initializes `DB_USERNAME` as its PostgreSQL superuser unless the operator has deliberately hardened or changed that arrangement. Inspect the deployment rather than assuming the role is named `postgres`; when `DB_USERNAME` is not a cluster superuser, use a separate database-administrator account for these commands. Never give that administrator credential to the Memento runtime.

### Provision with psql

Connect to the administrative `postgres` database as the PostgreSQL administrator:

```sh
psql -h <POSTGRES_HOST> -p 5432 -U <IMMICH_DB_USERNAME> -d postgres
```

Run these commands in `psql`. `CREATE DATABASE` must not be wrapped in a transaction. `\password` prompts without putting the password in SQL text or `psql` history.

```sql
CREATE ROLE memento_app
  WITH LOGIN
  NOSUPERUSER
  NOCREATEDB
  NOCREATEROLE
  NOINHERIT;

\password memento_app

CREATE DATABASE memento
  WITH OWNER = memento_app
  ENCODING = 'UTF8'
  TEMPLATE = template0;

REVOKE ALL ON DATABASE memento FROM PUBLIC;
GRANT CONNECT, TEMPORARY ON DATABASE memento TO memento_app;

\connect memento

REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE, CREATE ON SCHEMA public TO memento_app;

CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
```

Use different role and database names if desired, then apply the same names consistently to `MEMENTO_DATABASE_URL`, `MEMENTO_DATABASE_NAME`, and backup commands.

### Provision through the Immich PostgreSQL container

If the PostgreSQL container is named `immich_postgres`, open `psql` inside it:

```sh
docker exec -it immich_postgres \
  psql -U '<IMMICH_DB_USERNAME>' -d postgres
```

Then run the SQL block above. The value for `<IMMICH_DB_USERNAME>` comes from Immich's `DB_USERNAME` and may not be `postgres`.

### Application connection string

The runtime configuration uses a PostgreSQL URL shaped like:

```text
MEMENTO_DATABASE_URL=postgresql://memento_app:<URL_ENCODED_MEMENTO_DB_PASSWORD>@immich_postgres:5432/memento?sslmode=disable
```

`sslmode=disable` is appropriate only for a trusted private container network without TLS. Select an appropriate PostgreSQL TLS mode when traffic crosses an untrusted network. Startup verifies that the connected logical database has the configured `MEMENTO_DATABASE_NAME` before applying migrations.

## Runtime configuration

Configuration precedence is built-in defaults, an optional YAML file, environment variables, then container secret files. [`deploy/memento.example.yaml`](deploy/memento.example.yaml) documents every non-secret setting. Set `MEMENTO_CONFIG_FILE` to load it.

Required settings are:

- `MEMENTO_DATABASE_URL` or `MEMENTO_DATABASE_URL_FILE`
- `MEMENTO_IMMICH_URL`
- `MEMENTO_IMMICH_API_KEY` or `MEMENTO_IMMICH_API_KEY_FILE`

Environment names for YAML fields use the `MEMENTO_` prefix and underscores, such as `MEMENTO_HTTP_SHUTDOWN_TIMEOUT` and `MEMENTO_WORKER_LEASE_DURATION`. Secret file values override direct environment values and surrounding whitespace is removed. Never put real credentials in the YAML example, image, logs, or health output.

Build the one-image production topology with an explicit application tag:

```sh
docker build --tag memento:0.1.0 .
```

Caddy listens on port 8080 by default, serves the frontend with SPA fallback, and proxies `/api/*` to the Go process on loopback. Set `MEMENTO_SITE_ADDRESS` to a Caddy site address for direct TLS exposure. The container health check calls only `/api/health/live`; use `/api/health/ready` for traffic readiness.

### Private Docker network

Attaching the Memento container to the same private Docker network as Immich is recommended, but not required. On a shared network:

- use the PostgreSQL container name, such as `immich_postgres`, as the database host;
- set `MEMENTO_IMMICH_URL` to Immich's private service URL and port;
- do not publish PostgreSQL to the public internet;
- expose only Memento's HTTP endpoint through the chosen reverse proxy.

If Memento is on another network or host, `MEMENTO_IMMICH_URL` remains configurable. Protect both PostgreSQL and Immich transport appropriately.

## Database backup and restore

Memento recommends a daily PostgreSQL backup for a 24-hour recovery point objective and recovery within several hours. Memento will not schedule backups. The operator owns scheduling, retention, encryption, off-host storage, monitoring, and restore drills.

The Memento logical database is the only database included in these commands. Immich requires its own independent backup plan.

Create a custom-format backup outside the container. Write to a private temporary file, validate the archive directory, and rename it only after success so a failed retry cannot truncate or masquerade as a completed backup:

```sh
#!/bin/sh
set -eu
umask 077

stamp=$(date -u +%Y%m%dT%H%M%SZ)
final="memento-${stamp}.dump"
[ ! -e "$final" ] || { echo "Backup already exists: $final" >&2; exit 1; }
tmp=$(mktemp "${final}.tmp.XXXXXX")
trap 'rm -f "$tmp"' 0 1 2 3 15

docker exec immich_postgres \
  pg_dump -U '<IMMICH_DB_USERNAME>' \
  --dbname=memento \
  --format=custom \
  --no-owner \
  --no-acl \
  > "$tmp"

[ -s "$tmp" ]
docker exec -i immich_postgres pg_restore --list < "$tmp" >/dev/null
mv "$tmp" "$final"
trap - 0 1 2 3 15
printf 'Created %s\n' "$final"
```

A restore MUST first prove the archive can restore into a separate database. Keep the working `memento` database unchanged until that restore succeeds. Stop the Memento application, connect to the administrative `postgres` database, and create a temporary restore database:

```sql
CREATE DATABASE memento_restore
  WITH OWNER = memento_app
  ENCODING = 'UTF8'
  TEMPLATE = template0;

REVOKE ALL ON DATABASE memento_restore FROM PUBLIC;
GRANT CONNECT, TEMPORARY ON DATABASE memento_restore TO memento_app;

\connect memento_restore

REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE, CREATE ON SCHEMA public TO memento_app;

CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
```

Restore the archive as `memento_app`. The extensions already exist and comments are skipped because the application role does not own those administrator-created extensions:

```sh
docker exec -i immich_postgres \
  pg_restore -U memento_app \
  --dbname=memento_restore \
  --no-owner \
  --no-acl \
  --no-comments \
  --single-transaction \
  --exit-on-error \
  < memento-YYYYMMDDTHHMMSSZ.dump
```

The Immich container normally permits local socket authentication for this command. If the installation requires a password, use a protected PostgreSQL password file or container secret. Do not place `PGPASSWORD` or the password itself in the shell command.

Read-only restore validation and Recovery hold are not part of the current foundation. Do not cut a restored database into a production deployment until those safeguards are implemented. The remaining commands document the intended later-phase cutover and must not be treated as a complete recovery procedure today. After `pg_restore` succeeds and a future release provides restore validation, validate `memento_restore` and preserve the old database during cutover. From the administrative `postgres` database, terminate only Memento connections and use a unique suffix in place of `<RESTORE_TIMESTAMP>`:

```sql
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE datname IN ('memento', 'memento_restore')
  AND pid <> pg_backend_pid();

ALTER DATABASE memento RENAME TO memento_pre_restore_<RESTORE_TIMESTAMP>;
ALTER DATABASE memento_restore RENAME TO memento;
```

If the second rename fails, the original database still exists under the `memento_pre_restore_<RESTORE_TIMESTAMP>` name and can be renamed back. Retain that database until recovery validation and Curator review succeed; remove it later through an explicit operator decision.

The completed application will require `MEMENTO_RECOVERY_NONCE=<fresh-random-value>` before starting a restored database. That later-phase Recovery hold will rotate the security epoch, invalidate restored Sessions and linked push subscriptions, and block Recipient access and optional delivery until Curator review. The current foundation does not accept or enforce this setting, so it must not be used to claim that a restored production deployment is safe.

A database restore does not restore Immich Media, SMTP credentials, the Immich API key, VAPID private keys, configuration files, or an optional local GeoIP database. Back up those through their own secure operator procedures.

## License

Memento is licensed under the [MIT License](LICENSE).
