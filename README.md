# Memento

Memento is a planned self-hosted portal for privately publishing selected photos and videos from one Curator's existing Immich library to family Recipients. Immich remains the media source. Memento owns People, Events, Audiences, Publications, recipient access, interactions, and notifications.

The implementation is not present yet. This repository currently contains product, architecture, and integration planning. See [the product and architecture specification](docs/product-architecture-spec.md) and [canonical domain language](CONTEXT.md).

## Planned deployment

Memento is designed for one household, one application instance, PostgreSQL, and an existing Immich v3.0.3 instance. The planned production image contains Caddy, the frontend, the Go API, and the in-process PostgreSQL-backed worker.

Memento does not read Immich's PostgreSQL data. It connects to Immich only through a least-privilege server-side API key and the configured `IMMICH_URL`.

## Operator prerequisites

Before deploying the future application, an operator will need:

- an existing Immich v3.0.3 instance, or a later release that has passed Memento's contract tests;
- PostgreSQL with permission to create a role, database, and extensions;
- the `unaccent` and `pg_trgm` extension files installed on that PostgreSQL server;
- generic SMTP credentials for email;
- HTTPS for public access and Web Push;
- an Immich API key limited to the permissions documented in the specification;
- a backup location outside the PostgreSQL container.

The PostgreSQL image recommended by Immich v3.0.3 already contains `unaccent` and `pg_trgm`, but extensions must be created separately inside each logical database.

Complete Memento's first-time browser setup before exposing it publicly. Setup has no CLI token. The first successful setup transaction creates the first Person with both Curator and Recipient roles and then disables setup.

## Developer prerequisites

Exact tool versions and development commands will be added with the implementation. The planned stack requires:

- Go;
- Node.js and pnpm;
- PostgreSQL;
- Docker or another container runtime for integration tests;
- an Immich v3.0.3 test instance for connector contract tests.

Bootstrap must select current stable dependencies and then pin exact versions and lockfiles. Builds and images must not use floating `latest` tags.

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

Use different role and database names if desired, then apply the same names consistently to `DATABASE_URL` and backup commands.

### Provision through the Immich PostgreSQL container

If the PostgreSQL container is named `immich_postgres`, open `psql` inside it:

```sh
docker exec -it immich_postgres \
  psql -U '<IMMICH_DB_USERNAME>' -d postgres
```

Then run the SQL block above. The value for `<IMMICH_DB_USERNAME>` comes from Immich's `DB_USERNAME` and may not be `postgres`.

### Application connection string

The planned configuration uses a PostgreSQL URL shaped like:

```text
DATABASE_URL=postgresql://memento_app:<URL_ENCODED_MEMENTO_DB_PASSWORD>@immich_postgres:5432/memento?sslmode=disable
```

`sslmode=disable` is appropriate only for a trusted private container network without TLS. Select an appropriate PostgreSQL TLS mode when traffic crosses an untrusted network.

### Private Docker network

Attaching the future Memento container to the same private Docker network as Immich is recommended, but not required. On a shared network:

- use the PostgreSQL container name, such as `immich_postgres`, as the database host;
- set `IMMICH_URL` to Immich's private service URL and port;
- do not publish PostgreSQL to the public internet;
- expose only Memento's HTTP endpoint through the chosen reverse proxy.

If Memento is on another network or host, `IMMICH_URL` remains configurable. Protect both PostgreSQL and Immich transport appropriately.

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

After `pg_restore` succeeds, run the future application's documented restore validation against `memento_restore`. Preserve the old database during cutover. From the administrative `postgres` database, terminate only Memento connections and use a unique suffix in place of `<RESTORE_TIMESTAMP>`:

```sql
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE datname IN ('memento', 'memento_restore')
  AND pid <> pg_backend_pid();

ALTER DATABASE memento RENAME TO memento_pre_restore_<RESTORE_TIMESTAMP>;
ALTER DATABASE memento_restore RENAME TO memento;
```

If the second rename fails, the original database still exists under the `memento_pre_restore_<RESTORE_TIMESTAMP>` name and can be renamed back. Retain that database until recovery validation and Curator review succeed; remove it later through an explicit operator decision.

Restoring an older database can resurrect Sessions and authorization state that changed after the backup. Before the first restored start, generate a fresh random value that has never appeared in a configuration backup and set `MEMENTO_RECOVERY_NONCE=<fresh-random-value>`. Before serving non-liveness traffic or starting workers, Memento persists Recovery hold and rotates its security epoch. The fresh nonce forces rotation even when the restored snapshot was captured during an earlier Recovery hold. After startup records the hold, remove the nonce and restart normally. The hold invalidates restored Sessions and linked push subscriptions and blocks Recipient access and optional delivery until the Curator signs in with a fresh email code, reviews restored Recipient access, Audiences, Withdrawals, and Publications, and explicitly lifts it. A normal restart cannot clear the hold.

A database restore does not restore Immich Media, SMTP credentials, the Immich API key, VAPID private keys, configuration files, or an optional local GeoIP database. Back up those through their own secure operator procedures.

## License

The license decision is MIT. A `LICENSE` file has not been added yet.
