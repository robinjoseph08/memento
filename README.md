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

## Prerequisites

Install [mise](https://mise.jdx.dev/) and Docker Desktop or another Docker
engine with Compose support. The development commands use Unix process groups
and file locks, so native Windows development requires WSL.

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
worktree. The first setup clones the main database and `tmp/files` into the
linked worktree. If the worktree database is missing but `tmp/files` is not
empty, setup asks before replacing those files. Later setup runs preserve an
existing worktree database and files.

Worktree database names use the repository name, worktree directory, and a path
hash. Mutable application files live at `tmp/files` inside each worktree.

## Start development

```sh
mise start
```

The command starts the API with Air, waits for it to become ready, and then
starts Vite. It tries API port `3579` and web port `5173` first. If either port
is occupied, it uses the next available port. Vite proxies `/api` and `/health`
to the selected API port. Open the printed `http://localhost:PORT` web URL;
`PUBLIC_URL` uses that same origin. `localhost` and `127.0.0.1` are different
browser origins, so substituting one for the other causes mutation requests to
be rejected.

The following commands start one process when needed:

```sh
mise start:air
mise start:api
mise start:web
```

### Try a disposable installation

```sh
mise start:qa
```

Open the printed URL and keep the command running. This uses fake sign-in, a
controlled Immich server that starts offline, and a temporary PostgreSQL schema.
Stop it with Ctrl-C and run it again to reset without erasing development data.
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
instead.

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

The image runs one non-root Go process, listens on port `8080`, reads optional
configuration from `/config/app.yaml`, and stores mutable files under
`/data/files`. Configure `DATABASE_URL`, `PUBLIC_URL`, `IMMICH_URL`, and
`IMMICH_API_KEY` through YAML or environment variables. `IMMICH_URL` is the
instance base URL without `/api`. Environment values override YAML.
`PUBLIC_URL` must match the browser origin and controls cookie security and
mutation origin checks. See `app.example.yaml` for a deployment example.

### Separate PostgreSQL database and role

Memento can share a PostgreSQL 14 or newer server with Immich, but not Immich's
database or role. As a PostgreSQL administrator, provision Memento separately:

```sql
CREATE ROLE memento LOGIN PASSWORD 'choose-a-strong-password';
CREATE DATABASE memento OWNER memento;
REVOKE ALL ON DATABASE memento FROM PUBLIC;
```

Use `postgres://memento:YOUR_URL_ENCODED_PASSWORD@HOST:5432/memento` for
`DATABASE_URL`, adding the appropriate TLS settings for your deployment. Do not
grant this role access to Immich tables. Memento talks to Immich only through
its HTTP API with a read-only API key.

For isolated tests, provision another database and role:

```sql
CREATE ROLE memento_test LOGIN PASSWORD 'choose-a-test-password';
CREATE DATABASE memento_test OWNER memento_test;
REVOKE ALL ON DATABASE memento_test FROM PUBLIC;
```

Set `TEST_DATABASE_URL` to that test database. Test and QA cleanup drops only
the random schema allocated by that invocation, never the database.

## Google sign-in

Production sign-in uses Google OpenID Connect to verify identity. Memento requests
only `openid profile email`, not access to Google Photos, Drive, or Gmail. It does
not retain Google's access or refresh tokens. Browser sessions belong to Memento
and live in PostgreSQL.

### Create a Google client

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
   redirect URI, such as `https://photos.example.com/api/identity/google/callback`.
   For local development, also add
   `http://localhost:3579/api/identity/google/callback`. This server flow does not
   need an authorized JavaScript origin.
5. Copy the client ID and secret into Memento's configuration. Keep the secret
   out of source control.

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

### Configure production

Set these alongside `DATABASE_URL`, `IMMICH_URL`, and `IMMICH_API_KEY`:

```sh
export PUBLIC_URL=https://photos.example.com
export GOOGLE_CLIENT_ID='your-client-id.apps.googleusercontent.com'
export GOOGLE_CLIENT_SECRET='your-client-secret'
```

Google requires an exact redirect URI match, including scheme, port, path, and
trailing slash. Memento derives the callback by appending
`/api/identity/google/callback` to the configured `PUBLIC_URL`. Register that
exact URL on the Google client. There is no separate callback setting, and
Memento does not derive the origin from request or forwarded host headers.

Use HTTPS at the browser-facing reverse proxy. Session and login-state cookies
are Secure, HttpOnly, and SameSite=Lax. Google discovery happens on the first
sign-in, not at startup. A Google outage does not prevent database health checks
or use of existing Memento sessions.

The first successful sign-in claims an empty installation and creates its first
Curator. Keep a new installation private until you have claimed it. For later
people, a Curator must create the Person and preauthorize the exact email Google
reports. A verified Google email alone does not grant access. Google sign-in
availability does not bypass Memento's preauthorizations.

### Use Google locally

Automated tests use a local OIDC server, not real Google credentials. To develop
against Google, use a separate database and run the built application on a fixed
port. Unlike `mise start`, the binary does not select another port or replace
your database URL. Create the database and role first using the PostgreSQL
instructions above; startup applies migrations.

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
export FILES_PATH="$PWD/tmp/google-files"
./build/api/api
```

Open `http://localhost:3579`, not `127.0.0.1`. HTTP is permitted only for the exact
`localhost` hostname in development or test; cookies remain HttpOnly and
SameSite=Lax but are not Secure on this local HTTP origin. No public tunnel is
needed. The first account to sign in becomes Curator if the database is empty.

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

Pending Google logins stay in one server process. The normal single-process
Memento deployment needs no shared login-state store. Multiple processes require
sticky routing during sign-in.

For protocol details, see [Google OpenID Connect](https://developers.google.com/identity/openid-connect/openid-connect)
and [Google's web-server OAuth flow](https://developers.google.com/identity/protocols/oauth2/web-server).

## Database migrations

```sh
mise db:migrate
mise db:rollback
mise db:reset
mise db:migrate:create migration_name
```

`db:reset` deletes all data in the current worktree database and asks for
confirmation when the database exists. It does not change `tmp/files`.

## Clone worktree data

Clone the main worktree's data into the current linked worktree:

```sh
mise clone:db
mise clone:files
mise clone
```

Reverse the direction to replace main-worktree data with the current linked
worktree's data:

```sh
mise clone:db --to-main
mise clone:files --to-main
mise clone --to-main
```

Clone commands must run from a linked worktree. Existing destination data
requires confirmation. Combined clones stage files and restore the old files if
database replacement fails. Do not run clone commands while the target
application is active.

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
