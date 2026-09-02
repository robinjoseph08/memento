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

This command installs the pinned tools, JavaScript dependencies, Chromium, and
Firefox. It starts PostgreSQL with Docker Compose, creates a database named
after the repository if needed, runs migrations, and generates TypeScript
types.

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
to the selected API port.

The following commands start one process when needed:

```sh
mise start:air
mise start:api
mise start:web
```

## Run checks

```sh
mise check
```

This runs Go linting, Go tests, ESLint, Prettier, TypeScript checks, Vitest,
Chromium E2E tests, and a complete production build. CI also runs the race
detector, Firefox, a Docker build, and a production smoke test.

Other useful commands:

```sh
mise check:quiet
mise test:race
mise test:e2e
mise e2e:firefox
```

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
`/data/files`. Supply `DATABASE_URL` for a reachable PostgreSQL database. See
`app.example.yaml` for every setting.

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
