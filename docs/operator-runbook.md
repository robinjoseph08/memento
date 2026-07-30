# Memento operator runbook

This runbook covers production deployment, monitoring, shutdown, backup, restore, and upgrades. Read the [runtime configuration and provisioning guide](../README.md#runtime-configuration) before using it. Memento supports one application instance, one Memento PostgreSQL database, and one existing Immich v3.0.3 instance.

## Release identity

A Memento release publishes one multi-platform image to GHCR. A version tag is a discovery reference. The image digest recorded in the GitHub Release is the deployment identity.

1. Open the GitHub Release for the selected version.
2. Download `memento-image.txt` and `memento-image.txt.sha256`.
3. Verify the checksum and inspect the exact reference:

   ```sh
   sha256sum --check memento-image.txt.sha256
   MEMENTO_IMAGE=$(cat memento-image.txt)
   case "$MEMENTO_IMAGE" in
     ghcr.io/robinjoseph08/memento@sha256:????????????????????????????????????????????????????????????????) ;;
     *) echo "Release image is not digest pinned" >&2; exit 1 ;;
   esac
   docker pull "$MEMENTO_IMAGE"
   docker image inspect "$MEMENTO_IMAGE" \
     --format '{{ index .Config.Labels "org.opencontainers.image.version" }} {{ index .Config.Labels "org.opencontainers.image.revision" }} {{ index .Config.Labels "org.opencontainers.image.source" }} {{ index .Config.Labels "org.opencontainers.image.licenses" }}'
   ```

4. Confirm the version and revision match the release, the source is this repository, and the license is `MIT`.
5. Verify the attached GitHub attestation with the GitHub CLI when available:

   ```sh
   gh attestation verify "oci://$MEMENTO_IMAGE" --repo robinjoseph08/memento
   ```

The image contains the MIT license at `/usr/share/licenses/memento/LICENSE`. Its attached SBOM and provenance describe the exact build. Never replace the digest with `latest`, a version tag, or another floating alias in production.

The publish job requires the protected GitHub `release` environment. Repository administrators must configure required reviewers for that environment and a tag ruleset that limits `v*` creation to release maintainers. The workflow accepts only a tag at the current head of `master` and reconfirms that state after environment approval.

Prerelease tags publish a GitHub prerelease. A stable version tag creates a draft GitHub Release and pushes its exact candidate digest. Run the complete clean-environment exercise against that digest, attach the reviewed sanitized evidence, and only then publish the same draft without rebuilding or changing its assets. A version tag or GHCR tag alone is never release acceptance.

Download the versioned deployment bundle and checksum from the same GitHub Release, then verify and extract it:

```sh
sha256sum --check memento-deployment-<version>.tar.gz.sha256
tar -xzf memento-deployment-<version>.tar.gz
cd memento-deployment-<version>
```

The bundle contains the production Compose file, configuration examples, license, runbook, drills, and release exercise. A clean deployment does not require a source checkout.

## Prepare configuration and secrets

Copy `deploy/memento.example.yaml` and `deploy/production.env.example` from the verified bundle to an operator-owned directory:

```sh
install -d -m 0700 /srv/memento/config /srv/memento/secrets
install -m 0600 deploy/memento.example.yaml /srv/memento/config/memento.yaml
install -m 0600 deploy/production.env.example /srv/memento/production.env
```

Create these required secret files with no trailing explanatory text:

- `/srv/memento/secrets/database_url`
- `/srv/memento/secrets/immich_api_key`
- `/srv/memento/secrets/security_secret`

The security secret must contain at least 32 random bytes and remain stable across ordinary restarts and upgrades. Add `smtp_password` and `push_private_key` only when those features are enabled. A `recovery_nonce` is temporary and is permitted only for the first start after a validated restore.

Make the files readable by the image user, UID 10001, without making them public:

```sh
chown -R 10001:10001 /srv/memento/config /srv/memento/secrets
find /srv/memento/config /srv/memento/secrets -type d -exec chmod 0700 {} \;
find /srv/memento/config /srv/memento/secrets -type f -exec chmod 0600 {} \;
```

Do not put secrets in the environment file, YAML file, image, shell history, issue comments, monitoring labels, or backup logs. Back up configuration and secrets separately from PostgreSQL. Encrypt that backup and restrict it to operators who can administer Recipient access.

## Provision dependencies

### PostgreSQL

Follow [Provision PostgreSQL beside Immich](../README.md#provision-postgresql-beside-immich). Memento requires:

- a separate logical database, normally `memento`;
- a separate non-superuser login role, normally `memento_app`;
- `unaccent` and `pg_trgm` created inside the Memento database;
- a database URL that selects only that database;
- TLS whenever database traffic crosses an untrusted network.

Never provide the Immich logical database or Immich runtime role to Memento. Administrative credentials are used only for provisioning, backup, and restore.

### Immich v3.0.3

Connect Memento to the existing Immich private service URL. The release supports exactly Immich v3.0.3. The Curator-owned API key must have exactly these permissions:

- `album.read`
- `asset.read`
- `asset.view`
- `asset.download`
- `person.read`
- `face.read`

Missing or additional permissions fail Memento's connector gate. Memento never connects to Immich PostgreSQL.

### SMTP, Web Push, and GeoIP

Configure SMTP as described in [Verify required email delivery](../README.md#verify-required-email-delivery). Production permits STARTTLS or implicit TLS with certificate and hostname validation. Put the password in `/run/secrets/smtp_password`, set `MEMENTO_SMTP_PASSWORD_FILE` in the production environment file, and run the required setup test before exposing setup publicly.

For Web Push, generate one stable VAPID P-256 key pair. Put the private key in `/run/secrets/push_private_key`; keep only the public key and subject in YAML. Rotating this pair requires every trusted device to enroll again. Push service loss must not affect portal authorization or email delivery.

An optional MaxMind-compatible City database is local operator data. Mount it read-only and set `MEMENTO_GEOIP_DATABASE_PATH` to its container path. Memento performs no request-time GeoIP lookup. Back up the file or a reproducible acquisition record separately.

## Deploy the production Compose project

The production Compose file contains only Memento. PostgreSQL and Immich remain independently operated services. Create or select an external private Docker network that reaches both dependencies, then set `MEMENTO_NETWORK` to that network's name.

Use the production Compose file from the verified deployment bundle. Set `MEMENTO_IMAGE_DIGEST` in `production.env` to the `sha256:...` portion of the reference in `memento-image.txt`. The Compose contract fixes the image registry and repository so configuration cannot substitute another image. Validate without pulling or building:

```sh
docker compose \
  --env-file /srv/memento/production.env \
  --file deploy/compose.production.yaml \
  config --quiet
```

Start the exact image:

```sh
docker compose \
  --env-file /srv/memento/production.env \
  --file deploy/compose.production.yaml \
  pull
docker compose \
  --env-file /srv/memento/production.env \
  --file deploy/compose.production.yaml \
  up --detach --no-build
```

The container runs as UID 10001 with all Linux capabilities dropped, a read-only root filesystem, and writable Caddy data volumes. The default host bind is `127.0.0.1:8080`. Keep it on loopback when a host reverse proxy terminates public TLS. For direct Caddy TLS, configure `MEMENTO_SITE_ADDRESS`, publish the deliberate TLS ports, and persist Caddy data.

When another proxy terminates TLS, set `MEMENTO_CADDY_TRUSTED_PROXY_CIDRS` to only that proxy's network. The public URL must be HTTPS. Complete first-browser setup on a controlled endpoint before public exposure.

## Health monitoring

Probe both endpoints through the same route used by traffic:

- `GET /api/health/live` proves the process can answer. It performs no dependency calls and is the container health check.
- `GET /api/health/ready` proves PostgreSQL, migrations, setup consistency, Recovery state, worker freshness, and Immich are safe for traffic.

Remove the instance from traffic whenever readiness is not HTTP 200. Do not restart solely because readiness reports a dependency outage while liveness remains healthy. Alert on sustained readiness failure and inspect the allowlisted check names. Health output intentionally contains no URLs, credentials, SQL errors, recipient data, or provider responses.

SMTP status is informative. SMTP and Push are not authorization dependencies, so an external delivery outage must not remove portal access or in-portal activity.

## Graceful shutdown

Use Compose stop or send SIGTERM to the container. Do not send SIGKILL unless the configured grace period has expired:

```sh
docker compose --env-file /srv/memento/production.env \
  --file deploy/compose.production.yaml stop memento
```

On SIGTERM, Memento drops readiness first, stops new job claims, drains accepted HTTP requests, cancels or drains worker work within its bound, returns interrupted durable jobs for later claiming, closes PostgreSQL last, and exits. The bundled Caddy process stops in the same container. The provided 15-second Compose grace period exceeds the default 8-second application shutdown timeout. If `http.shutdown_timeout` is increased, increase `stop_grace_period` beyond it before deployment.

Confirm the container exits with status 0. A forced kill can interrupt streams and may leave work for lease expiry instead of immediate release, so investigate every grace-period overrun.

## Backup policy

Run a custom-format logical backup at least daily for the documented 24-hour recovery point objective. The complete safe command, temporary-file handling, archive check, and restore procedure are in [Database backup and restore](../README.md#database-backup-and-restore).

The operator must provide:

- encrypted off-container and preferably off-host storage;
- retention covering operator error discovery time;
- monitoring for missing, empty, stale, or failed backups;
- periodic restore drills into a separate database;
- a record of the image digest and configuration version associated with each backup.

PostgreSQL backup does not include Immich Media or database state, Memento configuration, the security secret, the Immich key, SMTP credentials, VAPID keys, Caddy state, or GeoIP data. Maintain separate encrypted backups or reproducible recovery procedures for each. Test Immich's own backup independently.

## Migration-compatible upgrade

Memento permits planned downtime and does not support rolling upgrades. Upgrade only between versions whose release notes identify the path as supported.

### Preflight on a disposable restore

1. Record the current image digest, configuration checksum, secret inventory, and health state.
2. Read all intervening release notes and obtain the candidate digest.
3. Create a fresh production database backup and verify its archive list.
4. Start an isolated disposable PostgreSQL instance or cluster with no route to production PostgreSQL. A second logical database on the production cluster is not a security boundary for untrusted candidate code.
5. Provision only the temporary Memento role, database, and required extensions there. Restore the backup and create a temporary database URL secret containing credentials valid only in that isolated instance.
6. Put the candidate container and isolated PostgreSQL instance on a dedicated temporary network, then run the candidate migration binary:

   ```sh
   docker run --rm --network "$UPGRADE_CHECK_NETWORK" \
     --entrypoint /usr/local/bin/memento-migrations \
     -e MEMENTO_DATABASE_URL_FILE=/run/secrets/database_url \
     -e MEMENTO_DATABASE_NAME=memento \
     -v /srv/memento/upgrade-check-secrets:/run/secrets:ro \
     "$CANDIDATE_IMAGE" apply
   ```

7. Run the same image's read-only validator by replacing `apply` with `validate`. Save its non-sensitive JSON result.
8. Destroy the temporary credential, network, and PostgreSQL instance only after the maintenance decision is complete.

Never give candidate code a production database credential or a network path to production during preflight, and never test candidate migrations against production while traffic is active.

### Planned downtime

1. Announce downtime and stop external traffic.
2. Stop the one Memento container and verify it exited cleanly.
3. Create and validate one final pre-upgrade backup.
4. Pull the candidate by digest.
5. Run `/usr/local/bin/memento-migrations apply` once against production using the same protected configuration and secret mounts.
6. Replace `MEMENTO_IMAGE_DIGEST` with the candidate digest and start with `--no-build`.
7. Require liveness and readiness to become healthy within the maintenance window.
8. Confirm Curator sign-in, Recipient sign-in, an authorized Media delivery, an unauthorized denial, worker freshness, and configured email and Push behavior.
9. Restore traffic and monitor readiness, logs, job backlog, and delivery problems.
10. Keep the prior digest and pre-upgrade backup until the observation window closes.

Startup also applies pending migrations under a PostgreSQL advisory lock. The explicit maintenance step makes the migration boundary visible and keeps it inside planned downtime.

### Rollback boundary

Before candidate migrations touch production, restoring the prior image digest is safe if configuration remains compatible. After forward migrations run, never assume an older binary can use the new schema. There is no one-click migration rollback.

A post-migration rollback is a restore operation:

1. Stop Memento and preserve the failed upgraded database for investigation.
2. Restore the final pre-upgrade backup into a separate database.
3. Validate it with the prior release image.
4. Cut over using the documented database rename sequence.
5. Generate a fresh Recovery nonce, mount it only for the first start, and start the prior digest.
6. Verify Recovery hold blocks readiness, old Sessions, Recipient access, and optional notifications.
7. Remove the nonce file setting immediately after activation.
8. Sign in freshly as Curator, review restored counts, explicitly release Recovery hold, smoke-test, and only then restore traffic.

Do not rotate the security secret, Immich key, SMTP password, or VAPID pair merely because an upgrade or rollback occurred.
