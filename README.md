# Web App Template

A full-stack web application template with React, Go, PostgreSQL, and one production binary.

<!-- web-app-template:template-only:start -->

## Create an application

Mark this repository as a template in GitHub, then choose **Use this template** to create a repository with independent history. Clone the generated repository and initialize its identity:

```sh
mise init github.com/owner/repository
```

The command derives the package slug, database identifier, and display name from the final segment of the module path. For example, `github.com/owner/order-console` becomes `order-console`, `order_console`, and `Order Console`. Review and commit the resulting changes before starting application work.

GitHub copies workflow files but does not copy repository secrets, rulesets, branch protection, environments, or other repository settings. Configure those separately in the generated repository.

<!-- web-app-template:template-only:end -->

## Prerequisites

Install [mise](https://mise.jdx.dev/) and Docker Desktop or another Docker engine with Compose support. The development commands use Unix process groups and file locks, so native Windows development requires WSL.

## Set up the main worktree

Run setup from the main Git worktree first:

```sh
mise setup
```

This command installs the pinned tools, JavaScript dependencies, Chromium, and Firefox. It starts PostgreSQL with Docker Compose, creates a database named after the repository if needed, runs migrations, and generates TypeScript types.

PostgreSQL is shared by every worktree, but each worktree receives a separate database. Shared PostgreSQL data lives in the main worktree at `tmp/postgres`. Its selected host port is stored in `tmp/database.env`. Setup prefers port `5432` and uses the next available port when necessary.

Only setup in the main worktree starts PostgreSQL. Do not run `docker compose down` while another worktree is using the database.

## Set up a linked worktree

Run the same command after creating a linked worktree:

```sh
mise setup
```

Linked-worktree setup requires the PostgreSQL container started by the main worktree. The first setup clones the main database and `tmp/files` into the linked worktree. If the worktree database is missing but `tmp/files` is not empty, setup asks before replacing those files. Later setup runs preserve an existing worktree database and files.

Worktree database names use the repository name, worktree directory, and a path hash. Mutable application files live at `tmp/files` inside each worktree.

## Start development

```sh
mise start
```

The command starts the API with Air, waits for it to become ready, and then starts Vite. It tries API port `3579` and web port `5173` first. If either port is occupied, it uses the next available port. Vite proxies `/api` and `/health` to the selected API port.

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

This runs Go linting, Go tests, ESLint, Prettier, TypeScript checks, Vitest, Chromium E2E tests, and a complete production build. CI also runs the race detector, Firefox, a Docker build, and a production smoke test.

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

Vite writes its production output into the Go web package. Go embeds those files into `build/api/api`. The binary serves API routes, static assets, and SPA fallback routes from one HTTP port. Development still uses Vite separately for hot module replacement.

Build the production container:

```sh
mise docker
```

The image runs one non-root Go process, listens on port `8080`, reads optional configuration from `/config/app.yaml`, and stores mutable files under `/data/files`. Supply `DATABASE_URL` for a reachable PostgreSQL database. See `app.example.yaml` for every setting.

## Database migrations

```sh
mise db:migrate
mise db:rollback
mise db:reset
mise db:migrate:create migration_name
```

`db:reset` deletes all data in the current worktree database and asks for confirmation when the database exists. It does not change `tmp/files`.

## Clone worktree data

Clone the main worktree's data into the current linked worktree:

```sh
mise clone:db
mise clone:files
mise clone
```

Reverse the direction to replace main-worktree data with the current linked worktree's data:

```sh
mise clone:db --to-main
mise clone:files --to-main
mise clone --to-main
```

Clone commands must run from a linked worktree. Existing destination data requires confirmation. Combined clones stage files and restore the old files if database replacement fails. Do not run clone commands while the target application is active.

## Releases

Create a release after the default branch is clean and pushed:

```sh
mise release 1.2.3 --dry-run
mise release 1.2.3
```

The release command updates `CHANGELOG.md` and `package.json`, creates a `[Release] v1.2.3` commit and annotated tag, then atomically pushes both. The tag-triggered workflow validates the repository, creates a GitHub release, and publishes `linux/amd64` and `linux/arm64` images to GitHub Container Registry.

<!-- web-app-template:template-only:start -->

## Template maintenance

Keep the template buildable with its neutral identity. Before marking a revision ready for reuse, verify:

```sh
mise setup
mise check
mise test:race
mise test:e2e
mise docker
```

Generate a temporary repository and run `mise init` there whenever the initializer or project identity changes.

<!-- web-app-template:template-only:end -->
