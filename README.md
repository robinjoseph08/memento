# Memento

Memento is a self-hosted portal for privately publishing selected photos and videos from one Curator's existing Immich library to family Recipients. Immich remains the media source. Memento owns People, Events, Audiences, Publications, Recipient access, interactions, and notifications.

The repository includes the deployable application foundation, first-browser Curator setup, Source reconciliation, private Event organization, coalesced Staged updates, atomic Publication, the authorized Recipient library, email delivery, and trusted-device Web Push: a React PWA, a Go API and in-process worker, PostgreSQL migrations, Caddy, and one production image. See [the product and architecture specification](docs/product-architecture-spec.md) and [canonical domain language](CONTEXT.md).

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
- generic SMTP access when email delivery is enabled, with credentials when the server requires authentication;
- a backup location outside the PostgreSQL container.

Optional Web Push requires HTTPS-capable devices and outbound HTTPS access to browser push services. Supporting a later Immich release requires a future Memento release that updates the hardcoded version pin after its connector contract suite passes. Source discovery validates that the API key has exactly `album.read`, `asset.read`, `asset.view`, `asset.download`, `person.read`, and `face.read`; missing or additional permissions fail the least-privilege gate.

The PostgreSQL image recommended by Immich v3.0.3 already contains `unaccent` and `pg_trgm`, but extensions must be created separately inside each logical database.

Complete first-browser setup before public exposure. Browser setup requires HTTPS, except on a browser-recognized loopback address such as `localhost`; a private-network HTTP address is not sufficient because the required Secure Session cookie would be discarded. Setup has no CLI token. Its first successful transaction creates the sole Person with Curator and Recipient roles, records completed Onboarding choices, creates an opaque server-side Session, and permanently disables setup.

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

Use the fast local gate while iterating on changes:

```sh
mise check:quiet
```

`mise check:quiet` runs the worktree-safe `mise check` gate once, suppresses successful task output, and prints the original captured output when a task fails. The gate generates API types, runs Go and frontend linters and unit tests, and builds the frontend. `mise lint` runs golangci-lint, while `mise lint:js` runs ESLint, Prettier, and TypeScript checks in parallel.

Run the complete suite used by CI once as the final local gate immediately before pushing:

```sh
mise ci:quiet
```

`mise ci:quiet` similarly runs `mise ci` once and emits its captured output only on failure. The complete gate includes `mise check`, then adds Go race detection, browser tests, isolated PostgreSQL integration tests, the Immich contract, development Compose validation, Caddy validation, and the production topology test. Docker-backed tests isolate concurrent worktrees with unique Compose project or container names, a unique built Memento image tag where applicable, and dynamic local ports. They may share pinned immutable fixture images such as Caddy.

The integration task provisions an isolated PostgreSQL 17 database and removes it when the tests finish. It does not connect to an existing PostgreSQL server unless `MEMENTO_TEST_DATABASE_URL` is explicitly set. Set that variable to use an explicitly managed integration database instead of the disposable container.

Run `mise test:performance` manually to build the deterministic 100,000-Media fixture and measure every baseline target. The target-scale suite is not part of ordinary CI because it is sensitive to shared-runner load. See [`docs/performance.md`](docs/performance.md) for reproducibility and evidence requirements.

Tygo output under `app/types/generated/` is gitignored. Mise generates it from Go before every frontend task that consumes it, so contributors never need to commit regenerated files with a PR. The production Docker build also generates its own copy instead of depending on the local working tree.

Individual checks are available through names such as `mise lint:eslint`, `mise lint:prettier`, `mise lint:types`, `mise types:generate`, `mise test:integration`, `mise compose:validate`, `mise caddy:validate`, and `mise test:production`. Docker-backed test harnesses live under `tests/`. See [`docs/accessibility.md`](docs/accessibility.md) for the automated browser matrix and assistive-technology release checks.

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

- `MEMENTO_HTTP_PUBLIC_URL`, set to the externally reachable HTTPS origin used in personalized Invitation links; HTTP is accepted only for loopback development origins
- `MEMENTO_DATABASE_URL` or `MEMENTO_DATABASE_URL_FILE`
- `MEMENTO_IMMICH_URL`
- `MEMENTO_IMMICH_API_KEY` or `MEMENTO_IMMICH_API_KEY_FILE`
- `MEMENTO_SECURITY_SECRET` or `MEMENTO_SECURITY_SECRET_FILE`, containing at least 32 random bytes and kept stable across restarts

`MEMENTO_RECOVERY_NONCE` or `MEMENTO_RECOVERY_NONCE_FILE` is a one-time restore setting, not routine configuration. It must contain at least 32 random bytes when set. Use a fresh value only for the first application start after replacing the production database with a validated restore, then remove it from the runtime environment.

Environment names for YAML fields use the `MEMENTO_` prefix and underscores, such as `MEMENTO_HTTP_SHUTDOWN_TIMEOUT` and `MEMENTO_WORKER_LEASE_DURATION`. Secret file values override direct environment values and surrounding whitespace is removed. Generate the security secret with a cryptographically secure tool, store it like the database and Immich credentials, and do not rotate it as a routine restart action. Setup bursts default to three code requests per normalized email and twenty setup mutations per client IP in fifteen minutes. Configure them with `MEMENTO_SECURITY_SETUP_EMAIL_LIMIT`, `MEMENTO_SECURITY_SETUP_IP_LIMIT`, and `MEMENTO_SECURITY_SETUP_RATE_WINDOW`. Invitation acceptance defaults to twenty attempts per token and client IP in fifteen minutes; configure it with `MEMENTO_SECURITY_INVITATION_ACCEPT_IP_LIMIT` and `MEMENTO_SECURITY_INVITATION_ACCEPT_RATE_WINDOW`. Passwordless sign-in defaults to three starts per normalized email and ten per client IP in fifteen minutes; configure it with `MEMENTO_SECURITY_SIGN_IN_EMAIL_LIMIT`, `MEMENTO_SECURITY_SIGN_IN_IP_LIMIT`, and `MEMENTO_SECURITY_SIGN_IN_RATE_WINDOW`. `MEMENTO_GEOIP_DATABASE_PATH` may point to a local MaxMind-compatible City database for approximate Session locations; Memento performs no request-time GeoIP network lookup. `MEMENTO_SECURITY_TRUSTED_PROXY_CIDRS` explicitly identifies proxies whose forwarding headers may supply the client IP; it defaults to the loopback networks used by the bundled Caddy process. Source album membership reconciliation defaults to every ten minutes and can be configured with `MEMENTO_SOURCES_RECONCILIATION_INTERVAL`. SMTP is optional until email is needed, but must be enabled before first-browser setup can send its verification code. When enabled, its host, transport mode, sender, test recipient, and optional username are ordinary settings, while its password should use `MEMENTO_SMTP_PASSWORD_FILE`.

Web Push is independently optional. Generate one stable VAPID P-256 key pair with a standards-compatible Web Push tool. Configure `MEMENTO_PUSH_PUBLIC_KEY`, `MEMENTO_PUSH_PRIVATE_KEY_FILE`, and a `mailto:` or HTTPS `MEMENTO_PUSH_SUBJECT`, then set `MEMENTO_PUSH_ENABLED=true`. The public key and private key must be unpadded base64url and must match. Rotating this key pair requires each device to enroll again. Restrictive firewalls must permit the browser vendors' push endpoints, including Apple, Mozilla, and Google services. Memento validates every untrusted endpoint at enrollment and again while connecting, bypasses environment proxies, rejects non-public addresses, and never forwards Memento credentials.

Never put real credentials or private keys in the YAML example, image, logs, or health output.

Build the one-image production topology with an explicit application tag:

```sh
docker build --tag memento:0.1.0 .
```

Caddy listens on port 8080 by default, serves the frontend with SPA fallback, and proxies `/api/*` to the Go process on loopback. Set `MEMENTO_SITE_ADDRESS` to a Caddy site address for direct TLS exposure. When a separate reverse proxy terminates HTTPS in front of the bundled Caddy process, set `MEMENTO_CADDY_TRUSTED_PROXY_CIDRS` to its space-separated CIDR networks. Caddy then preserves the trusted client address and original HTTPS scheme; untrusted forwarding headers remain ignored. The container health check calls only `/api/health/live`; use `/api/health/ready` for traffic readiness.

### Verify required email delivery

Configure the `smtp` section shown in [`deploy/memento.example.yaml`](deploy/memento.example.yaml), then restart Memento. Production delivery accepts `starttls` or `implicit_tls`; both verify the server certificate and hostname. STARTTLS fails rather than downgrading when the extension is unavailable. Authenticated and unauthenticated SMTP are supported. Set both username and password or neither.

While first-time setup remains incomplete and the deployment is still on its controlled endpoint, enqueue the configured test recipient without putting an address or message body in the request:

```sh
curl --fail --request POST https://memento.example/api/setup/email/test
```

The `202 Accepted` response contains only a delivery ID and `queued` status. Poll its safe status until it reports `sent` or `failed`:

```sh
curl --fail https://memento.example/api/setup/email/test/<DELIVERY_ID>
```

The request transaction commits an `email_deliveries` row and outbox event before returning. The in-process worker later leases the outbox event, creates an idempotent job, and attempts SMTP. Temporary failures retry with bounded exponential backoff for at most 24 hours. A permanent synchronous rejection reports an allowlisted failure code and creates an unresolved delivery problem without exposing the recipient, message, credentials, or raw provider response. This required test path does not read optional notification preferences.

SMTP delivery is at least once because provider acceptance cannot be committed atomically with PostgreSQL after a process crash. A stable `Message-ID` and persisted sent state limit observable replay, but cannot guarantee that a provider will suppress every duplicate.

Plaintext SMTP is forbidden by default. `mode: insecure` additionally requires `insecure_development: true`, a literal loopback or private IP endpoint, and no credentials. Startup logs a warning and readiness reports `"smtp":"insecure_development"` while active, including after a delivery failure. This exception is only for a controlled development SMTP fixture, never production.

### Enable Web Push

After the operator configures VAPID, each Recipient can enable push only from the explicit control in Account and family settings on that current trusted device. Memento never requests browser notification permission during page load. iPhone and iPad users must add Memento to the Home Screen and launch the installed PWA first. Android support is determined from browser capabilities and does not require installation.

Each browser subscription is encrypted at rest and linked to its exact trusted Session. Session revocation, expiry, sign out all, suspension, or Recipient access Revocation suppresses later delivery. Immediate push uses the same fifteen-minute authorized Publication and Comment activity windows as immediate email, but device enrollment and delivery outcomes remain independent of email settings. Provider responses 404 and 410 disable only the affected device subscription. Payloads contain only aggregate activity counts, with no thumbnail, Media identity, reusable URL, Comment body, or authorization token.

### Complete first-browser setup

After the required test email succeeds, open Memento over HTTPS or on a loopback address in the first browser. Enter the first Person's name and login email, verify the eight-digit code within ten minutes, review every Onboarding acknowledgment, choose an email preference and browser Session type, then explicitly complete setup within thirty minutes of verification.

A code permits at most five verification attempts and is single-use. Requesting or verifying it does not create a Person or Session. The durable email body containing a setup code is encrypted at rest and erased after terminal delivery. Final completion takes the singleton setup lock and creates the Person, Curator and Recipient roles, completed Recipient access generation, current login email, Onboarding choices, notification preference, initial Session, and safe security audit records in one transaction. A concurrent final request receives a conflict without retaining partial identity or Session records.

The Session credential is an opaque random value stored only as a hash on the server. The browser receives it in a host-prefixed Secure, HttpOnly, SameSite Lax cookie. Public-computer Sessions use a browser-session cookie and expire server-side within twelve hours. Trusted-device Sessions use a persistent cookie, refresh through a CSRF-protected mutation when the application opens, and expire after one year of inactivity. Session mutations require the Session-bound CSRF token returned by the API.

Setup closure is stored in PostgreSQL. Clearing cookies, using another browser, changing configuration, or restarting Memento does not reopen it. There is no CLI override. Safe GET requests only inspect persisted setup or Session state and never create identity or Session records.

### Sign in and manage Recipient access

After setup closes, a Recipient requests an eight-digit passwordless sign-in code. Start responses use the same accepted shape for eligible and unknown email addresses, codes expire after ten minutes with five attempts, and verification creates a new opaque Session only after a successful single-use exchange. Each Recipient can inspect, name, revoke, or sign out all Sessions. Public-computer Sessions display a persistent privacy warning, cannot use push, use a nonpersistent cookie, and expire within twelve hours. Revoking a trusted Session disables its linked push subscription.

A signed-in Recipient changes login email only after proving fresh codes sent to both current and replacement addresses. Curator-assisted recovery proves the replacement mailbox and revokes all of that Recipient's Sessions while preserving Person identity, the current access generation, Onboarding choices, preferences, and interactions. Suspension revokes Sessions but retains the current generation. Revocation ends that generation; explicitly designating and reinviting the same Person creates a new generation. This generation boundary prevents future Audience authorization from matching entries attached to the ended generation.

### Discover and triage Source albums

In the Curator browser Session created during setup, select **Connect and discover** in the Source album workspace. Memento validates Immich v3.0.3 and the exact least-privilege permission set before requesting owned albums. A failed version, permission, authentication, rate-limit, response, or availability check writes no discovery state.

Discovery stores normalized album summaries under Memento identities. It does not create Events, Publications, Audiences, Media delivery routes, or Recipient-visible content. Inspect an unreviewed Source album and select **Ignore Source album** to remove it from the inbox. The **Ignored** view can restore it later while preserving its Memento identity, first-seen time, last-seen time, and source-missing state. Source album discovery does not return Immich IDs, paths, library identifiers, owner details, faces, direct URLs, API keys, or raw DTOs to the browser. The Curator-only repair workspace is the deliberate exception: it displays normalized Immich IDs, paths, and face evidence needed for explicit identity decisions, but never raw DTOs, direct URLs, credentials, or Recipient authorization.

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

After `pg_restore` succeeds, run the validation command from the same Memento release that will serve the restore. Point `MEMENTO_DATABASE_URL` at `memento_restore`, set `MEMENTO_DATABASE_NAME=memento_restore`, and retain the other required configuration values. The validator opens no Immich, SMTP, Push, or other network client. It checks the exact migration ledger, required extensions, singleton setup and sole-Curator state, validated foreign keys, publication and Staged update projections, security settings, and representative non-sensitive counts inside one repeatable-read, read-only transaction.

For a local source checkout:

```sh
MEMENTO_DATABASE_URL='<POSTGRESQL_URL_SELECTING_memento_restore>' \
MEMENTO_DATABASE_NAME=memento_restore \
go run ./cmd/migrations validate
```

For the production image, supply the same environment and secret mounts used by Memento, override only the candidate database URL and name, and run `/usr/local/bin/memento-migrations validate` with the image entrypoint overridden. A successful command prints a JSON object with `"status":"valid"`, the completed checks, and representative counts. Any failure exits nonzero. The `validate` action never applies migrations. Use `memento-migrations apply` only for an explicit migration operation, never as candidate validation.

After validation succeeds, preserve the old database during cutover. Keep every Memento API and worker process stopped through the database rename and the first nonce-bearing startup. Do not use a rolling deployment or overlap a pre-Recovery-fence binary with this cutover. From the administrative `postgres` database, terminate only Memento connections and use a unique suffix in place of `<RESTORE_TIMESTAMP>`:

```sql
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE datname IN ('memento', 'memento_restore')
  AND pid <> pg_backend_pid();

ALTER DATABASE memento RENAME TO memento_pre_restore_<RESTORE_TIMESTAMP>;
ALTER DATABASE memento_restore RENAME TO memento;
```

If the second rename fails, the original database still exists under the `memento_pre_restore_<RESTORE_TIMESTAMP>` name and can be renamed back. Keep that database until recovery validation and Curator review succeed. Remove it later only through an explicit operator decision.

Before the first application start against the restored production database, generate a fresh nonce and provide it through the protected `MEMENTO_RECOVERY_NONCE_FILE` setting. Startup hashes the nonce, takes the singleton settings lock, rotates the security epoch, persists Recovery hold, and writes the security audit in one transaction before starting HTTP or worker work. Only nonce hashes are stored. Reusing any previously consumed nonce is an idempotent no-op. Every different nonce starts a new hold and rotates the epoch, even when a hold is already active. Remove the nonce setting after successful activation. A normal restart never clears the persisted hold.

During Recovery hold, liveness remains available but readiness returns unavailable. Restored Sessions, Recipient content, Invitation and optional email delivery, Web Push, notification assembly, outbox dispatch, and ordinary job claims remain blocked. Passwordless sign-in stays non-enumerating and sends a code only to the sole Curator. Challenges restored from the backup cannot create a Session because they belong to the prior security epoch.

Open Memento, sign in as the Curator with the fresh code, and review the bounded restored-state counts on the Recovery screen. Release requires that fresh current-epoch Curator Session, its CSRF token, and explicit confirmation. Release is persisted and audited. It does not rewrite People, Audiences, Publications, or interaction history. Keep the pre-restore database until the application is ready and the Curator has completed this review.

A database restore does not restore Immich Media, SMTP credentials, the Immich API key, VAPID private keys, configuration files, or an optional local GeoIP database. Back up those through their own secure operator procedures.

## License

Memento is licensed under the [MIT License](LICENSE).
