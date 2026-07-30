# Clean-environment release exercise

This exercise is the final operator acceptance gate for a Memento release. Run it on a clean isolated host using published digest-pinned images. Ordinary CI proves deterministic contracts with fixtures, but it cannot prove a real Immich library, SMTP service, browser Push service, destructive restore cutover, or operator procedure.

A stable version tag creates a draft GitHub Release and its final candidate image digest. A stable release is not complete until this exercise has passed against that exact digest, its sanitized record has been reviewed and attached, and a release maintainer publishes the same draft without rebuilding it. Keep failed attempts in the record and link each failure to a tracked issue.

## Safety and environment

Use a disposable environment with:

- no source checkout and no locally built Memento image, using only the verified release deployment bundle;
- Docker and the Compose plugin installed;
- a dedicated Memento PostgreSQL database and role;
- a dedicated or approved Immich v3.0.3 instance with non-sensitive test Media;
- controlled SMTP capable of receiving real messages;
- one supported trusted browser device capable of real Web Push;
- one Curator mailbox and at least two test Recipient mailboxes;
- encrypted temporary backup storage;
- a prior supported Memento digest and the candidate digest for the upgrade phase.

Do not use real family identities or Media. Do not include email addresses, tokens, credentials, private hostnames, Person names, Media, screenshots of private content, or raw provider responses in the release record.

## Record release inputs

Record these non-sensitive values before changing the environment:

```sh
docker version
docker compose version
sha256sum --check memento-image.txt.sha256
cat memento-image.txt
gh attestation verify "oci://$(cat memento-image.txt)" --repo robinjoseph08/memento
```

Inspect and record the image's version, revision, source, created time, and MIT license label. Confirm the digest equals the GitHub Release. Pull only by digest.

## Phase 1: clean deployment

1. Provision the Memento role and logical database from the [operator runbook](operator-runbook.md).
2. Prove `unaccent` and `pg_trgm` exist in the Memento database.
3. Prepare protected configuration and secret files with no secrets in YAML or the environment file.
4. Configure Immich v3.0.3 with exactly `album.read`, `asset.read`, `asset.view`, `asset.download`, `person.read`, and `face.read`.
5. Configure production SMTP, stable VAPID keys, and optional local GeoIP when it is part of the release environment.
6. Set `MEMENTO_IMAGE_DIGEST` to the candidate `sha256:...` digest in the production environment file.
7. Validate `deploy/compose.production.yaml`, pull, and start with `--no-build`.
8. Confirm the deployment has one Memento container, the process runs as UID 10001, the root filesystem is read-only, and Caddy plus the Go application and worker are in that image.
9. Confirm liveness and readiness return HTTP 200 and expose only allowlisted states.
10. Confirm the frontend, manifest, service worker, Caddy security headers, and HTTPS public origin.

Pass only if the host has no locally tagged substitute and `docker inspect` reports the exact candidate digest.

## Phase 2: setup and source contract

1. While the endpoint remains controlled, queue the required setup test email.
2. Poll its safe status until sent and verify the actual message arrives.
3. Open Memento over HTTPS and complete first-browser setup with all informed choices.
4. Confirm setup is permanently unavailable in a second browser.
5. Connect and discover the approved Immich library.
6. Confirm the server reports exactly Immich v3.0.3 and the permission gate rejects both missing and extra permission tests before restoring the exact key.
7. Confirm no Immich credential, source path, direct URL, owner detail, or raw DTO appears in browser traffic.

## Phase 3: authorized publication and delivery

1. Discover a Source album containing at least one image and one video.
2. Draft an Event, place its Media, confirm Attendance as needed, approve every Audience, complete final review, and publish.
3. Create and onboard two Recipients. Authorize one for the Event and keep the other unauthorized.
4. In the authorized Session, verify Event metadata, thumbnail, preview, ranged video, original, and archive delivery.
5. In the unauthorized Session, request the same stable Memento identifiers and verify no content, count, cover, stream, archive, or existence hint is returned.
6. Add one Comment and one Favorite. Confirm their privacy boundaries and first-party in-portal activity.
7. Generate optional Publication or Comment activity and receive a real immediate or scheduled email according to the configured preference.
8. Enroll a trusted device through the explicit Push control and receive a real Web Push notification.
9. Confirm the Push payload contains only authorized aggregate context and that a public-computer Session cannot enroll.

Record outcomes and response classes, not identifiers or content.

## Phase 4: dependency and shutdown drills

Execute every drill in [Dependency failure and shutdown drills](dependency-drills.md):

- PostgreSQL loss fails readiness while liveness remains available.
- Immich loss fails readiness and protected Media delivery closed.
- SMTP loss preserves authenticated access and in-portal activity.
- Push loss preserves authenticated access, in-portal activity, and email independence.
- Active jobs and streams stop safely on SIGTERM and durable work is reclaimable.

Copy one sanitized evidence block per drill into the release record. A failed drill blocks release until its cause is fixed and the complete affected drill is rerun.

## Phase 5: backup, restore, and Recovery hold

1. Create a custom-format Memento database backup using a private temporary file.
2. Verify the archive list and move it to encrypted off-container storage.
3. Create a separate `memento_restore` logical database and required extensions.
4. Restore the archive as the Memento application role with owner, ACL, and extension comments excluded as documented.
5. Run `/usr/local/bin/memento-migrations validate` from the candidate digest against the restored database.
6. Confirm the validator is read-only by recording database transaction or audit evidence appropriate to the staging environment.
7. Stop Memento and preserve the original database under a timestamped name during database rename cutover.
8. Generate a fresh 32-byte-or-longer Recovery nonce, place it in the protected secret mount, and start the candidate once.
9. Confirm liveness remains available and readiness reports Recovery unavailable.
10. Confirm every restored Recipient Session is invalid, Recipient content and optional notifications are blocked, and ordinary jobs are not claimed.
11. Remove the nonce setting immediately after activation. Restart once and confirm the persisted hold remains.
12. Request a fresh Curator sign-in code, authenticate in the current security epoch, review the restored counts, and explicitly release Recovery hold.
13. Confirm readiness, Curator access, Recipient sign-in, authorized delivery, Comments, Favorites, and activity recover.
14. Preserve the pre-restore database until review and smoke tests are complete.
15. Confirm the Memento PostgreSQL backup did not claim to restore Immich Media, configuration, or secrets. Verify their separate recovery procedures exist.

Reuse of any old Recovery nonce is a failed exercise.

## Phase 6: migration-compatible upgrade

Use a prior supported digest as the running version and the candidate as the target. For the first stable release, publish and exercise a release candidate digest first so the final digest-to-digest transition uses the same procedure.

1. Record the prior digest and establish healthy setup, Publication, Recipient, email, and Push state.
2. Create a fresh backup.
3. Restore it to an isolated disposable PostgreSQL instance with no production route or credential, run candidate migrations there, then run the candidate read-only validator.
4. Announce and begin planned downtime. Remove the single instance from traffic and stop it cleanly.
5. Run candidate migrations explicitly against production.
6. Change only `MEMENTO_IMAGE_DIGEST` to the candidate digest. Do not rotate stable secrets or VAPID keys.
7. Start with `--no-build`, wait for readiness, and repeat the setup-state, Curator, Recipient, authorization, Media, worker, email, and Push smoke tests.
8. Confirm the prior binary is not used after the migration boundary.
9. State the tested rollback boundary: before migration, revert the digest; after migration, restore the pre-upgrade backup with a fresh Recovery nonce.
10. Keep the prior digest and backup through the observation window.

A rolling or overlapping two-instance upgrade fails the exercise.

## Phase 7: release gates and final shutdown

1. Confirm the commit recorded in the image passed every required CI job.
2. Confirm the draft GitHub Release includes `LICENSE`, `memento-image.txt`, its checksum, and the checksum-pinned deployment bundle.
3. Verify the attached SBOM, provenance, and GitHub attestation refer to the same digest.
4. Repeat liveness, readiness, authorized delivery, unauthorized denial, SMTP, and Push probes.
5. Send SIGTERM with an idle system and confirm status 0 before the configured grace period.
6. Start once more and confirm readiness and durable state.
7. Review logs and evidence for secrets or private data.
8. Mark the exercise passed only after a second reviewer checks every phase, attach the sanitized record, then publish the unchanged draft GitHub Release.

## Versioned evidence template

Store the sanitized result in the release record system or in `docs/releases/<version>.md` when project policy calls for repository evidence.

```markdown
# Memento <version> release exercise

Status: passed | failed
Candidate tag:
Candidate commit:
Candidate image digest:
Prior image digest:
Exercise host identifier:
Docker version:
Compose version:
Primary operator:
Reviewer:
Started UTC:
Completed UTC:

## Artifact verification

- [ ] Digest matches GitHub Release
- [ ] Checksum passes
- [ ] Attestation passes
- [ ] OCI version, revision, source, created, and MIT labels match
- [ ] SBOM and provenance refer to the digest

## Clean deployment and setup

- [ ] Fresh database and secrets
- [ ] One non-root production container
- [ ] Liveness and readiness healthy
- [ ] Required email and first-browser setup complete
- [ ] Exact Immich version and permissions accepted

## Publication and delivery

- [ ] Authorized image, video, original, and archive succeed
- [ ] Unauthorized delivery and metadata fail closed
- [ ] Comment, Favorite, and in-portal activity pass
- [ ] Real optional email passes
- [ ] Real trusted-device Push passes

## Drills

- [ ] PostgreSQL loss
- [ ] Immich loss and protected delivery
- [ ] SMTP loss
- [ ] Push loss
- [ ] Interrupted job and stream shutdown

## Restore and Recovery

- [ ] Backup and archive validation
- [ ] Separate restore and read-only validation
- [ ] Fresh nonce activates Recovery hold
- [ ] Restored Sessions, access, notifications, and jobs blocked
- [ ] Fresh Curator review releases hold

## Upgrade

- [ ] Prior-to-candidate disposable migration validation
- [ ] Planned-downtime production upgrade
- [ ] Post-upgrade release gates
- [ ] Rollback boundary confirmed

## Failures and follow-up

List every failed attempt and linked issue. Do not delete earlier failures.

## Approval

Operator:
Reviewer:
Decision: PASS | FAIL
```
