# GitHub template repositories

Researched against GitHub, Go, npm, and Git documentation on 2026-04-13.

## Findings

A GitHub template repository is a good way to distribute this codebase. GitHub copies the selected branch's files and directory structure into a new repository with one new root commit. The generated repository is not a fork and does not inherit the template's commit history.[^github-generate]

GitHub performs no variable substitution. Repository metadata supplies the new owner, name, description, and visibility, but contents such as a Go module path, npm package name, display name, and database name remain unchanged.[^github-generate][^github-api] This template therefore includes `mise init <module-path>` to apply the generated application's identity.

Workflow files under `.github/workflows` are copied because they are ordinary files. Secrets, variables, environments, branch protection, rulesets, collaborators, webhooks, deploy keys, and Actions settings are repository configuration and are not copied. Configure them after generation.[^actions-settings][^actions-secrets][^actions-environments]

A generated repository has no automatic update relationship with its template. Later template improvements must be applied manually or through separate synchronization tooling. GitHub also does not allow Git LFS files in a template repository.[^github-template]

## Recommendation

Maintain this repository as the generic source of reusable infrastructure and mark it as a GitHub template. Keep it buildable with neutral defaults. Generate each application through GitHub, run the initializer immediately, review its changes, and commit that application identity before feature work.

Use the default branch only when generating repositories. GitHub can copy every branch, but it documents that generated branches then have unrelated histories and cannot be merged or used for pull requests between one another.[^github-generate]

[^github-generate]: GitHub Docs, [Creating a repository from a template](https://docs.github.com/en/repositories/creating-and-managing-repositories/creating-a-repository-from-a-template).

[^github-template]: GitHub Docs, [Creating a template repository](https://docs.github.com/en/repositories/creating-and-managing-repositories/creating-a-template-repository).

[^github-api]: GitHub REST API, [Create a repository using a template](https://docs.github.com/en/rest/repos/repos#create-a-repository-using-a-template).

[^actions-settings]: GitHub Docs, [Managing GitHub Actions settings for a repository](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/enabling-features/managing-github-actions-settings-for-a-repository).

[^actions-secrets]: GitHub Docs, [Using secrets in GitHub Actions](https://docs.github.com/en/actions/security-for-github-actions/security-guides/using-secrets-in-github-actions).

[^actions-environments]: GitHub Docs, [Managing environments](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/manage-environments).

[^go-module]: The Go Project, [`go.mod` file reference](https://go.dev/doc/modules/gomod-ref).

[^npm-package]: npm Docs, [`package.json`, `name`](https://docs.npmjs.com/cli/v11/configuring-npm/package-json#name).
