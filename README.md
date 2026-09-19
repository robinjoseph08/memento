# Memento

Memento is a self-hosted portal that can be exposed to friends and family so
that you can publish photos and videos from Immich with granular permissions on
a per-asset level.

## Why does this repo exist?

Even though the problem of sharing photos has a lot of possible solutions, I
haven't found anything that solves it a way that makes sense for my workflow.
Here are the reasons that alternatives don't really work.

Google Photos is the easiest solution for consumers of the media, but this is
the least ideal for me. While my JPEG exports of photos are small, the videos
that I take are not, so I would run out of Google storage very quickly. That's
why I've moved to Immich for most of my photo and video hosting needs (along
with data ownership, but that applies less to this problem). So my own personal
workflow resolves around Immich and not Google Photos.

While I could just share out public album links directly from Immich, the main
issue is that permission granularity of a single album isn't sufficient for
what I want. The prime example for this is a multi-day vacation trip. For the
first few days, we spent time with friend group A, and then we switched and
spent time with friend group B, and then we finished the trip off with just the
immediate family. In the end, I want a single "Trip" album for ourselves, but
I'd like to share out photos for both A and B. That means having and
maintaining 3 different albums for this one trip. And then it gets even more
complicated if there's some overlap between A and B because now they get a
subpar experience of not having a single album for the trip.

In addition, the way that videos are handled in photo sharing apps isn't how I
want to present them. Like a phone's camera roll, videos are shared inline
along with photos. This works fine for small, in the moment clips that are
usually taken with a phone, but my desire for videos is more of a "home video"
style of shooting. So even if I take multiple clips throughout the event/day,
I'll end up stitching them all up together and adding chapter markers. Immich
doesn't support video chapter marks in the first place, and what I really want
are two separate tabs: one for photos and one for videos. It doesn't make sense
to include the video chronologically since it would just go based on the first
clips start timestamp.

So in the end, I want to create my own portal that my friends and family can
log into to see the photos that I've taken of events that they've attended.

## Deploy Memento

Memento ships as one container image, `ghcr.io/robinjoseph08/memento`, built
for `linux/amd64` and `linux/arm64`. It runs a single non-root process that
listens on port `3579`, applies its own database migrations at startup, and
reads settings from environment variables or from a YAML file mounted at
`/config/app.yaml`. Environment variables win over the file. `app.example.yaml`
documents the settings an operator may need to change; the walkthrough below
uses only environment variables.

Memento keeps all of its state in PostgreSQL and stores no media, so the
container needs no volume. It also ignores the container's time zone, because
photo dates come from Immich's wall-clock capture times, so no `TZ` setting is
needed.

You need:

- An Immich server on a supported release. Memento imports from **Immich
  3.0.x, 3.1.x, and 3.2.x**; 2.x does not work. See
  [Immich imports](#immich-imports).
- PostgreSQL 14 or newer. The steps below reuse Immich's PostgreSQL container
  with a separate database and role. A
  [separate container](#use-a-separate-postgresql-container) also works.
- A public HTTPS address served by a reverse proxy, such as
  `https://photos.example.com`. Memento itself speaks only plain HTTP.
- A Google OAuth client, because sign-in is Google only.
- Optionally, an SMTP server for Invitations and update email.

The steps assume Immich runs from its standard Compose file, where the server
service is `immich-server`, the database service is `database`, and the
database container is `immich_postgres`.

### 1. Create the database and role

Memento must not share Immich's database or role. Create its own inside
Immich's PostgreSQL container, connecting as the superuser named by
`DB_USERNAME` in Immich's `.env` (`postgres` by default):

```sh
docker exec -it immich_postgres psql -U postgres
```

```sql
CREATE ROLE memento LOGIN PASSWORD 'choose-a-strong-password';
CREATE DATABASE memento OWNER memento;
REVOKE ALL ON DATABASE memento FROM PUBLIC;
```

Do not grant this role access to Immich's tables. Memento reads Immich only
through its HTTP API. The password goes inside `DATABASE_URL`, so use letters
and digits or URL-encode it.

### 2. Create the Immich API key

In Immich, open Account Settings and then API Keys for the account that owns
or can access the albums you will share. Create a key with only these
permissions: `album.read`, `asset.download`, `asset.read`, `asset.view`,
`face.read`, and `person.read`. No write permission is needed; Memento never
edits albums or assets. [Immich imports](#immich-imports) explains what each
permission is used for.

### 3. Create the Google client

1. Open [Google Auth Platform](https://console.cloud.google.com/auth/overview)
   and select or create a project.
2. Complete Branding with an app name, support email, and developer contact.
   Choose an External audience for friends and family outside your Workspace.
   Review Audience and Branding before publishing for your intended users.
3. Under Data Access, select `openid`, `https://www.googleapis.com/auth/userinfo.email`,
   and `https://www.googleapis.com/auth/userinfo.profile`. These are the console
   equivalents of Memento's `openid email profile` request. No Google Photos API,
   sensitive scopes, or offline access are needed.
4. Under Clients, create a **Web application** client. Add the exact authorized
   redirect URI, which is your public address followed by
   `/api/identity/google/callback`, such as
   `https://photos.example.com/api/identity/google/callback`. This server flow
   does not need an authorized JavaScript origin.
5. Keep the client ID and secret for the next step.

Google requires an exact redirect URI match, including scheme, port, path, and
trailing slash. Memento derives the callback by appending
`/api/identity/google/callback` to `PUBLIC_URL`; there is no separate callback
setting. You do not need to add people to Google's test-user list or complete
verification for these three scopes; see [Google sign-in](#google-sign-in) for
Google's audience and verification rules.

### 4. Write the Compose file

Create a directory for Memento with these two files. The Compose file joins
Immich's Docker network so Memento reaches Immich and its PostgreSQL by service
name, and publishes port `3579` for your reverse proxy the same way Immich
publishes `2283`.

`compose.yaml`:

```yaml
services:
  memento:
    # Pin a release tag instead of :latest to control when Memento upgrades.
    image: ghcr.io/robinjoseph08/memento:latest
    container_name: memento
    restart: unless-stopped
    environment:
      # The HTTPS address your reverse proxy serves.
      PUBLIC_URL: https://photos.example.com
      # The role and database from step 1, on Immich's database service.
      DATABASE_URL: postgres://memento:${MEMENTO_DB_PASSWORD}@database:5432/memento?sslmode=disable
      # Immich's base URL without /api, as seen from inside Docker.
      IMMICH_URL: http://immich-server:2283
      # The Immich address a Curator's browser opens from "Open in Immich" links.
      IMMICH_PUBLIC_URL: https://immich.example.com
      IMMICH_API_KEY: ${IMMICH_API_KEY}
      GOOGLE_CLIENT_ID: ${GOOGLE_CLIENT_ID}
      GOOGLE_CLIENT_SECRET: ${GOOGLE_CLIENT_SECRET}
      # Optional. See "Send Invitations and update email over SMTP" below.
      # SMTP_URL: smtps://user:password@mail.example.com:465
      # SMTP_FROM: Memento <memento@example.com>
    ports:
      - "3579:3579"
    networks:
      - immich

networks:
  immich:
    # Compose names Immich's network after its directory, usually
    # immich-app_default. Check with: docker network ls
    name: immich-app_default
    external: true
```

`.env`, which Compose reads for the `${...}` values above. Keep it out of
source control:

```sh
MEMENTO_DB_PASSWORD=choose-a-strong-password
IMMICH_API_KEY=your-read-only-immich-key
GOOGLE_CLIENT_ID=your-client-id.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=your-client-secret
```

### 5. Put a reverse proxy in front

A reverse proxy that terminates TLS is required. Set `PUBLIC_URL` to the
`https://` address served by the proxy. Memento serves HTTP on port `3579`;
the proxy owns certificates, HTTP-to-HTTPS redirects, and HSTS. Plain HTTP
session cookies are supported only for the exact `localhost` hostname.

Point the proxy at Memento the same way it already reaches Immich on port
`2283`: the Docker host's address and port `3579` for a proxy that runs on the
host or in its own Compose project. A proxy container that shares Immich's
network can instead use `memento:3579` directly, in which case you can drop
the `ports` entry. Bind the published port to `127.0.0.1:3579:3579` if the
proxy runs on the host and you want nothing else on the network to reach it.

For example, with [Nginx Proxy Manager](https://nginxproxymanager.com/guide/):

1. Create a Proxy Host for `photos.example.com` and forward it using the
   `http` scheme to the same host address you use for Immich and port `3579`.
2. Select or request a certificate in the SSL tab and enable Force SSL.
   Configure HSTS there if you want it for this host.

Memento gzip-compresses API JSON, the app shell, and frontend bundles when
the browser accepts gzip. No proxy gzip tuning is needed. Nginx's
[default gzip types](https://nginx.org/en/docs/http/ngx_http_gzip_module.html#gzip_types)
cover only HTML, which leaves JSON, JavaScript, and CSS uncompressed when
relying on the proxy alone. Media and playback routes bypass Memento's
compression to preserve byte ranges and private versioned caching.

Casting a video over AirPlay, or a photo or video over Google Cast, hands the
TV a signed address under `PUBLIC_URL` that works for a few hours, and the TV
fetches the media from there itself. Casting therefore needs `PUBLIC_URL` to
be reachable from the TV on the family's network, not only from phones and
laptops, with a certificate the TV trusts. An address that resolves only
through one device's hosts file or VPN, or a certificate from a private
authority, will not cast.

### 6. Start and claim the installation

```sh
docker compose up -d
```

Open your public address and sign in with Google. The first successful
sign-in claims an empty installation and creates its first Curator, so do this
yourself before sharing the address with anyone. Everyone else needs a Person
with a preauthorized email, created by a Curator, or arrives as an Access
Request for a Curator to approve. [Google sign-in](#google-sign-in) describes
how access works from there.

To upgrade, pull the new image and recreate the container. Migrations run at
startup:

```sh
docker compose pull
docker compose up -d
```

### Use a separate PostgreSQL container

To give Memento its own PostgreSQL instead of Immich's, change these parts of
the walkthrough:

- Skip step 1. The official PostgreSQL image creates the role and database
  from its environment.
- Add a `postgres` service with a volume and a `depends_on` entry, and point
  `DATABASE_URL` at the `postgres` service instead of `database`.
- Put `memento` on the Compose project's `default` network as well, so it can
  reach `postgres`. Keep the `immich` network for `http://immich-server:2283`,
  or remove it from both the service and the top-level `networks` block and
  set `IMMICH_URL` to Immich's public address, such as
  `https://immich.example.com`.

```yaml
services:
  memento:
    # ...unchanged...
    environment:
      DATABASE_URL: postgres://memento:${MEMENTO_DB_PASSWORD}@postgres:5432/memento?sslmode=disable
      # ...unchanged...
    depends_on:
      postgres:
        condition: service_healthy
    networks:
      - default
      - immich

  postgres:
    image: postgres:17-alpine
    container_name: memento_postgres
    restart: unless-stopped
    environment:
      POSTGRES_USER: memento
      POSTGRES_PASSWORD: ${MEMENTO_DB_PASSWORD}
      POSTGRES_DB: memento
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U memento -d memento"]
      interval: 5s
      timeout: 5s
      retries: 10
    volumes:
      - memento-postgres:/var/lib/postgresql/data

volumes:
  memento-postgres:
```

Any PostgreSQL 14 or newer works. The password still goes inside
`DATABASE_URL`, so the same URL-encoding advice applies.

### Send Invitations and update email over SMTP

Email is optional. Without it Memento runs normally, Curators still create
People and approve emails, Update Notifications stay in app, and the Invite
button explains that email is not configured. To enable email, set both
values on the `memento` service:

```yaml
environment:
  SMTP_URL: smtps://user:password@mail.example.com:465
  SMTP_FROM: Memento <memento@example.com>
```

`smtp://` connects in plain text and upgrades with STARTTLS whenever the server
offers it; `smtps://` uses TLS from the first byte, typically on port 465.
Credentials stay in the URL, so keep it in `.env` or the environment rather
than a checked-in file. A username and password are only sent over TLS, so
pair them with `smtps://` or a server that offers STARTTLS; a plain relay
without authentication needs no credentials at all. `SMTP_CONCURRENCY` bounds
simultaneous deliveries and defaults to five. Settings, in the account menu,
shows whether the mail server accepts a connection and sign-in; that check
sends nothing.

Deliveries run through the same in-process River runtime as imports, on a
separate `mail` queue. Each email has a stable delivery record: a rejected
connection or a 4xx reply retries automatically up to five times, a 5xx reply
fails permanently, and a connection lost after the message body was sent is
marked uncertain because the server may have accepted it. Uncertain and failed
Invitations show their state on the Person page with a Retry action; the
uncertain case warns that sending again could deliver a duplicate. Memento never
resends an ambiguous attempt on its own, including after a restart.

Invitations are outreach only. They name the approved email and link to the
ordinary sign-in page without any token, so admission still depends on the
Preauthorization. Invitation email ignores a Person's update-email preference
because it is transactional.

Approved Update Notifications are also emailed, but only to an active Person
who selected a destination and switched update email on. The email is queued
with the approval and checked again right before it is sent: a Person who was
deactivated, promoted to Curator, unlinked their destination, or unsubscribed
in the meantime is skipped, and content revoked since approval is left out
of the counts. The in-app notification is never changed by any of this.
Every update email carries a private unsubscribe link that opens a
confirmation page without signing in; loading the link changes nothing, and
confirming switches off update email only. Invitations, Access Request
alerts, and in-app notifications continue.

## Immich imports

The import and synchronization gate supports stable Immich **3.0.x, 3.1.x,
and 3.2.x**. Other versions still report their detected version during setup,
but Memento blocks new imports and synchronization and shows a warning. Safe
existing reads remain available when the source APIs work. The
[release smoke test](#smoke-test-a-real-immich-release) defaults to v3.2.1 and
also tests v3.1.0 and v3.0.3; this is not a claim that every patch in those
minors has been certified.

**Immich 2.x does not work with this release of Memento.** Testing the latest
2.x release, v2.7.5, confirmed that the shipped adapter rejects its asset metadata.
Imports and synchronization remain blocked on all 2.x versions. Use a supported
3.x release instead; `--probe` is a diagnostic tool, not a compatibility workaround.

The API key from [step 2](#2-create-the-immich-api-key) needs only read
permissions. `album.read` and `asset.read` list albums and their assets. The
face and person permissions let Memento read face associations and person
thumbnails for access suggestions and avatars. `asset.download` lets authorized
viewers save an original photo or video and lets `ffprobe` read chapters from
the original file; Curator preview never downloads. `asset.view` also serves
video playback, which proxies Immich's playback stream with byte ranges so a
long video seeks without downloading first. Installations created before
downloads existed must add `asset.download` to their key, or every download
fails with "Media is unavailable" and the server log records the missing
permission. Memento uses GET requests plus Immich's read-only
`POST /search/metadata` endpoint for membership pagination. It never edits
source albums or assets.

Face review links each Immich person to its page in Immich so merging duplicate
faces or changing a featured photo happens there. Those links use `IMMICH_URL`
unless `IMMICH_PUBLIC_URL` names the address a browser should open instead, for
example when `IMMICH_URL` is a Compose service name.

Imports run inside the API process through River, sharing Memento's PostgreSQL
pool. Two imports can run at once, with three automatic attempts and a
15-minute limit per attempt. An orderly shutdown interrupts unfinished work for
the next startup. After a forced process termination, stale work becomes
eligible for automatic recovery after 16 minutes. The Album page reports
missing progress as interrupted; retry remains available after failure.

Video chapters use the same runtime on a separate `ffprobe` queue. Every
imported video is probed once per source checksum, with three attempts, a
one-minute budget per probe, and a three-minute limit per task, and videos
imported before this capability existed are queued at the next start. `ffprobe` reads Immich's original-file endpoint
through HTTP ranges and never downloads a complete original. A video with no
chapters is a normal, finished result. A failed probe is shown in the Curator's
video details with a retry, and never blocks playback or publication. Curators
can also give a video a title there; the filename without its extension shows
until they do.

A completed import is unpublished. Memento owns its title, while the description
is the last imported Immich description and cannot be edited in Memento. No
media bytes are stored persistently. Thumbnails use private browser caching;
source media that changes before synchronization may show an unavailable image
rather than different bytes under an old content-versioned URL.

## Google sign-in

Production sign-in uses Google OpenID Connect to verify identity. Memento requests
only `openid profile email`, not access to Google Photos, Drive, or Gmail. It does
not retain Google's access or refresh tokens. Browser sessions belong to Memento
and live in PostgreSQL.

Google exempts basic-sign-in-only requests from the test-user list requirement,
even when the app's publishing status is **Testing**. You do not need to add
Memento users to Google's test-user list or complete sensitive-scope verification
for these three scopes. Publishing an app and verifying it are separate actions;
custom consent-screen branding may require brand verification. Google Workspace
administrators can still restrict their users' access. See
[Google's audience rules](https://support.google.com/cloud/answer/15549945) and
[brand verification requirements](https://developers.google.com/identity/verification/authentication-verification).

A single Web application client can list both the deployed and localhost
callback URLs. For a personal installation used by fewer than 100 people, all
personally known to you, this allows sharing a client for local testing. Google
classifies that audience as personal use. Apps classified as production under
[Google's OAuth policies](https://developers.google.com/identity/protocols/oauth2/policies#separate-projects)
require separate Cloud projects for development and production, not merely
separate clients. Separate projects also keep credentials and consent settings
independent.

### Sign-in and access

Memento derives the Google callback from `PUBLIC_URL` and never from request
or forwarded host headers. Session and login-state cookies are Secure,
HttpOnly, and SameSite=Lax. Google discovery happens on the first sign-in, not
at startup. A Google outage does not prevent database health checks or use of
existing Memento sessions.

The first successful sign-in claims an empty installation and creates its first
Curator. Keep a new installation private until you have claimed it. For later
people, a Curator must create the Person and preauthorize the exact email Google
reports. A verified Google email alone does not grant access. Google sign-in
availability does not bypass Memento's preauthorizations.

A verified Google account that Memento does not know creates one pending Access
Request instead of an account. Repeated sign-ins refresh that request rather
than creating more, and a denied request absorbs later attempts silently until
a Curator reconsiders it. Curators review requests under Requests, where
approval links the identity to an existing Person or creates one and approves
the exact email; Album access remains a separate decision in each Album. An
existing Person who reaches an Album they cannot see gets an explicit Request
access action, and visiting alone records nothing. Each new request also
emails every active Curator who has selected an email, when SMTP is
configured; repeated sign-ins against the same request send nothing more.

Every Person completes a one-time Onboarding after their first sign-in, whether
they arrived through an Invitation or signed in directly. It confirms their
name and email preference and shows the Albums already visible to them, which
become their notification baseline so later updates only announce new content.

Publishing never notifies anyone by itself. The Curator's Updates page lists
every Person who can now see photos or videos they have not been told about,
grouped into collapsible rows with the Albums and counts each would hear
about; photos and videos are counted, never listed. A Curator can leave out a
person or one Album update, add a note for everyone, and send. Sending creates
an in-app Update Notification for each included Person and records exactly
which Album Entries were announced, so repeated sends, two Curators approving
overlapping previews, or revoking and restoring access never announce the same
media twice. Members see a bell that
shows their unread count and lists only new updates, and an Updates page with
every update they have received. Opening one marks it read and goes to the Album, or
to the Album list when it covers several. Browsing never changes read state.
People who asked for email get the same summary by email; see
[Send Invitations and update email over SMTP](#send-invitations-and-update-email-over-smtp).

The Curator's home page is a short work list in two groups. Needs attention
holds pending Access Requests, failed or interrupted imports, failed or
uncertain email, failed chapter extraction, and an unreachable Immich server.
Ready when you are lists unpublished Albums and people with changes they have
not heard about, in neutral words, because waiting is a choice rather than a
failure. The page polls only while an import or email is still running.
Settings, in the account menu, checks the Immich connection, the mail server,
and the bundled ffprobe on request.

### Troubleshooting

- `redirect_uri_mismatch`: compare the callback with the Google client's
  authorized redirect URI. Check the port and remove any extra slash.
- Expired or invalid sign-in: start again from Memento. Login transactions expire
  after ten minutes and can be used only once. Restarting Memento or starting a
  newer login in the same browser cancels the previous pending login.
- Google unavailable: retry later. Discovery and token requests have a timeout;
  later attempts retry failed discovery.
- Access denied: use the preauthorized Google account or ask a Curator to
  preauthorize its exact email. Do not switch production to fake authentication.
- Access requested: the account is unknown and a Curator now has a pending
  request for it. Nothing more is needed from the person signing in.

Pending Google logins stay in one server process. The normal single-process
Memento deployment needs no shared login-state store. Multiple processes require
sticky routing during sign-in.

For protocol details, see [Google OpenID Connect](https://developers.google.com/identity/openid-connect/openid-connect)
and [Google's web-server OAuth flow](https://developers.google.com/identity/protocols/oauth2/web-server).

## Development prerequisites

Install [mise](https://mise.jdx.dev/) and Docker Desktop or another Docker
engine with Compose support. The development commands use Unix process groups
and file locks, so native Windows development requires WSL.

Install `ffmpeg` as well (`brew install ffmpeg` on macOS, `apt install ffmpeg`
on Debian and Ubuntu). Memento runs its `ffprobe` binary to read video
chapters, and the adapter tests and browser tests skip or fail without it.

## Set up the main worktree

Run setup from the main Git worktree first:

```sh
mise setup
```

This command installs the pinned tools, JavaScript dependencies, Chromium,
Firefox, and WebKit. It starts PostgreSQL with Docker Compose, creates a
database named after the repository if needed, runs migrations, and generates
TypeScript types.

PostgreSQL is shared by every worktree, but each worktree receives a separate
database. Shared PostgreSQL data lives in the main worktree at `tmp/postgres`.
Its selected host port is stored in `tmp/database.env`. Setup prefers port
`5432` and uses the next available port when necessary.

Only setup in the main worktree starts PostgreSQL. Do not run `docker compose
down` while another worktree is using the database.

## Set up a linked worktree

Run the same command after creating a linked worktree:

```sh
mise setup
```

Linked-worktree setup requires the PostgreSQL container started by the main
worktree. The first setup clones the main database into the linked worktree.
Later setup runs preserve an existing worktree database.

Worktree database names use the repository name, worktree directory, and a path
hash.

## Start development

```sh
mise start
```

The command starts the API with Air, waits for it to become ready, and then
starts Vite. It tries API port `3579` and web port `5173` first. If either port
is occupied, it uses the next available port. Vite proxies `/api` and `/health`
to the selected API port. Open the printed `http://localhost:PORT` web URL, or
reach it from another machine on your network using this machine's hostname,
such as `http://my-machine.local:PORT`.

The following commands start one process when needed:

```sh
mise start:air
mise start:api
mise start:web
```

### Develop against your own Immich server

Use a read-only API key from the Immich account that owns or can access your
source albums. Grant `album.read`, `asset.download`, `asset.read`,
`asset.view`, `face.read`, and `person.read`, then set these
in your local shell before starting development:

```sh
export IMMICH_URL='http://your-immich-host:2283'
export IMMICH_API_KEY='your-read-only-immich-key'
mise start
```

Use the instance base URL without `/api`. Keep the actual key out of source
control and shared logs. These environment values override development YAML;
`mise start` still uses the current worktree's Memento database. Imports read
your real library without changing it. `mise start:qa` instead uses its
controlled fixture and ignores these values.

### Try a disposable installation

```sh
mise start:qa
```

Open the printed URL and keep the command running. This uses fake sign-in, a
controlled Immich server that starts online, a controlled SMTP server, and a
temporary PostgreSQL schema. The command prints curl lines that take the
fixture offline, restart the API, and inspect or steer mail delivery: the SMTP
fixture lists every accepted message at `/__fixture/smtp`, and its mode can be
`accept`, `transient` (reply 451), `permanent` (reply 550), or `hold`, which
records the message but never answers so a restart leaves delivery uncertain.
Accepted messages include the unsubscribe link of each update email, so the
confirmation page can be opened from the fixture output.
Pass `--no-smtp` to start without email and check that Person setup and sign-in
still work. Stop it with Ctrl-C and run it again to reset without erasing
development data.

The fixture library can be edited while the command runs, which is how to try
Check for changes on an imported Album: `POST /__fixture/library` replaces an
album's `members`, sets its `description`, patches asset facts such as
`checksum`, `localDateTime`, or `isTrashed` under `assets`, and removes assets
from Immich entirely with `delete`. The printed curl line shows the shape.
Take the fixture offline to see a check fail without touching the Album, or
edit the library again between a review and Apply to see the stale review
refused. Media that should stay in Immich but out of an Album is kept out from
its Moment with Keep out and listed in the Album's Excluded media section, where
Add back returns it; a check never offers excluded media again, though it keeps
their details current and reports ones that left Immich.
Use separate browser profiles to try multiple people. Ordinary tabs share the
same session cookie. Never expose fake development sign-in publicly.

## Run checks

```sh
mise check
```

This runs Go linting, Go tests, ESLint, Prettier, TypeScript checks, Vitest,
Chromium E2E tests, and a complete production build. CI also runs the race
detector, Firefox, WebKit, PostgreSQL 14 compatibility, a Docker build, and a
production-image smoke test.

Other useful commands:

```sh
mise check:quiet
mise test:race
mise test:e2e
mise e2e:firefox
mise e2e:webkit
```

`mise test:e2e` uses `TEST_DATABASE_URL`, or the current worktree database and
its recorded PostgreSQL port when unset. Direct Go tests also use the current
worktree database, while `pnpm exec playwright test` uses the main development
database. All test data stays in temporary schemas with small pools. Export
`TEST_DATABASE_URL` to make all commands use one dedicated test database
instead. To provision one on a shared server:

```sql
CREATE ROLE memento_test LOGIN PASSWORD 'choose-a-test-password';
CREATE DATABASE memento_test OWNER memento_test;
REVOKE ALL ON DATABASE memento_test FROM PUBLIC;
```

Test and QA cleanup drops only the random schema allocated by that invocation,
never the database.

### Check PostgreSQL 14

The focused job covers migrations, Identity, Publishing, Media, Notifications,
and Worker behavior. To run it locally against a disposable PostgreSQL 14:

```sh
(
  set -e
  pg14=$(docker run --detach --rm --publish 127.0.0.1::5432 \
    --env POSTGRES_PASSWORD=smoke --env POSTGRES_DB=memento_test postgres:14-alpine)
  trap 'docker rm --force "$pg14" >/dev/null' EXIT
  until docker exec "$pg14" pg_isready -U postgres -d memento_test >/dev/null; do sleep 1; done
  address=$(docker port "$pg14" 5432)
  TEST_DATABASE_URL="postgres://postgres:smoke@$address/memento_test?sslmode=disable" mise test:postgres14
)
```

`mise test:postgres14` alone does not select a server version; it uses
`TEST_DATABASE_URL` or the normal worktree database, which remains PostgreSQL 17.

### Smoke-test a real Immich release

```sh
mise test:immich                         # pinned current release
mise test:immich --version v3.1.0
mise test:immich --version v3.0.3         # retained support for existing installations
mise test:immich --version v2.7.5 --probe # exploratory, not declared support
```

This requires Docker Compose 2.24.4 or newer, `curl`, `shasum`, Python 3, and
`ffprobe`. Budget 5 to 15 minutes per release after image downloads and enough
Docker memory for Immich and two PostgreSQL containers. The default release is
v3.2.1. `--version` accepts an exact stable tag, not a floating tag or prerelease.
The script downloads the official Compose file and tagged image into a uniquely
named disposable project. Official Compose SHA-256 checks are pinned for
v3.2.1, v3.1.0, v3.0.3, and the exploratory v2.7.5. Other exact releases can be
tried without a pinned Compose checksum. Running container image IDs and registry
digests are recorded, rather than assuming a tag still resolves to the same image.
The fixture checks the actual API version against the requested tag before writes.
Ports bind only to loopback. Core checks disable machine learning and reverse
geocoding; manual face associations make them deterministic.

The OpenAPI preflight checks consumed endpoints, response types, and required
search inputs against the release's official document. It never regenerates
Memento's adapter. `--probe` continues past contract drift and skips only the
compatibility command's import version gate to collect runtime evidence. All
source reads still use the shipped adapter. It neither changes the application's
supported range nor makes an unsupported release work out of the box. Contract
drift still makes the probe fail even if subsequent runtime checks pass.

Fixtures use supported Immich APIs, not database tables. A non-admin source
owner creates a key with exactly `album.read`, `asset.download`, `asset.read`,
`asset.view`, `face.read`, and `person.read`.
The smoke imports two overlapping photo albums and one video album through
Memento's production adapter and publishing module. It checks EXIF capture dates
around midnight, tied entry ordering, shared Media Items, generated thumbnails
and original downloads through production media HTTP routes, private cache
validators, ranged video playback, byte-range reads of the original file, real
`ffprobe` chapter extraction, manual synchronization after editing a source
album's membership and description through the album API, and unchanged
source album titles, descriptions, and membership once those edits are
reverted. The in-process media check bypasses sign-in and
does not expose an HTTP listener. Fixture creation helpers live in
`cmd/immich-smoke/fixture` for reuse by release compatibility tests. Additional
cases import a non-JPEG photo through generated display variants, a paired Live
Photo as a still, both stack members as independent photos, and an unchaptered
video whose extraction finishes with zero chapters. The suite checks each of the
six required key permissions separately and verifies that diagnostics name the
missing permission without exposing the key.

Memento uses a temporary schema in its own disposable PostgreSQL container.
The script ignores existing application and Immich configuration. On exit it
removes only its own containers and volumes; it never resets development data.
The final output names the evidence directory under
`tmp/immich-smoke.*/artifacts/`. It retains the requested version and flags,
Compose/OpenAPI checksums, resolved image digests, redacted service and test logs,
and the exit code and failure phase. Only upload that `artifacts` directory,
never the private Compose working files left behind if cleanup fails. A successful
run certifies only the assertions exercised on that release, not every version
in the supported range. Coverage grows manually: every Immich-dependent change must extend this
same suite's fixtures and assertions to cover the capability being shipped.
Passing the existing import checks alone does not certify new functionality.
This command is opt-in, not part of `mise check`. Running it again is the clean
fixture/reset command; each invocation creates new users, media, and databases.

For an intentional permission failure or the separate slower recognition check:

```sh
mise test:immich --version v3.0.3 --permission-failure asset.download
mise test:immich --ml
```

The first command must exit nonzero and name `asset.download`, not its key.
The second enables official ML services and uploads a bundled public-domain NASA
portrait. It requires a machine-learning face rather than a manual association,
without checking model-specific identity, geometry, scores, or clustering. Budget
10 to 30 minutes on a cold machine, including image and model downloads.

### Maintain the compatibility declaration

Each release tests the latest stable Immich minor and its predecessor. Memento
also retains 3.0.x support for existing installations. Pinned representatives are
v3.2.1, v3.1.0, and v3.0.3. Keep testing 3.0.x until its removal is an explicit
release decision. The v2.7.5 probe does not pass: fixture creation succeeds, but
the shipped adapter rejects asset metadata. Its OpenAPI also reports v2 wire
formats such as string video duration where Memento expects an integer. It does
not work out of the box, and the production gate continues to block 2.x.

Pull requests run the pinned current version in a separate parallel CI job.
Nightly and pre-release workflows test every declared minor. Nightly recognition
is a separate slower job. The release detector probes a newly released stable tag
and reports success in its run summary or opens a diagnostic issue on failure.
It never changes the declaration or production gate automatically.

To evaluate a new tag locally, run `mise test:immich --version vX.Y.Z --probe`.
Inspect `request.txt`, `images.txt`, `checksums.txt`, `result.txt`, `run.log`, and
`compose.log` in its printed artifact directory. A preflight mismatch identifies
a wire contract; HTTP permission errors identify the missing grant; startup
failures point to service logs. Share only redacted evidence, never your real API
key, a full `docker inspect`, or interpolated Compose configuration.

After proving compatibility, update the gate and message in
`pkg/immich/client.go`, its boundary tests, `fixture.Release`, the smoke runner's
default and official Compose checksum, the mise task default, CI matrices and
release detector pins, and this declaration. Move the controlled fixture's
unsupported version beyond the range too. Run all declared minors again before
release; do not generate adapter code from the candidate's OpenAPI document.

Once the detector workflow is on the default branch, simulate either outcome
without changing support:

```sh
gh workflow run immich-release-detector.yml -f simulated_release=v3.0.3
gh workflow run immich-release-detector.yml -f simulated_release=v2.7.5
```

The first should report success; the second exercises the diagnostic issue path
for an incompatible release. These commands run GitHub Actions and may create or
comment on a compatibility issue. Before merging the workflow, use the local
`--probe` command to inspect the same core evidence without publishing anything.

### Browser certification

Memento supports current Chrome, Firefox, desktop Safari, and iOS Safari.
`mise test:e2e` builds the frontend and API once, then runs the critical journeys
in Chromium, Firefox, and WebKit. Each parallel worker gets an isolated API,
PostgreSQL schema, fake sign-in, and controlled Immich/SMTP fixtures. Budget
5 to 15 minutes after installing browsers. `mise e2e:chromium`,
`mise e2e:firefox`, and `mise e2e:webkit` keep focused runs available. Failures
retain Playwright traces in `test-results/`; CI uploads them.

Playwright WebKit is not a substitute for testing shipped Safari. Before a
release, use `mise start:qa` or a private test deployment on desktop Safari and
iOS Safari. Check photo swipes and back navigation, video playback and seeking,
chapter selection, portrait/landscape layout, and touch controls. On a phone,
use a reachable private HTTPS deployment for production cookies rather than
exposing development fake sign-in to the internet.

## Build the production application

```sh
mise build
```

Vite writes its production output into the Go web package. Go embeds those
files into `build/api/api`. The binary serves API routes, static assets, and
SPA fallback routes from one HTTP port. Development still uses Vite separately
for hot module replacement.

Build the production container:

```sh
mise docker
```

This builds the same image that releases publish; [Deploy Memento](#deploy-memento)
describes how it is configured and run.

To exercise the built image's YAML/environment configuration and outage behavior,
use a fresh disposable Memento database, never your development or production
installation:

```sh
DATABASE_URL='postgres://USER:PASSWORD@127.0.0.1:PORT/DISPOSABLE_DATABASE?sslmode=disable' \
  bash scripts/production-smoke.sh memento:latest
```

This check claims that empty database using explicit test-only fake sign-in,
verifies liveness with Immich offline before and after claiming, and checks the
embedded UI and bundled `ffprobe`. It uses host networking, which requires Linux
or Docker Desktop's host-networking setting. It removes its application container
and retains redacted logs under `tmp/production-smoke.*/artifacts/`; remove the
disposable database separately afterward.

The image bundles `ffprobe` from Alpine's `ffmpeg` package for video chapters,
which adds roughly 110 MB because Alpine ships no ffprobe-only package.
`FFPROBE_PATH` names another binary when the process runs outside the image,
and `FFPROBE_CONCURRENCY` (default `1`) bounds how many probes run at once.

### Use Google locally

Automated tests use a local OIDC server, not real Google credentials. To develop
against Google, use a separate database and run the built application on a fixed
port. Unlike `mise start`, the binary does not select another port or replace
your database URL. Create the database and role first by running the SQL from
[Create the database and role](#1-create-the-database-and-role) against the
development PostgreSQL, naming the database `memento_google`; startup applies
migrations. Register `http://localhost:3579/api/identity/google/callback` as a
second redirect URI on the Google client.

```sh
mise build
export CONFIG_FILE="$PWD/app.example.yaml"
export APP_ENV=development
export AUTH_MODE=google
export SERVER_HOST=127.0.0.1
export SERVER_PORT=3579
export PUBLIC_URL=http://localhost:3579
export GOOGLE_CLIENT_ID='your-client-id.apps.googleusercontent.com'
export GOOGLE_CLIENT_SECRET='your-client-secret'
export DATABASE_URL='postgres://memento:YOUR_PASSWORD@localhost:5432/memento_google?sslmode=disable'
export IMMICH_URL='http://localhost:2283'
export IMMICH_API_KEY='your-read-only-immich-key'
./build/api/api
```

Open `http://localhost:3579`, not `127.0.0.1`. HTTP is permitted only for the exact
`localhost` hostname in development or test; cookies remain HttpOnly and
SameSite=Lax but are not Secure on this local HTTP origin. No public tunnel is
needed. The first account to sign in becomes Curator if the database is empty.

## Database migrations

```sh
mise db:migrate
mise db:rollback
mise db:reset
mise db:migrate:create migration_name
```

`db:reset` deletes all data in the current worktree database and asks for
confirmation when the database exists.

## Clone worktree data

Clone the main worktree's database into the current linked worktree:

```sh
mise clone
```

Reverse the direction to replace the main-worktree database with the current
linked worktree's database:

```sh
mise clone --to-main
```

The command must run from a linked worktree. An existing destination database
requires confirmation. Do not run it while the target application is active.

## Releases

Create a release after the default branch is clean and pushed:

```sh
mise release 1.2.3 --dry-run
mise release 1.2.3
```

The release command updates `CHANGELOG.md` and `package.json`, creates a
`[Release] v1.2.3` commit and annotated tag, then atomically pushes both. The
tag-triggered workflow validates the repository, creates a GitHub release, and
publishes `linux/amd64` and `linux/arm64` images to GitHub Container Registry.
