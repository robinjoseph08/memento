# Memento Product and Architecture Specification

## Status

Status: implementation-ready specification for the MVP.

The Phase 1 deployable application foundation is implemented. Setup, Recipient workflows, restore validation, Recovery hold, and the remaining product phases are not yet implemented.

The key words **MUST**, **MUST NOT**, **REQUIRED**, **SHOULD**, **SHOULD NOT**, and **MAY** are normative. They have their usual requirements-language meanings. Product terms such as Person, Recipient, Pending Recipient, Audience, Publication, and Staged update have the exact meanings in [`CONTEXT.md`](../CONTEXT.md).

## Purpose

Memento is a private, self-hosted family photo and video portal. One Curator organizes Media items already managed by Immich, publishes selected material to approved Audiences, and gives a small set of Recipients a low-friction web and PWA experience.

Memento is a greenfield bootstrap against an existing Immich instance. It MUST NOT migrate legacy sharing state. It is optimized for one household, one Curator, 30 to 50 Recipients, and up to 100,000 Media items. Its configuration MUST remain open-source and portable between operators.

Memento's selected license is MIT. The repository MUST include the license file before its first source release.

## Success criteria

The MVP succeeds when:

1. The Curator can connect Immich v3.0.3, discover Source albums, organize Events and Moments, review explained Audience proposals, and publish atomically.
2. A Recipient can access only Media authorized by current approved Audiences, with no Moment or hidden-content leakage through pages, search, counts, covers, media delivery, downloads, notifications, or Comments.
3. Pending Recipients can be preapproved in Audiences without gaining access or optional notifications before Invitation acceptance and Onboarding completion.
4. Source changes never bypass Curator review, and missing or relinked Immich records never silently transfer authorization.
5. Email and standards-based Web Push can announce matching authorized activity after the same 15-minute coalescing window, while either channel can be disabled independently.
6. One production image, one Memento application instance, PostgreSQL, and the existing Immich instance can operate the system without Redis or a second media library.
7. Normal list, detail, authorization, and search operations meet the baseline targets in this specification at 100,000 Media items and 50 Recipients.
8. Operators can health-check, stop, upgrade, back up, and restore the application using documented boundaries.

## Source precedence

When sources conflict, implementation MUST use this order:

1. The normative decisions and explicit ambiguity resolutions in this specification.
2. Canonical language and definitions in [`CONTEXT.md`](../CONTEXT.md).
3. Resolutions attached to the closed tickets listed in the traceability appendix.
4. The Immich and Web Push research notes in [`docs/research`](research/).
5. The map ticket, [Define Memento](https://github.com/robinjoseph08/memento/issues/1).
6. Shisho conventions as implementation precedent, where they do not conflict with this specification.

The later notification decision supersedes the earlier push deferral. Web Push is MVP. The term **Preview as Recipient** supersedes prototype wording. Production MUST self-host fonts and MUST make no runtime Google Fonts request.

## Product principles and invariants

- Memento has exactly one Curator. The Curator is a role on a Person and the same Person also holds the Recipient role.
- Memento is single-instance and single-household. Planned upgrade downtime is acceptable.
- No public Media, anonymous gallery, reusable public link, recipient Immich account, or browser-visible Immich credential is permitted.
- Immich remains the only master media source. Memento MUST NOT persist original Media or derivatives as a server-side Media cache.
- The Audience is the sole source of Recipient item access. Interest lists, Visibility circles, Attendance, face matches, Invitations, notification preferences, and engagement never grant access. Curator access is independent of Audiences.
- Every Recipient content read and stream MUST authorize the current Session, Recipient state, access generation, and current-published Audience entitlement on the server. Curator-only, Recipient-self, setup, token-exchange, and health operations use their own explicit policies and do not require an Audience.
- Source changes, draft edits, and Staged updates remain private until a Publication. Withdrawal is the sole immediate Curator action that removes published access outside a Publication.
- Portal identities and URLs survive Immich identity churn when the Curator explicitly confirms a relink.
- The application MUST never connect to Immich's logical database, use Immich's PostgreSQL application role, query Immich tables, or infer behavior from Immich's database schema.

## Scope

### MVP

The MVP includes:

- first-browser setup and one Curator;
- People, family relationships, Visibility circles, and audited Interest lists;
- Pending Recipient designation, explicit Invitations, passwordless sign-in, Onboarding, Sessions, suspension, revocation, and email changes;
- Source album discovery, ignore and restore, Event and Moment organization, Loose items, Attendance, Audience proposals, Staged updates, Publications, corrections, withdrawals, source-missing handling, and confirmed relinking;
- Recipient Photos, Events, Favorites, New for you, item viewer, authorized originals, Event archives, subset archives, and search;
- item-level Comments, private Favorites, Invitation suggestions, Curator activity, and first-party engagement;
- generic SMTP, weekly and immediate email, standards Web Push, delivery retries, and Curator-visible delivery failures;
- scheduled and on-demand Immich reconciliation;
- PostgreSQL-backed jobs and outbox delivery;
- responsive installable PWA, dark and light themes, and mobile and desktop workflows;
- backup and restore documentation, health checks, graceful shutdown, structured logging, and audit history.

### Deferred

The following are deferred:

- multiple Curators, multiple households, general tenancy, recipient uploads, collaborative albums, and native mobile apps;
- public links, public Media, reusable bearer Media URLs, and public CDN delivery;
- media editing, replacing Lightroom or Immich, or storing a second master library;
- semantic, visual, object, OCR, AI-generated, map, coordinate, radius, or place-hierarchy search;
- broad natural-language date parsing;
- Comment threads, mentions, reactions, and Event-level Comments;
- one-click Publication rollback;
- provider-specific SMTP bounce and complaint webhooks or adapters;
- Immich sync-stream credentials, WebSocket correctness dependencies, and support for Immich releases newer than v3.0.3 before contract tests pass;
- high availability, multiple app replicas, Redis, an external queue, and zero-downtime upgrades;
- automatic backup scheduling by Memento;
- permanent audit purge tooling for the MVP.

Immediate-household albums that the Curator chooses to keep entirely in Immich remain outside Memento.

## System and deployment architecture

```text
Browser or installed PWA
        |
     HTTPS
        |
Caddy in the Memento image
  | serves frontend
  | proxies /api and protected media
        |
Go modular monolith, one process
  | Echo HTTP API
  | domain services
  | PostgreSQL worker and outbox dispatcher
  |
  +--> Memento PostgreSQL database and role
  +--> Immich v3.0.3 REST API through IMMICH_URL
  +--> generic SMTP server
  +--> standards Web Push endpoints
  +--> optional local GeoIP database
```

### Deployment boundaries

- Production MUST use one image containing the built frontend, Caddy, and the Go binary. Caddy MUST serve static frontend assets and reverse proxy API and streaming routes to Go.
- Production MUST run one Memento app instance. The in-process worker runs in that instance.
- PostgreSQL MAY be the same server or container used by Immich, but Memento MUST have its own logical database and login role. `unaccent` and `pg_trgm` MUST be created in the Memento database. Extensions are database-local even when their files already exist in the Immich PostgreSQL image.
- Sharing Immich's private Docker network is recommended for simple, private transport but is optional. `IMMICH_URL` MUST be configurable and MAY point to any operator-approved reachable URL.
- The Immich API key MUST be Curator-owned, server-side, and limited to `album.read`, `asset.read`, `asset.view`, `asset.download`, `person.read`, and `face.read`.
- Memento MUST validate the connected Immich version. The supported contract remains exactly v3.0.3 until a later compatible release passes the complete connector contract suite.
- Secrets MUST be supplied through environment variables, container secrets, or a protected configuration file. Secrets MUST NOT appear in generated frontend assets, logs, health payloads, or diagnostics.
- Configuration MUST use Koanf with documented defaults, YAML support, and environment-variable overrides. Portable configuration MUST not depend on a specific Compose project name or host path.

## Component boundaries

### Caddy

Caddy owns TLS termination when deployed directly, static file serving, SPA fallback, compression, security headers, request-size limits, and reverse proxying. It MUST NOT make authorization decisions or cache protected Media.

### Go application

The Go application owns authentication, authorization, domain transactions, PostgreSQL persistence, Immich normalization, reconciliation, streaming authorization, notification assembly, search, audit, jobs, and health readiness.

### PostgreSQL

PostgreSQL is the source of truth for all Memento state. It also provides migrations, transactional job claims, outbox durability, normalized search indexes, constraints, and audit persistence. Memento MUST use PostgreSQL transactions for every state change that affects authorization or external delivery.

### Immich

Immich owns source assets, source albums, source metadata, face clusters, thumbnails, playable video, and originals. Memento treats Immich people and faces only as advisory suggestion inputs. Immich does not know Memento Recipients or Audiences.

### Browser and PWA

The browser owns presentation and private HTTP caching. The service worker MAY cache the application shell and public build assets. It MUST NOT create an offline protected gallery or persist protected Media in a shared cache. Web Push subscriptions are per trusted device.

## Repository and module architecture

Memento MUST be a Go modular monolith following Shisho conventions, adjusted for PostgreSQL:

```text
cmd/api/                 application entry point
cmd/migrations/          Bun migration command
pkg/config/              Koanf configuration
pkg/database/            Bun PostgreSQL connection and transaction helpers
pkg/migrations/          Bun migrations
pkg/server/              Echo construction, middleware, and route registration
pkg/<domain>/            handlers.go, routes.go, service.go, types.go, models as needed
pkg/worker/              job claiming, dispatch, leases, and shutdown
pkg/immich/              typed v3.0.3 client, normalization, reconciliation
app/                     React application
app/components/          shared and domain UI
app/hooks/queries/       TanStack Query hooks
app/types/generated/     Tygo output, generated during build
public/                  PWA manifest, icons, and static assets
docs/                    product, operations, and research documentation
```

Initial backend domain packages SHOULD include `setup`, `people`, `relationships`, `recipients`, `auth`, `sessions`, `sources`, `media`, `events`, `audiences`, `publications`, `comments`, `favorites`, `notifications`, `push`, `suggestions`, `engagement`, `search`, `archives`, `audit`, `jobs`, and `outbox`. Cross-domain writes MUST be coordinated by an explicit application service or transaction, not by HTTP handlers calling each other.

Backend requirements:

- Go, Echo, Uptrace Bun, PostgreSQL, Bun migrations, Koanf, and `github.com/robinjoseph08/golib` logging and middleware.
- Package-by-domain structure with thin handlers, business rules in services, and database access behind domain services.
- Request context propagation through database, Immich, SMTP, push, archive, and streaming operations.
- Named exported Go structs for every JSON request and response. JSON fields MUST use `snake_case`.
- Go is the source of truth for API types. Tygo MUST generate TypeScript types. The frontend MUST NOT duplicate Go wire types.
- API acknowledgment without new information SHOULD return `204 No Content`.
- Migrations MUST be ordered, transactional where PostgreSQL permits, and marked applied only after success.

Frontend requirements:

- React, TypeScript, Vite, Tailwind CSS, TanStack Query, React Router, Tygo-generated API types, ESLint, and Prettier.
- TanStack Query owns server state. URL routes and structured filters own bookmarkable navigation state. Local component state owns transient editing state.
- Responsive behavior MUST be implemented as one product, not separate mobile and desktop applications.
- Dark mode is the default, light mode is supported, and the accent family is Tailwind sky.
- UI primitives SHOULD follow shadcn-style composition and accessible headless primitives.
- Fonts MUST be bundled with the application. Runtime requests to Google Fonts or another font CDN are forbidden.
- The PWA MUST have a stable manifest ID, the selected Memento icon showing three fanned photo tiles becoming one archive stack, standalone display, HTTPS service worker registration, and feature-detected push UX.
- DM Sans at weights 400, 500, 600, and 700 is the selected open-licensed Google Font and MUST be self-hosted in the production image. Runtime requests to Google Fonts remain forbidden.

At bootstrap, maintainers MUST select the latest stable project dependencies available at that time. The resulting exact versions MUST then be pinned in manifests and lockfiles. Production image tags and tool versions MUST NOT float on `latest`. Immich remains pinned contractually to v3.0.3 regardless of dependency bootstrap timing.

## Product workflows and state transitions

### First-time setup

The public setup route exists only while no Curator exists.

1. The operator starts Memento on a private or otherwise controlled endpoint.
2. A browser loads setup and provides the first Person's name and case-insensitively unique login email. Memento sends an email verification code without creating a Person or disabling setup.
3. After successful code verification, the browser completes the first-time Onboarding choices.
4. The final setup request runs one PostgreSQL transaction that locks the singleton setup state, confirms setup is still enabled and no Curator exists, consumes the verified challenge, creates the Person, assigns both Curator and Recipient roles, creates the completed Recipient access generation, stores the Onboarding choices, and disables setup.
5. The transaction commits before a normal Session is issued. One concurrent final setup request can win; all others receive a conflict response and cannot create another Curator. There is no CLI setup token.

Operator documentation MUST warn that first-time setup should be completed before public exposure. A disabled setup endpoint MUST not be re-enabled by deleting browser state or changing configuration.

### People and Recipient lifecycle

Person lifecycle: `current -> archived`, with `merge` moving historical references to a Curator-selected survivor. Referenced People are not hard-deleted.

A Person merge MUST run as one audited transaction and MUST NOT union roles, current Recipient access generations, login emails, or Sessions implicitly. If either Person is the Curator, that Person must be the survivor; the sole Curator role can never move through merge. At most one side may have a current Recipient access generation. When the source alone has current Recipient access, the Curator must explicitly transfer that one generation and resolve any email conflict before commit. Merging two People with current Recipient access is rejected until one access generation is revoked. Every Session belonging to either Person is invalidated, historical access generations and audit attribution remain distinguishable, and a preview must show all references and identity effects before confirmation.

Recipient access lifecycle:

```text
Person only
  -> Pending Recipient, Invitation not sent
  -> Pending Recipient, Invitation active
  -> Pending Recipient, Invitation accepted and Onboarding incomplete
  -> Recipient, Onboarding complete
  -> suspended -> Recipient, Onboarding complete
  -> revoked
  -> Pending Recipient in a new access generation, only by explicit Curator action
```

- The Curator MAY designate a Person as a Pending Recipient without sending an Invitation.
- A Pending Recipient MAY be included in Audience proposals and approved Audiences.
- Sending an Invitation is a later, explicit action. Creation of Recipient access MUST end with a separate **Create and send invitation** action when sending is desired.
- Access and optional email or push notifications MUST remain blocked through all Pending Recipient states.
- On explicit Onboarding completion, the Recipient gains access to the current-published material authorized by preapproved Audience entries in the current access generation. Completion MUST NOT send a notification backlog.
- Suspension invalidates every Session and temporarily blocks access without changing the access generation or approved Audiences.
- Lifting suspension restores still-valid Audience access after a new sign-in.
- Revocation invalidates every Session and permanently invalidates prior Audience entries by ending the current access generation. A later reinvitation creates a new generation and does not reactivate old entries.

### Invitations and Onboarding

- An Invitation is Curator-issued, single-use, and valid for 14 days. It MAY be revoked or reissued without replacing the Person or Recipient.
- The token-bearing GET only loads the frontend. It MUST NOT authenticate or mutate state.
- The Recipient explicitly selects **Accept invitation**, causing a POST exchange. The client then removes the token from browser history. Invitation pages MUST use `Referrer-Policy: no-referrer`.
- Acceptance establishes verified identity and starts resumable Onboarding without a second email code.
- Onboarding covers private-access expectations, Curator-visible engagement, explicit Interest-list choices, email preference, optional supported-device push, and explicit completion.
- Invitation email MUST be personalized with who invited the Pending Recipient, what Memento is, its private individual-access model, the 14-day acceptance period, and the Onboarding requirement.
- One automatic reminder is sent after seven days if the Invitation remains unaccepted.

### Passwordless sign-in and Sessions

- A sign-in request sends an eight-digit, single-use email code valid for ten minutes with at most five verification attempts.
- Request and verification responses MUST not reveal whether the email belongs to a Recipient.
- A successful verification creates an opaque random server-side Session. Sessions MUST NOT be JWTs.
- Trusted-device Sessions expire after one year of inactivity and have no absolute lifetime while used.
- Public-computer Sessions use a browser-session cookie and expire no later than 12 hours after creation. They cannot register Web Push. The interface MUST prominently offer sign-out throughout the Session. Authorized downloads remain available, but every download surface MUST show a prominent shared-computer privacy reminder.
- Recipients can inspect, rename, revoke, and sign out all of their Sessions. The Curator can inspect Sessions for every Recipient.
- Revoking or expiring a trusted Session immediately disables every linked Web Push subscription. Signing out all Sessions disables all subscriptions for that Recipient.
- Session displays include browser/platform, creation, last meaningful activity, and approximate location when an optional local GeoIP database is configured. Memento MUST never perform request-time third-party GeoIP lookups.
- A signed-in Recipient changing login email proves both old and new addresses with fresh codes. Curator recovery when the old mailbox is unavailable revokes all Sessions but preserves Person identity, Onboarding, preferences, interactions, and current access generation.

### Relationships, Visibility circles, and Interest lists

- Only the Curator can create, edit, archive, or merge People and family relationships.
- Parent-child, sibling, and current or former partner relationships are explicit and support multiple parents and partners. The directed parent-child graph MUST remain acyclic; a mutation is rejected when the proposed parent is already the Person's descendant. Family branches include current partners, descendants recursively, and descendants' current partners. Siblings, siblings' descendants, and former partners are excluded unless reached by another qualifying relationship. Relationships never grant access.
- Visibility circles overlap. Recipient discovery is the union of circles containing their Person and is not transitive.
- The Recipient People directory hides email, Recipient and Onboarding status, other Interest lists, hidden relationship intermediates, and circle structure.
- Interest lists start empty. Family-branch People are unselected suggestions, never automatic additions. The Recipient's own Person is not selectable because confirmed Attendance independently proposes that Recipient.
- The Recipient and Curator can view and edit that Recipient's Interest list. It is hidden from everyone else. Every mutation records actor, time, selected Person, and result.
- Loss of shared circle eligibility deactivates a choice without erasing history. Returning visibility does not reactivate it automatically.

### Source discovery and reconciliation

- Owned Immich albums enter the Source album inbox as unreviewed. Discovery does not create an Event.
- The Curator chooses **Draft event** or **Ignore**. Ignored albums remain tracked until restored.
- A Source album missing from Immich becomes Source missing and remains Curator-visible.
- The Curator MAY combine Source albums, divide one album among Events, and leave items unpublished.
- One Media item MAY appear in multiple Events. Its Recipient entitlement is the union of current approved Audiences through all current-published placements.
- Initial Moment proposals group by local capture date. The Curator chooses the timezone for unzoned timestamps. Unknown dates require manual placement.
- Moment and Media ordering default to capture time. The Curator may override both orders and explicitly choose an accessible Media item as the Event cover.
- Merged day proposals continue receiving later assets from those days. A manually split day sends later assets to an unassigned state for Curator placement.
- Event metadata becomes portal-owned after initialization. Later source metadata is an optional suggestion only.

### Attendance and Audience review

- Immich people and faces produce Curator-only suggested People with supporting Media.
- Only Curator-confirmed Attendance counts. Face matches never grant access or become Recipient-visible.
- An Eligible Recipient is proposed when their own Person is present or their active Interest list intersects Attendance.
- Each proposal retains all applicable `Present`, `Interested`, `Manually included`, and `Manually excluded` reasons.
- Manual overrides persist while draft inputs recalculate and do not modify Interest lists.
- The Curator reviews and approves one Audience per Moment or Loose item. An explicitly approved empty Audience is valid and shown as **Curator only**. The Curator never appears in a proposal because Curator authority already grants access.

### Publication, Staged updates, correction, and Withdrawal

- A draft Event can be published only after every Moment has an approved Audience.
- A published Event has at most one mutable Staged update. It is the net effect of source additions and removals, Curator edits, metadata suggestions, ordering, Moment structure and placement, and Audience changes.
- Changes that cancel before Publication leave no Staged residue.
- Publication atomically commits the full reviewed state. Partial Publication is forbidden.
- Recipients see one filtered Event and never see Moment boundaries, hidden counts, or gaps.
- Source removal remains staged while Immich can still serve the Media. If Immich cannot serve it, delivery stops immediately, the published listing remains marked unavailable until correction, and the Curator receives a delivery problem.
- A confirmed relink changes the Immich backing while preserving Memento Media identity, URL, placement, Audiences, Comments, Favorites, and history.
- Event and Moment reorganization is a staged correction. During an Event split, the Curator selects which result keeps the original Event identity.
- Withdrawal immediately blocks access to an Event, Moment, or Media item and preserves identity and history. Restoration requires a new Publication with newly reviewed Audiences.

### Recipient library

- Primary destinations are Photos, Events, and Favorites. Desktop uses a persistent rail and mobile uses bottom navigation.
- Photos is the landing page. **New for you** appears above the chronological library only when authorized unseen Publications exist. It is durable in-portal state, not an algorithmic feed.
- Photos, Events, and Favorites use dense justified rows based on real aspect ratios. Complete thumbnails are preserved on mobile.
- Photos supports explicit multi-selection for subset archives and other applicable bulk actions. Desktop makes selection primary and efficient; mobile supports it as a secondary interaction.
- Desktop provides a visible-date rail with hover, jump, and drag-to-scrub. Mobile receives an appropriate compact date navigation treatment.
- Event pages are seamless Curator-ordered galleries with no Moment hints or inaccessible totals.
- The near-fullscreen viewer exposes Favorite, original download, details, and item-level Comments. Desktop uses a side panel and mobile stacks interactions below the Media.
- Favorites MUST explain: **“Favorites aren't shared with other recipients.”**
- Interest-list editing lives in the avatar menu with Settings and Onboarding, not in primary navigation. Its guidance MUST explain that choosing People helps the Curator share relevant Event photos even when the Recipient did not attend.
- Every Curator flow MUST be available on mobile through focused drill-down surfaces. Desktop remains optimized for dense and bulk work.

### Curator workspace

- The primary desktop structure is a split-pane command center: work and Events on the left, active organization workspace in the center, and Attendance or Audience inspection on the right.
- Work ordering prioritizes delivery and privacy problems, then drafts and Staged updates, then new Source albums.
- Every draft and Staged update shows completed steps, remaining steps, progress, and next action.
- Staged-update review MUST show the resulting Event with additions, removals, moves, metadata edits, and access changes highlighted, plus a compact secondary list of those net changes.
- A persistent nonlinear checklist covers Media organization, Moments, Attendance, Audiences, and final review. It MUST NOT force wizard order.
- Ordinary edits autosave. Publication, Withdrawal, and other access-changing operations require explicit actions.
- Event Moment rows include Media, Attendance summary, and Audience summary. Desktop supports multiselect and drag or toolbar assignment. Mobile supports selection followed by explicit move.
- Final review MUST provide **Preview as Recipient**, a read-only authorization-filtered view. Preview cannot Comment, Favorite, change settings, or download. Its activity is Curator-audit-only and excluded from engagement.
- People management uses a searchable directory, relationship detail, desktop Visibility-circle membership matrix, and mobile filterable membership lists. Venn diagrams are not used.

## Data ownership

| Owner | Data |
| --- | --- |
| Immich | source album records, asset bytes, original metadata, derivatives, playback, face detections, face clusters |
| Memento | People, Recipient identity, relationships, Visibility circles, Interest lists, Source dispositions, portal Media identity, Events, Moments, Attendance, Audiences, Publications, current projections, Sessions, Comments, Favorites, notifications, engagement, search, audit |
| Browser | session credential cookie, transient CSRF token, preferences cached for UX, private HTTP Media cache, explicit downloaded files |
| Operator | configuration, secrets, SMTP account, VAPID keys, PostgreSQL backup schedule and backup storage, optional GeoIP database |

Memento stores normalized Immich metadata needed for reconciliation and display, but does not forward raw Immich DTOs to Recipients. Internal paths, library IDs, API keys, face IDs, and raw coordinates MUST NOT cross the Recipient API.

## Persistence inventory

This section defines logical records, constraints, and critical indexes. It intentionally does not provide full SQL.

All timestamps MUST use `timestamptz`. Durable domain identities SHOULD use UUIDs. Mutable rows MUST carry optimistic versioning or an equivalent lost-update guard where concurrent browser edits are possible. Foreign keys MUST state explicit delete behavior. Authorization and history records SHOULD be retained rather than cascaded away.

### Identity and access

| Records | Required constraints and indexes |
| --- | --- |
| `system_settings` | Singleton key. Stores setup-enabled state, connected Immich contract version, notification defaults, current security epoch, recovery-hold state, and the hash of the last applied recovery nonce. Setup and recovery transitions are row-locked. |
| `people` | Stable portal identity, display and sort names, archive and merge metadata. Index normalized names with `unaccent` and trigram support. A merge survivor cannot equal the source. |
| `person_roles` | Unique `(person_id, role)`; role limited to Curator and Recipient. A partial unique constraint permits only one current Curator. |
| `recipient_access_generations` | Unique `(person_id, generation)`, exactly one current generation at most. State captures pending, onboarding, completed, suspended, or revoked, with state timestamps. Access generation is immutable once ended. |
| `recipient_emails` | At most one current login email per Recipient generation and one case-insensitively unique normalized current email across the instance. Preserve change history without making old email login-capable. |
| `invitations` | Token hash unique, Recipient generation FK, issued, sent, expires, accepted, revoked, reminder timestamp. At most one live Invitation per generation. Index live expiration and reminder due time. |
| `login_challenges` | Code hash, normalized email lookup hash, purpose, attempts, expiry, consumed time. Index unconsumed expiry and rate-limit dimensions. Never store plaintext codes. |
| `sessions` | Unique credential hash, Person and access-generation FK, security epoch copied at creation, type, created, last activity, idle or absolute expiry, revoked time, label, browser/platform, IP and optional local GeoIP result. Index active hashes, Person active Sessions, and expiry. Validation requires the stored epoch to equal the singleton current epoch. No JWT payload exists. |
| `security_audit_events` | Append-only actor, subject, action, outcome, raw IP, user agent, Session, and safe structured metadata. Index subject/time, actor/time, action/time. Secrets and raw tokens are forbidden. |

### Family discovery

| Records | Required constraints and indexes |
| --- | --- |
| `family_relationships` | Relationship type, People pair, current/former state, audit fields. Parent-child is directed. Sibling and partner pairs use canonical ordering to prevent duplicates. Self-relationships are forbidden. Index both endpoints and current partner traversal. |
| `visibility_circles` and `visibility_circle_members` | Circle names unique among current circles. Membership unique by circle and Person. Index Person-to-circle and circle-to-Person traversal. |
| `interest_list_entries` | Unique current logical choice per Recipient and selected Person, with active/ineligible state. Recipient cannot select self. Index Recipient active choices and selected Person. |
| `interest_list_history` | Append-only before/after action, actor Person, reason including visibility loss, and timestamp. Index Recipient/time and selected Person/time. |

### Immich source and portal Media

| Records | Required constraints and indexes |
| --- | --- |
| `source_albums` | Unique Immich album UUID for the configured owner, normalized source summary, disposition, last seen, source-missing state, and reconciliation fingerprint. Index disposition, missing state, and reconciliation due time. |
| `source_album_memberships` | Unique `(source_album_id, immich_asset_id)` for the latest validated snapshot, first and last seen, consecutive absence count, and source fingerprint. Index asset ID and album scan generation. |
| `media_items` | Stable Memento identity, media type, normalized dimensions and capture metadata, availability, current backing reference, and timestamps. Stable URL uses this ID. Index capture time, availability, and type. |
| `media_backings` | History of Immich asset UUID, checksum, path and filename repair evidence, source fingerprint, active interval, and relink approval. Exactly one current backing per Media item. Immich path data is Curator-only. Index current Immich UUID and checksum for repair candidates. |
| `immich_person_links` | At most one current confirmed Immich person UUID per portal Person, state linked or needs-review, last seen data. Never unique by name. Index Immich person UUID and needs-review state. |
| `immich_face_anchors` | Small rotating repair sample with face UUID, asset UUID, checksum, bounds, last linked Immich person, and last seen. Index portal Person and face UUID. Never authorizes access. |
| `reconciliation_runs` and `reconciliation_findings` | Run status, before/after summaries, stable-set evidence, retries, and Curator-visible additions, removals, missing records, and relink candidates. Index run time, unresolved findings, Source album. |

### Drafting, Publication, and projections

| Records | Required constraints and indexes |
| --- | --- |
| `events` | Stable portal identity, lifecycle, current Publication pointer, and current Staged update pointer. Index lifecycle and Curator work priority. |
| `event_sources` | Unique Event and Source album association plus optional membership rules. Index Source album to Event. |
| `staged_updates` | At most one open update per published Event, version, readiness, and net-change summary. Draft Event content uses the same editable model before first Publication. |
| `draft_moments` | Event or Staged update FK, ordering, proposed day and timezone, split/merge provenance. Unique order per editable Event version. |
| `draft_media_placements` | Unique `(draft_moment_id, media_item_id)` unless intentional repeated placement is explicitly represented. Index Media item and ordering. Unassigned draft items are represented explicitly. |
| `attendance` | Unique Moment and Person confirmation with source manual or suggested-then-confirmed and Curator audit. Only confirmed rows feed proposals. Index Person and Moment. |
| `audience_proposals` and `audience_reasons` | Recipient generation, proposed inclusion, manual override, and all automatic reasons. Unique proposal per Moment and generation. Index Moment and Recipient. |
| `publications` | Immutable Event or Loose item revision, Curator, committed time, notify-recipients flag, and prior revision. Index Event/revision and committed time. |
| `published_event_revisions`, `published_moments`, `published_media_placements` | Immutable published metadata, Moment order, Media order, Place labels, and capture presentation. Unique revision-local ordering. Index Media item to published placements. |
| `audience_entries` | Immutable Publication Moment or Loose item, Recipient Person, and access generation. Unique content target and Recipient generation. Index Recipient generation to target and target to Recipient. Empty Audience is represented by approved target metadata with zero entries. |
| `current_published_events`, `current_published_placements`, `current_audience_entitlements` | Transactionally replaced projection of the latest non-withdrawn revision. Unique projection keys. Covering indexes begin with Recipient generation for authorization and with Event plus order for rendering. Media entitlement is the distinct union of current authorized placements. |
| `loose_items` and `published_loose_item_revisions` | Stable identity and the same draft, Audience, Publication, projection, and Withdrawal rules as Event content. |
| `withdrawals` | Append-only target, scope, Curator, reason, time, and superseding Publication if restored. Index currently withdrawn targets. |
| `publication_audit_events` | Append-only draft, approval, Publication, Audience change, correction, Withdrawal, and relink events. Index Event/time, target/time, actor/time. |

### Interactions, notifications, and activity

| Records | Required constraints and indexes |
| --- | --- |
| `comments` | Media item, author Person, body, created, edited, deleted or moderated state. Index Media/time and author/time. Authorization is evaluated at read time. |
| `comment_moderation_history` | Append-only old/new state and actor. Index Comment/time. |
| `comment_subscriptions` | Unique Media item and Recipient generation after that Recipient Comments; includes mute state. Access alone and Favorite do not create it. |
| `favorites` | Unique Recipient Person and Media item, current state and timestamps. Index Recipient current favorites and Media item for Curator view. No recipient aggregate index is exposed. |
| `notification_preferences` | One current email preference per Recipient, immediate, weekly, or none, plus weekly day, local time, and timezone. Curator operational preferences are separate. |
| `push_subscriptions` | Unique endpoint hash, encrypted endpoint and subscription material, trusted Session, Recipient, created, disabled, last success, and reconciliation time. Public-computer Session FK is forbidden. An active subscription requires an active linked trusted Session in the current security epoch. Index Recipient/device and stale reconciliation. |
| `activity_items` | Durable in-portal authorized Publication and Comment activity. Index Recipient/time and unread or unseen state. It remains available when external delivery is disabled or fails. |
| `new_for_you_entries` | Recipient generation and Publication, seen time. Unique pair. Contains only currently authorized Publication results. |
| `notification_batches` | Recipient, channel, coalescing bucket, status, scheduled time, and authorization snapshot inputs. Unique channel and coalescing key prevents duplicate batches. Index due batches. |
| `delivery_attempts` | Batch, provider result, safe diagnostic, attempt, and next retry. Index retry due and unresolved permanent failures. SMTP message bodies and tokens are not logged. |
| `invitation_suggestions` | Requester, submitted name/email/relationship note, spoken-to answer, status, withdrawal, Curator decision, optional matched Person. Index requester/time and Submitted status. It never creates access by itself. |
| `engagement_events` | Meaningful event type, Recipient, target, Session, and time. Index Recipient/time, target/time, and retention due time. Detailed rows retain one year. |
| `engagement_daily_aggregates` | Unique Recipient, date, and metric dimensions. Retained indefinitely. |
| `curator_activity_items` | Chronological denormalized pointers to Comments, Favorites, Publications, engagement, suggestions, security, and delivery failures, with Curator-only read state. Index time, category, and unread state. |

### Search, archives, and work infrastructure

| Records | Required constraints and indexes |
| --- | --- |
| `place_labels` | Portal-owned label and normalized text, attached to publishable content revision. Exact coordinates are not required and are never Recipient-visible through the label. Trigram and normalized text indexes support matching. |
| `published_search_documents` | Current-published Event, Media, Person-attendance, place, and date fields with Recipient authorization join keys. GIN indexes support text vectors; `pg_trgm` indexes support prefix and modest typo tolerance. Staged data is absent. |
| `archive_plans` | Random plan hash, Recipient, Session, expiry, selected scope, and consumed state. Index hash and expiry. |
| `archive_parts` and `archive_part_items` | Ordered parts and exact authorized Media or Live Photo companion IDs. Each part is individually single-use, has its own consumed time, and is reauthorized immediately before streaming. Unique plan and part order. |
| `jobs` | Type, payload, state, run time, attempts, lease owner and expiry, idempotency key, last safe error, and cancellation state. Index claim order on queued `run_at`, reclaimable leases, and type/status. Idempotency key is unique when present. |
| `outbox_events` | Domain event type, aggregate identity/version, payload, created, available, attempts, lease, and delivered time. Unique aggregate event identity. Index undelivered availability and expired leases. |

## Publication transaction and current-published projection

Publication is the central privacy transaction.

Within one PostgreSQL transaction, the Publication service MUST:

1. lock the Event and its editable version;
2. verify the submitted optimistic version and Curator authority;
3. verify every Moment has a reviewed Audience, including explicit empty approval;
4. reject suspended or revoked access generations while allowing Pending Recipients in current generations;
5. create an immutable Publication and immutable published revision records;
6. replace the Event's current-published metadata, placements, Audience entries, Recipient Media entitlement union, safe Recipient covers, New for you candidates, and published search documents;
7. apply any restoration or prior-revision supersession markers;
8. clear or roll forward the coalesced Staged update;
9. append audit records and transactional outbox events;
10. commit all changes together.

No Recipient request may observe a mix of old and new projection rows. Projection rows MUST be queryable directly without reconstructing draft history on every request. Authorization MUST first require the Session's security epoch to equal the singleton current epoch, then join the Session's Person and access generation to `current_audience_entitlements` and apply completed and non-suspended Recipient state plus Withdrawal and Media availability.

`Notify recipients` defaults on. If disabled, the transaction still creates New for you and Curator activity but marks external Publication delivery suppressed. Notification eligibility is based on newly accessible Event, Loose item, or additional Media in the net result. Metadata edits, reordering, removals, Withdrawal, relink, and corrections without newly accessible Media are externally quiet.

## Immich integration contract

### Supported v3.0.3 operations

Memento MUST use these stable REST contracts, relative to Immich `/api`:

| Need | Immich operation |
| --- | --- |
| Owned album discovery | `GET /albums?isOwned=true` |
| Album metadata | `GET /albums/{id}` |
| Complete album membership | paginated `POST /search/metadata` with `albumIds`, at most 1,000 results per page |
| Asset metadata | `GET /assets/{id}` |
| Thumbnail or preview | `GET /assets/{id}/thumbnail` |
| Range-capable video | `GET /assets/{id}/video/playback` |
| Original | `GET /assets/{id}/original` |
| Archive planning and streaming | `POST /download/info`, then `POST /download/archive` with server-derived IDs |
| Immich people | paginated `GET /people?withHidden=true` |
| Faces for repair evidence | `GET /faces?id={assetId}` |
| Version readiness | `GET /server/version` |

Album responses do not contain assets in v3.0.3. Memento MUST obtain membership through metadata search and follow pagination. It MUST NOT use shared links, browser-to-Immich calls, internal timeline APIs, the session-only sync stream, or WebSockets as a correctness source.

### Reconciliation contract

- Discovery and linked-album reconciliation run from one background process, not per browser or Recipient.
- A default schedule between 5 and 15 minutes is appropriate and MUST be configurable. Opening or reviewing an Event SHOULD trigger bounded on-demand reconciliation.
- Album summary timestamps and counts are hints, not complete change cursors.
- A pagination pass is a candidate set. Memento MUST deduplicate IDs, compare album summaries before and after, and retry when they change.
- A removal MUST appear in two consecutive stable, identical membership passes before it is staged. Failed, incomplete, summary-changing, or otherwise unstable passes never advance absence evidence. Any intervening validated set that differs resets the consecutive evidence. Additions MAY stage after one validated pass because they remain private.
- Reconciliation MUST be idempotent, bounded in concurrency, and use timeouts, exponential backoff, and jitter.
- The net diff is against the last published source revision and the current editable state, so add-then-remove before Publication leaves no residue.
- NAS changes are visible only after Immich scans the external library. Expected freshness is Immich scan delay plus Memento reconciliation delay.

### Identity and repair

Immich album and asset UUIDs are stable only for the lifetime of those Immich records. External-library moves and delete/re-import operations can create new identities.

- A new Immich UUID is initially a source add/remove, not an automatic relink.
- Checksum, path, filename, capture metadata, face IDs, bounds, and anchors MAY propose a replacement to the Curator.
- Only Curator confirmation relinks a Media item or portal Person.
- If an Immich person link disappears or anchors conflict, it enters needs-review and stops generating suggestions.
- Ordinary face reassignment and merge evidence MAY produce a high-confidence repair proposal, but never automatic authorization.
- Later Immich releases are unsupported until the version pin is changed after contract tests cover all listed operations, redirects, DTO normalization, pagination, Range behavior, archive expansion, and identity-repair assumptions.

## Media proxy and archive contract

- Every thumbnail, preview, playback, original, and archive request MUST authenticate and authorize before contacting Immich.
- Browser URLs contain only Memento IDs and same-origin routes.
- Memento MUST normalize metadata and allowlist response headers. It MUST never expose Immich API keys, direct URLs, source paths, owner IDs, internal library IDs, face data, or raw DTOs.
- Thumbnail redirects MAY be followed server-side with Immich authentication retained only when every redirect remains on the configured Immich scheme, host, and port. Cross-origin, credential-bearing, non-HTTP, loopback substitution, and unapproved redirect targets MUST be rejected without forwarding the API key.
- Video and original proxying MUST stream rather than buffer. `Range`, `If-Range`, `Content-Range`, `Accept-Ranges`, content type, length when known, disposition, ETag, and safe cache validators MUST be handled correctly.
- Protected responses MUST use private browser caching and MUST NOT be cacheable by shared proxies or a public CDN.
- Originals are returned unchanged, including EXIF and GPS metadata.
- An archive plan is bound to one Recipient and Session and expires after 15 minutes.
- Event archives include every currently accessible item in the Event. Subset archives validate the complete selected set server-side. Archive entries use sanitized Event-based paths and never source-library paths.
- Immich Live Photo companion expansion is permitted only for the current companion of an authorized item.
- A multi-part plan contains individually single-use parts. Each part MUST be reauthorized immediately before it starts streaming. Revocation, suspension, Withdrawal, Session revocation, and entitlement loss block every later part.
- An open stream MAY finish when safe interruption is not possible. Already downloaded files cannot be recalled.

## HTTP API conventions and route inventory

All application JSON and protected Media routes are under `/api`. Stable portal URLs MAY use non-API SPA paths.

### Conventions

- JSON uses `snake_case`, UTF-8, and explicit named request and response types generated to TypeScript with Tygo.
- IDs are opaque strings. Clients MUST NOT infer type or ordering from them.
- Timestamps are RFC 3339 UTC values. User-entered local dates and timezone identifiers remain explicit fields.
- List endpoints use cursor pagination with deterministic tie-breakers. Offset pagination MAY be used only for small Curator configuration lists.
- Mutations requiring conflict protection use an entity version or `If-Match` and return `409 Conflict` for stale state.
- Validation failures return `422`; authentication failures `401`; authenticated denial and hidden-resource access use a non-enumerating `404` where disclosure matters; rate limits return `429`; dependency unavailability returns `503` only when retry is appropriate.
- Errors use one stable `error` envelope with a machine-readable code, safe message, HTTP status code, field errors when applicable, and request ID. Stack traces and dependency bodies are never returned.
- Mutation endpoints that can be retried by browsers SHOULD accept an idempotency key.
- Free-text search queries MUST be sent in POST bodies or transient client state, not URLs.

### Route groups

| Group | Representative responsibilities |
| --- | --- |
| `/api/setup` | setup status and transactional first-Person creation |
| `/api/auth` | sign-in request, code verification, Invitation inspect and accept, sign-out, email verification and recovery |
| `/api/session` and `/api/sessions` | current Session and CSRF bootstrap, list, rename, revoke, sign out all |
| `/api/me` | Recipient profile, Onboarding, preferences, Interest list, push devices, engagement disclosure |
| `/api/people` | Curator People CRUD, archive, merge, discoverable Recipient directory |
| `/api/relationships` and `/api/visibility-circles` | Curator family graph and circle membership |
| `/api/recipients` and `/api/invitations` | designate Pending Recipient, access states, explicit send, revoke, reissue, suspend, restore |
| `/api/sources` and `/api/reconciliation` | Source album inbox, ignore, restore, missing state, runs, findings, repair |
| `/api/events`, `/api/moments`, and `/api/loose-items` | Curator drafts, Staged updates, organization, Attendance, ordering, metadata, places |
| `/api/audiences` | proposal reasons, manual overrides, approval, Recipient-safe entitlement views |
| `/api/publications` and `/api/withdrawals` | final review, publish, history, immediate Withdrawal, restoration staging |
| `/api/library` | Recipient Photos timeline, Events, New for you, collection statistics, seen state |
| `/api/media` | Recipient metadata, authorized thumbnail, preview, playback, and original streams |
| `/api/archives` | plan, inspect safe summary, and single-use part streams |
| `/api/comments` | create, edit, delete, moderate, mute, list authorized item Comments |
| `/api/favorites` | Recipient toggle and list, Curator per-Recipient view |
| `/api/search` | authorized portal-index search and structured date filters |
| `/api/invitation-suggestions` | Recipient submit, withdraw, status; Curator accept, match, reject |
| `/api/activity` | Recipient durable activity and Curator filtered activity |
| `/api/engagement` | Curator-only metrics and timelines |
| `/api/push` | VAPID public configuration, trusted-device subscription upsert, reconciliation, removal |
| `/api/health/live` and `/api/health/ready` | process liveness and dependency readiness |

Every route group MUST declare exactly one primary policy in route registration: public-safe, setup-only, token-authorized, authenticated Person, Recipient-self, Curator-only, or current-entitlement. Resource-level authorization MUST run again in service queries. Only current-entitlement routes require an Audience; public-safe routes are limited to health, setup status, and enumeration-resistant starts, while setup-only and token-authorized mutations enforce their dedicated state and token contracts.

## Authentication and security requirements

### Tokens and storage

- Session credentials and all Invitation, archive, unsubscribe, and recovery tokens MUST be generated from at least 256 bits of cryptographically secure randomness.
- Only a one-way hash of each opaque token is stored. A fast cryptographic digest such as SHA-256 is appropriate for high-entropy tokens. Email codes require a server-side pepper in addition to a hash because their search space is small.
- Token comparisons MUST be constant-time after indexed hash lookup where applicable.
- Tokens are purpose-bound, single-use where specified, and expire independently.
- Session credentials MUST NOT be JWTs and MUST contain no identity or authorization claims in browser-readable form.

### Cookie and CSRF

- The Session cookie MUST use a `__Host-` name, `Secure`, `HttpOnly`, `Path=/`, and `SameSite=Lax`. It MUST have no `Domain` attribute.
- Trusted-device cookies persist in line with server idle expiry. Public-computer cookies omit `Max-Age` and `Expires`.
- State-changing cookie-authenticated requests MUST include a server-issued CSRF token tied to the Session in a custom header. The server stores only its hash or derives it from Session-scoped secret material.
- CSRF tokens MUST rotate when identity, privilege, or Session changes. CORS MUST deny unapproved origins, and state-changing routes MUST reject simple cross-origin content types.
- GET and HEAD MUST be safe and non-mutating. Invitation acceptance, unsubscribe changes, sign-out, and push changes use POST or DELETE.

### Security headers

Caddy or Go MUST set, as appropriate:

- a restrictive Content Security Policy with `default-src 'self'`, no third-party font source, and narrowly declared Immich-independent push connection needs;
- `Strict-Transport-Security` when HTTPS is operator-confirmed;
- `X-Content-Type-Options: nosniff`;
- `Referrer-Policy: no-referrer` for token flows and at least `strict-origin` elsewhere;
- `frame-ancestors 'none'` and `X-Frame-Options: DENY`;
- a restrictive `Permissions-Policy`;
- `Cross-Origin-Opener-Policy` and compatible resource policies where they do not break PWA behavior.

Protected Media responses MUST set explicit private cache policy. Authentication and token responses MUST use `Cache-Control: no-store`.

### Rate limits

Single-instance in-memory limiters are acceptable for burst control, while security event persistence remains in PostgreSQL. Limits MUST be configurable and keyed by both normalized identity dimension and client IP where applicable.

Initial defaults SHOULD enforce:

- no more than three sign-in emails per normalized email in 15 minutes;
- no more than ten sign-in starts per IP in 15 minutes;
- five verification attempts per challenge, then consume it;
- bounded Invitation acceptance, setup, recovery, email-change, archive-plan, Comment-write, and search bursts;
- per-Session concurrent archive and original-stream limits to protect Immich and memory.

Responses MUST remain non-enumerating. Trusted proxy configuration MUST be explicit before forwarded IP headers are accepted.

### Logging, audit, and privacy

- Use structured golib logging and request IDs.
- Routine logs MUST exclude cookies, authorization headers, token query values, email codes, SMTP credentials, VAPID secrets, Immich keys, archive IDs, raw free-text search, Comment bodies, and raw dependency responses containing private metadata.
- Safe identifiers MAY be logged in Curator-only audit storage, not necessarily in general logs.
- MVP security audit retains raw IP data indefinitely. Detailed engagement retains one year; daily aggregates retain indefinitely.
- No Google Analytics, third-party analytics, email-open tracking, or tracking pixel is permitted.
- Optional Session location comes only from an operator-provided local GeoIP database.

## PostgreSQL jobs and outbox

The worker runs in the Go process and uses PostgreSQL only.

### Claim and lease

1. In a short transaction, select due queued jobs ordered by priority and time using `FOR UPDATE SKIP LOCKED`.
2. Mark claimed rows running with a unique worker lease owner and bounded lease expiry.
3. Commit before doing external work.
4. Heartbeat long jobs by extending the lease while ownership still matches.
5. Complete or retry with compare-and-set on lease owner. Expired leases are reclaimable.

Delivery is at least once. Every handler MUST be idempotent through natural uniqueness, an idempotency key, an aggregate version, or a recorded effect. Retries use bounded exponential backoff with jitter. Permanent failure moves to a failed state and creates a Curator-visible work item. Reconciliation, delivery assembly, SMTP sends, push sends, Invitation reminders, retention aggregation, and cleanup are job types.

### Transactional outbox

Any domain transaction that requires external work MUST insert its outbox event in the same transaction. The dispatcher claims outbox rows with the same lease pattern, creates or updates idempotent jobs or delivery batches, and marks the event delivered only after durable handoff. Direct SMTP or push calls inside a Publication transaction are forbidden.

Shutdown stops new claims, cancels context-aware work, allows a bounded drain, and leaves unfinished leases to expire. No job may remain permanently running because the process stopped.

## Notifications, Comments, Favorites, and engagement

### Matching email and push behavior

- Email and push expose matching authorized Publication and Comment activity.
- Immediate email and push use the same 15-minute coalescing window. A Recipient with both enabled receives one coalesced email and one coalesced push for the same qualifying window, subject to channel delivery limits.
- Email and push are independent preferences. A Recipient may enable either, both, or neither without changing access.
- Push is immediate only. Email is Immediate, Weekly, or None. Immediate is the default after Onboarding.
- Weekly defaults to Sunday at 9:00 AM in a Curator-configurable platform timezone. Each Recipient can override day, time, and timezone.
- Required identity and security email ignores optional preferences.
- No optional delivery occurs before Onboarding completion, and completion sends no backlog.
- Every batch rechecks current access when assembled and immediately before each channel send. Deleted Comments and inaccessible content are omitted.
- A Publication-level **Notify recipients** off switch suppresses email and push for every affected Recipient, while New for you remains.

Email content MAY include one metadata-stripped authorized cover up to 480 pixels for immediate mail and up to three for weekly mail. These bytes are embedded in the message, not served by a public URL. Onboarding MUST explain that an embedded preview becomes a permanent low-resolution mailbox copy that can be forwarded and cannot be recalled after Withdrawal or Revocation. The message MUST explain no hidden counts or Moments. Email links require a normal Session and authorization.

Push text MAY include an authorized Event title, authorized addition count, or Comment author and action. It MUST contain no thumbnail, face data, hidden count, or reusable Media URL. UX MUST warn that lock screens can display the text.

The Curator has independent email and per-device push controls for new Comments, Invitation suggestions, and delivery failures. Favorites remain in-app only. Security-critical information and delivery failures remain in the Curator work queue when external operational delivery is disabled.

### SMTP behavior

- MVP uses generic SMTP with authenticated transport where supported.
- Production SMTP MUST use certificate-verified implicit TLS or STARTTLS without plaintext downgrade. Plaintext SMTP is forbidden unless the operator explicitly enables an insecure-development option for a loopback or trusted private-network test server; startup and health output MUST warn while that option is active.
- Temporary synchronous failures retry with backoff for up to 24 hours.
- A synchronous permanent recipient rejection disables optional email and creates a Curator-visible delivery problem.
- Generic SMTP cannot promise asynchronous bounce or complaint detection. Provider-specific asynchronous bounce and complaint adapters are deferred. If a future adapter reports a hard bounce or complaint, it disables optional email without changing access.
- Restoring delivery does not send missed notifications.
- Optional email includes one-click unsubscribe. The signed preference page can set None, Immediate, or Weekly and configure schedule without a Session.

### Web Push

- Use the Push API, Notifications API, service worker, VAPID, and HTTPS. No Apple developer account or Firebase project is required by Memento.
- Push can be enabled only through an explicit Recipient action on the current trusted device.
- Subscription endpoints MUST use HTTPS, contain no URL credentials, and resolve only to public unicast addresses. Each connection and redirect MUST re-resolve and reject loopback, private, link-local, multicast, metadata-service, and other non-public targets. Redirects are disabled by default; if supported, every target receives the same validation and no Memento credential is forwarded.
- iPhone and iPad guidance requires Add to Home Screen, launch of the installed PWA, then the enable action. Android uses feature detection and need not require installation.
- Browser permission prompts MUST NOT appear on page load.
- Reconcile browser subscription state on authenticated launch. Remove terminal 404 or 410 endpoints without changing email preference.
- Assembly and send MUST require an active linked trusted Session in the current security epoch. Session revocation, Session expiry, sign out all, suspension, or Revocation disables the linked subscription before any later send.
- Push is best-effort. Durable New for you and activity state remain authoritative.

### Comments

- Comments are chronological and item-level.
- Authors may edit or delete their own Comments. The Curator may remove any Comment. Moderation history is Curator-only.
- The Curator receives all new Comment activity according to operational settings.
- A Recipient subscribes to future Comment activity for an item after they Comment and may mute it.
- Do not notify an author about their own Comment or notify anyone about edits and deletions.
- Losing access hides the item and Comments and stops delivery. Earlier Comments remain attributed and visible to the Curator and currently authorized Recipients. Reauthorization restores visibility without a backlog.

### Favorites

- A Favorite is visible only to its Recipient and the Curator.
- Favorites persist through suspension, revocation, Withdrawal, and temporary access loss, but inaccessible Media cannot be browsed by the Recipient.
- Favorite additions and removals appear in Curator activity and MAY be grouped into bursts. They never generate email or push.
- Other Recipients never receive Favorite state or aggregate counts.

### Engagement

Meaningful first-party activity includes authenticated visits, Sessions, opening primary destinations, Events, or the Media viewer, starting video, downloads, Comments, Favorite changes, and Invitation suggestions. Curator Recipient lists MUST prominently show each Recipient's latest meaningful authenticated activity.

It excludes background and service-worker traffic, prefetches, incidental thumbnail loading, email opens, and Preview as Recipient. The Curator can inspect latest meaningful activity, active days for 7, 30, and 90 days, visit frequency, explicitly opened Events and Media, downloads, Comments, Favorites, per-Recipient timelines, and Recipients who explicitly opened a Media item.

Recipients MUST be told during Onboarding that the Curator can see this usage. Other Recipients cannot see engagement.

### Invitation suggestions

- A Recipient can submit name, email, free-form relationship note, and a required Yes or No answer to whether they already spoke with the person.
- Submission creates no Person, Pending Recipient, Invitation, Session, or access.
- The requester sees Submitted, Accepted, or Rejected and may withdraw while Submitted. They never see resulting Invitation or Onboarding state.
- The Curator can reject, match an existing Person, or accept by selecting or creating a Person and then separately creating Recipient access and optionally sending the Invitation.
- Suggestions create Curator in-app activity and optional operational email or push.

## Search requirements

MVP provides one authenticated Recipient search across Photos and Events from the desktop header and a dedicated mobile action. Interest-list People search remains separate.

- Index Event titles and descriptions, Media capture dates, Curator-approved Place labels, and discoverable People.
- Do not index filenames, camera details, raw EXIF, Immich tags, Comments, Favorites, faces, or raw coordinates for Recipient search.
- Dates support year, month, exact date, and explicit range. A Media item matches its capture date, and an Event matches its current published date range.
- Text matching is case-insensitive and diacritic-insensitive with token and prefix matching plus modest typo tolerance for longer terms. Use PostgreSQL `unaccent`, text search, and `pg_trgm`.
- Person matching is limited to People discoverable through current shared Visibility circles and returns only authorized Moments with confirmed Attendance. Results may say a Person attended but MUST NOT imply they appear in every result.
- Results group by Event plus Shared separately. Global Photos results deduplicate a Media item; Event-specific reuse remains visible in each authorized Event.
- Authorization filters candidates before matching, ranking, grouping, faceting, covers, previews, or counts.
- Every total, span, cover, preview, and count includes authorized Media only. Never show partial totals, hidden facets, gaps, Moment boundaries, original totals, or unavailable-result hints.
- Unpublished and Staged data is absent. Publication changes search and access in one transaction. Withdrawal and entitlement loss remove results immediately.
- Source-missing published listings MAY remain searchable while clearly unavailable.
- A no-result response reveals only that nothing in the shared collection matched.
- Memento MUST NOT retain Recipient search history, send queries to third parties, or put free-text queries in routine logs or URLs.

## Authorization and privacy invariants

The following are release-blocking invariants:

1. Curator authority and Recipient Audience authority are separate code paths.
2. A Pending Recipient has no Media, search, Comment, archive, New for you, email, or push access before Onboarding completion.
3. Audience entries reference an access generation. Revocation makes every old entry permanently unusable.
4. Suspension blocks all Recipient access without destroying Audience history.
5. Visibility circles and Interest lists affect discovery and proposals only.
6. Attendance and faces affect proposals only. Curator confirmation is required even for Attendance.
7. Recipient Event rendering is the union of authorized Moments with no Moment boundary, gap, count, cover, or order leakage.
8. A Media item reused across Events is accessible if at least one current-published placement authorizes it.
9. Search filters authorization before all observable computation.
10. Comments are visible only to the Curator and people currently authorized for the Media item.
11. Favorites have no cross-Recipient visibility.
12. Preview as Recipient is read-only and cannot create Recipient interactions or downloads.
13. Notification assembly and send both reauthorize. Delivery preferences never grant access.
14. All archive plan parts are Session-bound, Recipient-bound, expiring, individually single-use, and reauthorized.
15. No browser receives an Immich credential, source path, direct private URL, face identifier, or hidden result count.
16. No public or reusable Media link exists.

## Operational requirements

### Startup and migrations

- Startup validates configuration without printing secrets, connects to the Memento database, verifies required extensions, applies or validates Bun migrations, validates Immich v3.0.3, then starts readiness.
- `MEMENTO_RECOVERY_NONCE=<fresh-random-value>` explicitly enters restored-database recovery. The operator MUST generate a new nonce for every restore and MUST NOT reuse a value from configuration backups.
- Before HTTP traffic, job claims, outbox dispatch, or readiness begin, one transaction locks `system_settings`. When the supplied nonce hash differs from the last applied hash, the transaction sets recovery hold, replaces the current security epoch with fresh cryptographic randomness, stores the nonce hash, and records an audit event. Restarting with the same nonce is idempotent, while a newly supplied nonce always rotates the epoch even when the restored snapshot already records an active hold.
- While recovery hold is active, Recipient content, optional email, Web Push, notification assembly, and delivery workers fail closed. Only liveness, restricted recovery status, email-code authentication, and Curator recovery-review operations are available. General readiness reports the recovery restriction without exposing private state.
- The operator removes the recovery nonce after the hold is recorded. Only a Curator authenticated by a fresh email code in the new epoch may explicitly lift the persisted hold after review. A normal restart MUST NOT clear it.
- Every Session authentication query MUST compare the Session row's epoch with the current singleton epoch. Every push assembly and send MUST perform the same check through its linked Session.
- Migrations run once under a PostgreSQL advisory lock. A failed migration MUST not be marked applied.
- Planned downtime during upgrade is acceptable. Operators SHOULD stop traffic and the single app instance before schema-changing upgrades.

### Health checks

- `/api/health/live` reports only process and event-loop liveness and does not call dependencies.
- `/api/health/ready` verifies PostgreSQL with a short timeout, migration compatibility, setup-state consistency, worker heartbeat freshness, and supported Immich version and basic reachability. SMTP and push provider outages SHOULD degrade readiness detail but SHOULD NOT make the photo library unavailable.
- Health payloads expose no secrets, recipient data, internal URLs, or raw dependency errors.

### Graceful shutdown

On SIGTERM or SIGINT, the process MUST:

1. become unready;
2. stop accepting new requests and job claims;
3. cancel reconciliation, outbound requests, and other context-aware work;
4. allow a configurable bounded drain for normal HTTP and streams;
5. release or allow expiry of worker leases;
6. close database connections and exit within the container stop timeout.

### Backups and restore boundary

- The Memento database is the complete Memento backup boundary. Immich Media and Immich's own database remain under Immich's independent backup plan.
- Operators SHOULD run a daily PostgreSQL backup for a 24-hour recovery point objective and recovery within several hours.
- Memento documents `pg_dump` and `pg_restore` but does not schedule, retain, encrypt, copy, or test backups for the operator.
- Operators SHOULD store backups outside the PostgreSQL container and periodically test restore into a separate logical database.
- The Go binary MUST provide a read-only restore-validation operation for a candidate database. It checks migration compatibility, required extensions, setup singleton and sole-Curator constraints, foreign keys, current projections, security settings, and representative record counts without serving HTTP, claiming work, contacting delivery providers, or mutating the candidate.
- VAPID private keys, SMTP credentials, Immich API keys, configuration, and optional GeoIP files require a separate secrets/configuration backup.
- Restoring Memento can resurrect Sessions and authorization state that changed after the recovery point. The application MUST support a recovery hold that blocks all Recipient access, Web Push, and optional email after restore. Entering recovery hold rotates the current security epoch so every restored Session and linked push subscription becomes invalid. The Curator signs in with a fresh email code, reviews restored Recipient access, Audiences, Withdrawals, and Publications, and explicitly lifts the hold before Recipient readiness returns.
- Restoring Memento does not restore Media that Immich can no longer serve.

### Availability and dependency failure

- PostgreSQL loss makes the application unready.
- Immich loss keeps safe Curator diagnostics available where possible but protected Media delivery fails closed. Cached browser copies may remain available according to normal private cache behavior.
- SMTP or push loss does not change access and leaves durable in-portal state intact.
- Source missing and delivery failures are prioritized in the Curator work queue.

## Baseline performance targets

Targets assume one app instance, 100,000 indexed Media items, 50 Recipients, a healthy local PostgreSQL connection, warm application process, and normal pagination. They are initial service-level objectives to validate and tune, not claims about Immich capacity.

| Operation | Baseline target |
| --- | --- |
| Liveness response | p95 under 50 ms |
| Readiness response | p95 under 500 ms with healthy dependencies |
| Session validation plus simple authorization query | p95 under 50 ms |
| Recipient timeline or Event page, up to 100 items | p95 under 300 ms server time |
| Curator work queue or People list | p95 under 300 ms server time |
| Authorized search first page | p95 under 500 ms server time |
| Comment, Favorite, preference, or seen-state mutation | p95 under 300 ms server time |
| Audience proposal recalculation for 50 Recipients and 500 Moment items | p95 under 1 second after source data is local |
| Publication of an Event revision with 5,000 placements and 50 Recipients | p95 under 3 seconds, all-or-nothing |
| Eligible job start after `run_at` | p95 under 60 seconds under normal load |
| Notification availability | coalescing closes at 15 minutes, then p95 dispatch starts within 2 minutes |
| Full 100,000-item reconciliation | completes within 30 minutes under configured bounded concurrency, excluding Immich scan delay |
| Media proxy first-byte overhead | p95 under 150 ms excluding Immich response time and network transfer time |
| Media proxy memory | bounded streaming buffer, target no more than 1 MiB per active stream excluding protocol libraries |

Proxy overhead is measured from Memento request receipt to upstream request dispatch plus Memento processing after the first Immich byte. It explicitly excludes Immich processing, storage access, and network time. Memento MUST not buffer full Media to improve these numbers.

Performance tests MUST report dataset shape, cold or warm cache state, PostgreSQL plan, Immich latency injection, and concurrency. Targets may be revised only with measured evidence and an updated specification.

## Test strategy

### Unit tests

- Domain state transitions for Recipient access generations, Invitations, Onboarding, suspension, revocation, Publication, Withdrawal, Staged update coalescing, and source-missing behavior.
- Person merge gates covering the sole Curator, two current Recipient generations, explicit transfer, email conflicts, Session invalidation, and preservation of distinguishable authorization history.
- Audience proposal reasoning, manual override stability, Family branch traversal, parent-child cycle rejection, Visibility-circle eligibility, and Interest-list deactivation.
- Authorization matrix functions covering roles, Sessions, access generations, placements, Withdrawals, and Media reuse.
- Notification eligibility, 15-minute coalescing, weekly scheduling across timezones and daylight transitions, self-Comment suppression, quiet changes, and preference independence.
- Search normalization, date parsing, safe grouping, cover selection, and no-result behavior.
- Token hashing, expiry, attempts, Session idle expiry, CSRF validation, rate-limit keys, and log redaction.
- Worker lease, retry, idempotency, and shutdown state machines.

### PostgreSQL integration tests

Tests MUST run against real supported PostgreSQL, not an in-memory substitute.

- Apply every migration from empty, verify required extensions, and test supported rollback policy.
- Exercise constraints, partial uniqueness, foreign-key behavior, indexes, transaction isolation, advisory migration lock, and concurrent setup winner.
- Prove Publication projection and outbox atomicity with injected failures at every transaction step.
- Prove job claims do not duplicate a lease under concurrent workers and expired leases recover.
- Use `EXPLAIN (ANALYZE, BUFFERS)` on representative 100,000-item authorization, gallery, search, and Curator queries.

### Immich contract tests

A disposable Immich v3.0.3 instance with fixtures MUST test:

- version gate and least-privilege API key permissions;
- owned album discovery and paginated metadata membership;
- changing summaries during pagination and two-pass removal handling, including proof that failed, incomplete, unstable, or differing intervening passes reset or do not advance removal evidence;
- DTO normalization and forbidden metadata stripping;
- thumbnail redirects with retained authentication;
- video and original byte ranges and safe response headers;
- archive planning, Live Photo expansion, multi-part authorization, and single use;
- missing assets, deleted albums, identity churn, person merge, face reassignment, and repair proposals;
- fail-closed behavior for 401, 403, 404, 429, timeout, malformed body, and unavailable derivatives.

The same suite gates every proposed Immich upgrade before the contract version changes.

### HTTP and security tests

- Route-policy tests for every group and resource-level non-enumeration.
- Cookie flags, CSRF rotation and rejection, CORS, content types, security headers, cache headers, and safe GET behavior.
- Email and token links do not mutate on GET and do not leak through referrers or logs.
- Rate limits work by IP and normalized email without account disclosure.
- Property-based or table-driven tests generate combinations of Recipient state, Audience generation, Session state, Withdrawal, source availability, and placement reuse.
- Fuzz JSON binding, archive selections, Range headers, redirect handling, and normalized Immich responses.
- Use a controlled resolver and dialer to prove push endpoint validation rejects DNS rebinding between validation and connection, including public-to-loopback, private, link-local, and metadata-service answer changes.
- Inject every readiness dependency failure and assert an allowlisted health schema that excludes database connection strings, Immich URLs, Recipient data, and raw dependency errors.

### Frontend tests

- Vitest and React Testing Library cover state rendering, accessible controls, permission-safe empty states, nonlinear progress, and responsive component behavior.
- Generated Tygo types are rebuilt in CI and the frontend cannot compile against hand-maintained duplicate wire types.
- Playwright covers Chromium and Firefox desktop and mobile viewports, Invitation and Onboarding, trusted and public-computer Sessions, Curator publication, Preview as Recipient, Recipient galleries, search, Comments, Favorites, archives, and push capability states. Public-computer coverage MUST assert that push enrollment is unavailable, sign-out remains prominent, and every original, Event archive, and subset archive surface shows the shared-computer warning.
- Automated accessibility checks cover keyboard navigation, focus management, labels, contrast, dialogs, reduced motion, and screen-reader names.
- Service-worker tests prove protected Media is not added to offline caches.
- Network assertions prove no runtime Google Fonts or analytics request.

### Delivery tests

- Use a local SMTP test server to assert required versus optional email, message coalescing, embedded preview limits, unsubscribe, retry, and synchronous failures.
- Use a Web Push test endpoint or standards-compatible fixture for payload encryption, VAPID, terminal subscription cleanup, and channel independence.
- Reauthorize immediately before both channel sends under concurrent revocation and Withdrawal. For partially invalidated batches, assert the exact surviving Publication and Comment identities, titles, counts, authors, and preview bytes, and require enabled email and push to represent the same surviving activity set.

### Performance and operations tests

- Seed 100,000 Media items, large Events, 50 Recipients, overlapping Audiences, Comments, Favorites, and search documents.
- Measure all baseline targets at steady state and during reconciliation, Publication, and notification assembly.
- Test graceful shutdown during HTTP requests, streams, job claims, reconciliation, SMTP, and push.
- Perform a documented `pg_dump` and `pg_restore` drill into a separate Memento database and verify migrations, counts, Sessions revocation expectations, Publications, interactions, and search consistency.
- Instrument recovery startup to prove no non-liveness HTTP route, job claim, outbox dispatch, notification assembly, or delivery starts before hold persistence and security-epoch rotation. Assert that repeating the same recovery nonce does not rotate again, a fresh nonce always rotates even when the restored snapshot already contains a hold, and normal restart never clears the hold.

## Acceptance scenarios

### Transactional setup

**Given** setup is enabled, no Curator exists, and two browsers have verified setup email challenges\
**When** both browsers submit final first-time Onboarding concurrently\
**Then** exactly one transaction consumes its challenge, creates one Person with Curator and Recipient roles, disables setup, and the other request receives a conflict without creating a Person.

### Setup disabled

**Given** first-time setup committed\
**When** any browser or unauthenticated caller revisits setup\
**Then** Memento reveals only that setup is unavailable and cannot create another Curator.

### Person merge cannot union authority

**Given** two People include the sole Curator, two current Recipient access generations, or conflicting login emails
**When** the Curator previews and attempts a merge
**Then** Memento requires the Curator Person to survive, rejects two current access generations, requires explicit transfer and email resolution where allowed, invalidates both People's Sessions, and never unions roles or current Audience authority implicitly.

### Pending Recipient preapproval

**Given** the Curator designates a Person as a Pending Recipient but has not sent an Invitation\
**When** the Curator approves that current access generation in a Moment Audience and publishes\
**Then** the Publication commits, but the Pending Recipient cannot sign in, access Media, appear in delivery batches, or receive optional notifications.

### Onboarding unlocks current Audiences

**Given** a Pending Recipient accepted an Invitation and has preapproved current-published Audiences\
**When** they explicitly complete Onboarding\
**Then** those current Audiences become accessible immediately, New for you reflects authorized published material, and no historical email or push backlog is sent.

### Revocation prevents silent restoration

**Given** a Recipient has old Audience entries and active Sessions\
**When** the Curator revokes access and later reinvites the same Person\
**Then** old Sessions fail, the new access generation does not match old Audience entries, and access returns only after new explicit Audience approval and Onboarding rules.

### Visibility does not authorize

**Given** a Recipient can discover a Person through a Visibility circle and adds them to an Interest list\
**When** no current approved Audience includes that Recipient\
**Then** no Event, Media, search result, count, Comment, or notification becomes accessible.

### Faces do not authorize

**Given** Immich detects a linked face in a Moment\
**When** the Curator has not confirmed Attendance and approved an Audience\
**Then** the face appears only as a Curator suggestion and no Recipient access changes.

### Atomic Publication

**Given** a Staged update changes metadata, placements, Audience entries, search documents, and notifications\
**When** a database failure occurs before commit\
**Then** Recipients continue seeing the prior complete projection and no outbox event or partial notification escapes.

### Empty Audience

**Given** a Moment has an explicitly approved empty Audience\
**When** the Event is published\
**Then** Publication succeeds, the Curator sees Curator only, and Recipients receive no hint that the Moment exists.

### Filtered Event privacy

**Given** an Event has three Moments and a Recipient is authorized for only two\
**When** the Recipient opens the Event\
**Then** they see one seamless ordered gallery using only accessible Media, an accessible cover, and counts computed only from that Media, with no Moment boundary or hidden gap.

### Search privacy

**Given** inaccessible Media matches a query and accessible Media does not\
**When** a Recipient searches\
**Then** the response says nothing in their shared collection matched and reveals no hidden total, Person, place, Event, cover, or facet.

### Source removal is staged

**Given** a published Media item is removed from a Source album but Immich still serves it\
**When** reconciliation confirms the removal twice\
**Then** Memento stages a removal and keeps the current published representation until Curator Publication.

### Source missing fails closed

**Given** a published Media item can no longer be served by Immich\
**When** a Recipient requests its thumbnail or original\
**Then** Memento does not serve stale server-side bytes, marks the listing unavailable, and raises a Curator problem without silently rewriting the Publication.

### Relink preserves portal identity

**Given** Immich re-imports a source file under a new asset UUID\
**When** the Curator confirms a repair proposal\
**Then** the Memento Media ID, URL, placements, Audiences, Comments, Favorites, and history remain unchanged while the backing changes.

### Range proxy authorization

**Given** a valid trusted Session and current Media entitlement\
**When** the browser requests a video byte range\
**Then** Memento reauthorizes, forwards the Range request, streams the valid partial response, and exposes no Immich URL or credential.

### Multi-part archive revocation

**Given** an authorized archive plan has three individually single-use parts\
**When** part one finishes and the Curator revokes the Recipient before part two begins\
**Then** parts two and three fail authorization, and replaying part one is rejected as already consumed.

### Matching notification channels

**Given** a completed Recipient has Immediate email and device push enabled\
**When** qualifying Publication and Comment activity occurs within one 15-minute window\
**Then** one email batch and one push batch contain matching currently authorized activity, each subject to its independent channel preference and delivery result.

### Quiet correction

**Given** a Publication only reorders Media and corrects metadata\
**When** the Curator publishes with notifications enabled\
**Then** the correction appears in current content and Curator activity but generates no optional email or push.

### Comment authorization

**Given** a Recipient previously Commented on a Media item and then loses access\
**When** another authorized Recipient Comments\
**Then** the first Recipient receives no activity and cannot read the item or thread, while their prior Comment remains attributed for the Curator and currently authorized Recipients.

### Favorite privacy

**Given** two Recipients can access the same Media item\
**When** one marks it as a Favorite\
**Then** only that Recipient and the Curator can see the Favorite, with no shared count or external notification.

### Preview is read-only

**Given** the Curator enters Preview as Recipient\
**When** they browse an Event and open Media\
**Then** Recipient authorization filtering applies, interaction and download controls are disabled, and the actions do not create Recipient engagement.

### Session revocation disables push

**Given** a trusted Session has an active Web Push subscription\
**When** the Recipient revokes that Session or signs out all Sessions\
**Then** the linked subscription is disabled before any later batch sends, while the Recipient's email preference remains unchanged.

### Outbound redirects fail closed

**Given** Immich returns a thumbnail redirect to another origin or a Recipient submits a push endpoint that resolves to a private address\
**When** Memento validates the outbound request\
**Then** it rejects the request without forwarding an Immich key, making the private connection, or disclosing the target response.

### Generic SMTP limitation

**Given** SMTP accepts a message and later the provider learns of a complaint\
**When** no provider-specific adapter is installed\
**Then** Memento does not claim complaint detection; optional email changes only from synchronous results, one-click unsubscribe, Recipient action, or future adapter input.

### Backup boundary

**Given** the operator backs up only the Memento logical database with `pg_dump`\
**When** it is restored into a clean Memento database and the same configuration and secrets are supplied\
**Then** portal state is recoverable, while Media availability still depends on the independently operated Immich instance.

### Restored authorization is held

**Given** the operator restores a database backup that may predate access changes, including one captured during an earlier Recovery hold
**When** Memento starts with a fresh recovery nonce
**Then** it rotates the security epoch before serving non-liveness traffic or claiming work, rejects every restored Session, disables linked push, blocks Recipient access and optional delivery, and remains held until a freshly authenticated Curator reviews state and explicitly releases it.

### Graceful worker recovery

**Given** a worker holds a reconciliation lease\
**When** the process receives SIGTERM and cannot finish before the drain deadline\
**Then** it exits without marking success, the lease expires, and the next process safely reclaims the idempotent job.

## Dependency-ordered implementation phases

### Phase 1: repository and runtime foundation

- Bootstrap pinned Go and frontend toolchains, manifests, lockfiles, linting, formatting, tests, Tygo, Vite, Caddy, and one production image.
- Add Koanf configuration, golib logging and middleware, Echo, Bun PostgreSQL connection, migrations, health routes, request IDs, error documents, graceful shutdown, and CI.
- Add the MIT license before the first source release.

### Phase 2: PostgreSQL identity and setup

- Add required extensions and core identity migrations.
- Implement transactional first-browser setup, Person roles, Recipient access generations, normalized email, passwordless codes, opaque hashed Sessions, cookies, CSRF, rate limits, and security audit.
- Add trusted and public-computer Session management and optional local GeoIP lookup.

### Phase 3: family model and Recipient administration

- Implement People, merge and archive, relationships, Family branches, Visibility circles, Interest lists and audit.
- Implement Pending Recipient designation, Invitation send, acceptance, reminder, Onboarding, suspension, revocation, email change, and recovery.

### Phase 4: Immich connector and source inventory

- Implement the pinned v3.0.3 client, version readiness, least-privilege validation, DTO normalization, Source album inbox, asset metadata, people and face repair inputs.
- Add leased jobs, outbox, scheduled reconciliation, two-pass removals, source-missing findings, and repair proposals.
- Complete disposable Immich contract tests before publication work depends on the connector.

### Phase 5: curation and Publication core

- Implement Events, Moments, Loose items, day proposals, ordering, Attendance, explained Audience proposals, manual overrides, draft autosave, Staged updates, immutable Publications, projections, search-document writes, audit, correction, Withdrawal, and relink.
- Add atomicity and authorization integration tests before any Recipient gallery is exposed.

### Phase 6: Recipient library and protected Media

- Build the selected Photos library, Events, Favorites navigation shell, New for you, justified galleries, date navigation, lightbox, details, and responsive PWA.
- Implement protected thumbnail, preview, video Range, original, archive-plan, and multi-part archive routes with private caching.
- Implement Curator split-pane workspace and Preview as Recipient.

### Phase 7: search and interactions

- Add published authorization-first search with `unaccent` and `pg_trgm`.
- Add Comments, moderation, Comment subscriptions and mute, Favorites, Invitation suggestions, Curator activity, meaningful engagement, retention aggregation, and Recipient disclosures.

### Phase 8: email and Web Push

- Add generic SMTP, required email, optional preferences, 15-minute immediate coalescing, weekly schedules, embedded safe previews, unsubscribe, retries, and failures.
- Add VAPID, trusted-device subscriptions, PWA permission guidance, matching 15-minute push batches, reconciliation, and invalid-subscription cleanup.
- Test reauthorization at assembly and send for both channels.

### Phase 9: hardening and release readiness

- Execute security, accessibility, cross-browser, load, failure-injection, graceful-shutdown, migration, backup, and restore tests.
- Validate all performance baselines with the 100,000-item and 50-Recipient fixture.
- Complete operator configuration, upgrade, Immich compatibility, PostgreSQL provisioning, and recovery documentation.

Each phase depends on the preceding privacy and persistence contracts. UI shortcuts MUST NOT precede server-side authorization or transaction guarantees.

## Traceability appendix

### Decision tickets

- [Validate the Immich v3.0.3 media integration contract](https://github.com/robinjoseph08/memento/issues/2): pinned REST operations, reconciliation, source identity, proxy, and no second media library.
- [Validate Immich v3.0.3 person and face identity behavior](https://github.com/robinjoseph08/memento/issues/3): portal-owned People, advisory faces, repair anchors, and no face-based authorization.
- [Determine mobile PWA and push-notification constraints](https://github.com/robinjoseph08/memento/issues/4): standards Web Push, Apple installation requirements, explicit permission action, VAPID, and best-effort delivery.
- [Define people, relationships, and audience authorization](https://github.com/robinjoseph08/memento/issues/5): People, roles, relationships, Visibility circles, Interest lists, Attendance, proposals, Audience snapshots, suspension, and revocation.
- [Define source-album, event, moment, and publication lifecycles](https://github.com/robinjoseph08/memento/issues/6): Source album inbox, Events, Moments, Staged updates, Publication, source missing, relink, correction, and Withdrawal.
- [Define recipient identity, sessions, and media authorization](https://github.com/robinjoseph08/memento/issues/7): Invitations, email codes, opaque Sessions, trusted and public-computer behavior, Media proxy, archives, audit, and Preview as Recipient.
- [Prototype the curator publishing workflow](https://github.com/robinjoseph08/memento/issues/8): split-pane command center, nonlinear progress, mobile drill-down, Audience reasoning, Curator only, and visual direction.
- [Prototype the recipient experience](https://github.com/robinjoseph08/memento/issues/9): Timeline library, New for you, justified galleries, date rail, Event filtering, viewer, onboarding, and navigation.
- [Define notification and interaction behavior](https://github.com/robinjoseph08/memento/issues/10): MVP Web Push, email and push coalescing, Comments, Favorites, Invitation suggestions, engagement, failures, and quiet behavior.
- [Define recipient search and discovery boundaries](https://github.com/robinjoseph08/memento/issues/17): authorization-first portal search, People and place boundaries, hidden-content invariants, and query privacy.

### Research

- [Immich v3.0.3 media integration contract](research/immich-v3-media-integration.md)
- [Immich v3.0.3 person and face identity behavior](research/immich-v3-face-identity.md)
- [Mobile PWA push-notification constraints](research/mobile-pwa-push.md)

### Map and canonical language

- [Define Memento](https://github.com/robinjoseph08/memento/issues/1)
- [Memento canonical domain language](../CONTEXT.md)
