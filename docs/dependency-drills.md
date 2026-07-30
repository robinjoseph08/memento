# Dependency failure and shutdown drills

Run these drills against an isolated staging deployment built from the exact release digest. Do not inject failure into a shared PostgreSQL or Immich service unless every affected operator has approved it. Record the sanitized evidence fields at the end of each drill.

The automated suite verifies the underlying behavior on every release:

```sh
mise run test:production
mise run test:integration
```

The production topology test stops Immich and PostgreSQL independently, proves readiness fails while liveness remains available, restores each dependency, and proves readiness recovers. Integration tests cover fail-closed Media delivery, SMTP and Push retry behavior, durable in-portal activity, interrupted jobs, and shutdown ordering.

## Common preparation

1. Record the release tag, commit, image digest, UTC start time, operator, and staging environment identifier.
2. Confirm liveness and readiness are HTTP 200.
3. Confirm one authorized staging Recipient can open one published Media item and one unauthorized Recipient receives no content.
4. Confirm logs and health responses are being captured into a protected, short-retention location.
5. Set an abort time. Restore service immediately if another system becomes affected or Memento exposes a secret, private upstream detail, or unauthorized byte.
6. Never place tokens, email addresses, Person names, Media, internal URLs, provider responses, or database URLs in the evidence record.

## PostgreSQL loss

### Injection

Block only Memento's path to its logical database. Prefer a temporary network rule scoped to the Memento container. If staging PostgreSQL is dedicated to Memento, stopping that service is also acceptable. Do not stop a PostgreSQL container shared with Immich because that combines two failures.

### Pass criteria

- `/api/health/live` remains HTTP 200.
- `/api/health/ready` becomes HTTP 503 within the database health timeout and reports only `"postgresql":"unavailable"` plus other allowlisted states.
- New traffic is removed from routing.
- Health payloads and logs contain no URL, password, SQL text, driver error, or private hostname.
- The process does not claim that it is ready from cached database state.
- After restoring the database path, readiness returns to HTTP 200 without data repair or process replacement.

### Recovery

Restore network access, poll readiness with a bounded deadline, and verify Curator and Recipient Sessions still enforce their current access generations. Investigate any migration, Recovery, worker, or setup check that does not recover.

Automated evidence: `TestReadyReportsEachUnsafeDependencySymmetrically`, `TestReadyBoundsPostgreSQLCheck`, `TestNewWiresRealPostgreSQLMigrationAndSetupChecks`, and `tests/test-production.sh`.

## Immich loss and protected delivery

### Injection

Block Memento's connection to the dedicated Immich v3.0.3 staging endpoint while leaving PostgreSQL available. Keep an already authenticated Curator and Recipient browser open.

### Pass criteria

- Liveness remains HTTP 200 and readiness becomes HTTP 503 with only `immich` reported unavailable.
- Existing Memento Event metadata may remain visible because PostgreSQL owns it.
- Every authorized thumbnail, preview, video, original, and archive attempt returns no Media bytes while Immich is unavailable.
- Unauthorized requests remain indistinguishable from missing content and never trigger an Immich request.
- No response exposes an Immich URL, API key, asset UUID, source path, raw DTO, redirect target, or provider body.
- No Media item is relinked and no Audience broadens because of the outage.
- After service restoration, readiness and authorized delivery recover using the same Memento URL.

### Recovery

Restore the exact endpoint, wait for readiness, then test one authorized and one unauthorized delivery. Review delivery problems and source-missing findings. A transport outage alone must not invent Source missing state.

Automated evidence: `TestThumbnailRouteKeepsUpstreamFailuresSafeAndPrivate`, `TestMediaRepresentationSelectsOnlyActiveBackingAndFailsClosedForVideo`, `TestUpstreamMissingResponseFailsEveryRepresentationClosedWithoutRemovingHistory`, `TestMalformedOrUnauthorizedUpstreamFailureDoesNotInventSourceMissing`, and `tests/test-production.sh`.

## SMTP loss

### Injection

Use an authenticated staging Session established before the drill. Block the configured SMTP endpoint or direct it to a controlled server that returns a temporary failure. Generate optional Publication or Comment activity. Do not use first-time setup or passwordless sign-in as the portal-availability probe because those workflows require email by design.

### Pass criteria

- Existing Curator and Recipient portal access continues.
- Publications, authorized Media, Comments, Favorites, New for you, and first-party engagement activity continue according to their normal authorization rules.
- In-portal activity remains visible even though optional email is delayed.
- The durable email job records a bounded retry using an allowlisted safe diagnostic.
- Readiness does not falsely classify SMTP as an authorization dependency. Its SMTP status may report the configured delivery condition.
- No recipient address, message body, code, credential, raw SMTP response, or provider identifier appears in health output or ordinary logs.
- Restoring SMTP allows the durable retry to complete without creating a second logical activity item.

### Recovery

Restore SMTP, observe the existing job reach a terminal state, and verify one logical notification window. Investigate duplicate external delivery separately because SMTP acceptance and PostgreSQL commit cannot be atomic.

Automated evidence: `TestReadyReportsSMTPWithoutChangingLibraryReadiness`, `TestTemporaryFailureRetriesWithBoundedBackoff`, `TestInterruptedRetryPreservesDurableBackoff`, `TestOutboxLeaseIsReclaimableAfterInterruptedDispatch`, and the immediate and weekly email integration suites.

## Push loss

### Injection

Enroll a dedicated trusted staging device first. Block only that device's push endpoint, or use a controlled endpoint that produces the provider outcome being tested. Generate optional authorized activity while keeping the browser Session active.

### Pass criteria

- Portal authorization, protected Media, in-portal activity, and email remain independent and available.
- A temporary provider failure creates bounded durable retry behavior.
- Provider 404 or 410 disables only the affected device subscription.
- Session revocation, Recipient Suspension, access Revocation, Withdrawal, and Recovery hold suppress delivery before the provider call.
- The payload contains only authorized aggregate counts and no Media identity, Comment body, thumbnail, reusable URL, token, or Memento credential.
- Memento bypasses environment proxies and does not follow redirects to an unapproved address.

### Recovery

Restore egress for a temporary failure and observe the existing batch complete. Re-enroll only the affected device after a terminal subscription response. Do not rotate the VAPID pair as an outage response.

Automated evidence: `TestPushReauthorizesWithdrawalImmediatelyBeforeSend`, `TestPushRequiresCurrentSecurityEpochAtSend`, `TestPushMatchesEmailSurvivorsAndTerminalOutcomeIsDeviceOnly`, `TestEndpointPolicyRejectsUnsafeDestinations`, and `TestHTTPClientDisablesProxyAndRedirects`.

## Interrupted jobs and streams

### Injection

1. Start a large authorized video or original response through Memento and verify bytes have begun.
2. Arrange one controlled durable job to be active. A reconciliation or a delivery against a slow staging fixture is suitable.
3. Send SIGTERM to the Memento container through `docker compose stop`. Do not use SIGKILL.
4. Observe readiness, the client connection, container state, and durable job state.

### Pass criteria

- Readiness drops before HTTP and worker draining begins.
- New worker claims stop.
- Accepted requests either finish inside the configured shutdown deadline or have their contexts canceled. No stream continues after container exit.
- The worker drains within its configured bound. Interrupted work returns to a reclaimable state and no lease remains permanently owned.
- PostgreSQL closes after HTTP and worker handling.
- The container exits with status 0 before `stop_grace_period`.
- After restart, readiness recovers and the job can be reclaimed without broadening authorization or repeating a recorded terminal effect.
- The interrupted client receives no upstream credential, internal URL, or unauthorized bytes.

### Recovery

Start the same digest with unchanged secrets and configuration. Poll readiness, inspect safe job status, and repeat one authorized and unauthorized Media request. Treat forced termination or a grace-period overrun as a failed drill.

Automated evidence: `TestShutdownOrdersReadinessClaimsDrainAndClose`, `TestShutdownUsesCallerDeadline`, `TestShutdownStopsClaimsAndMakesLeaseReclaimable`, `TestExpiredLeaseIsReclaimedAndCompleted`, `TestRepresentationClosesOpenedBodyWhenActorInvalidatesBeforeHandoff`, and the bounded SIGTERM check in `tests/test-production.sh`.

## Evidence record

Copy this block into the versioned release exercise. Do not include sensitive output.

```text
Drill:
Release tag:
Commit:
Image digest:
Environment:
Operator:
Started UTC:
Recovered UTC:
Injection method:
Observed liveness:
Observed readiness:
Authorization probes:
Durable work observation:
Secret-leak review:
Recovery observation:
Result: PASS | FAIL
Follow-up issue:
```

A rerun does not erase a failed observation. Link the failure to a tracked issue, fix the cause, and execute the drill again with a new timestamp.
