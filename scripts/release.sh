#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: $0 <version> [--dry-run]"
}

version=""
dry_run=false
for argument in "$@"; do
  case "$argument" in
    --dry-run) dry_run=true ;;
    *)
      if [[ -n "$version" ]]; then
        usage
        exit 1
      fi
      version="${argument#v}"
      ;;
  esac
done

if [[ -z "$version" ]]; then
  usage
  exit 1
fi
if [[ ! "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$ ]]; then
  echo "Error: version must use semantic version syntax such as 1.2.3 or 1.2.3-rc.1."
  exit 1
fi
prerelease="${BASH_REMATCH[5]:-}"
if [[ -n "$prerelease" ]]; then
  IFS='.' read -r -a prerelease_identifiers <<< "$prerelease"
  for identifier in "${prerelease_identifiers[@]}"; do
    if [[ "$identifier" =~ ^[0-9]+$ && ${#identifier} -gt 1 && "$identifier" == 0* ]]; then
      echo "Error: numeric prerelease identifiers cannot contain leading zeros."
      exit 1
    fi
  done
fi

tag="v$version"
branch=$(git branch --show-current)
if [[ -z "$branch" ]]; then
  echo "Error: releases cannot be created from a detached HEAD."
  exit 1
fi
if ! git remote get-url origin >/dev/null 2>&1; then
  echo "Error: the origin remote is not configured."
  exit 1
fi

if ! default_branch=$(git ls-remote --symref origin HEAD | awk '$1 == "ref:" { sub("refs/heads/", "", $2); print $2; exit }'); then
  echo "Error: could not query the default branch from origin."
  exit 1
fi
if [[ -z "$default_branch" ]]; then
  echo "Error: could not determine the default branch from origin."
  exit 1
fi
if [[ "$branch" != "$default_branch" ]]; then
  echo "Error: releases must be created from $default_branch, not $branch."
  exit 1
fi
if ! starting_head=$(git rev-parse --verify HEAD 2>/dev/null); then
  echo "Error: the repository has no commits to release."
  exit 1
fi

git fetch --quiet --tags origin "+refs/heads/$default_branch:refs/remotes/origin/$default_branch"
remote_head=$(git rev-parse "refs/remotes/origin/$default_branch")
if [[ "$starting_head" != "$remote_head" ]]; then
  echo "Error: local $default_branch does not match origin/$default_branch. Pull or push before releasing."
  exit 1
fi
if [[ -n "$(git status --porcelain)" ]]; then
  if [[ "$dry_run" == true ]]; then
    echo "Warning: the worktree is not clean."
  else
    echo "Error: commit or stash all changes before releasing."
    exit 1
  fi
fi
if git rev-parse --verify --quiet "refs/tags/$tag" >/dev/null || [[ -n "$(git ls-remote --tags origin "refs/tags/$tag")" ]]; then
  echo "Error: tag $tag already exists."
  exit 1
fi

previous_tag=$(git describe --tags --abbrev=0 2>/dev/null || true)
range="HEAD"
if [[ -n "$previous_tag" ]]; then
  range="$previous_tag..HEAD"
fi

echo "Release: $tag"
echo "Branch: $default_branch"
echo "Commits:"
git log --format='  %s' "$range"

if [[ "$dry_run" == true ]]; then
  echo "Dry run complete. Would update package.json and CHANGELOG.md, commit [Release] $tag, create an annotated tag, and atomically push the branch and tag."
  exit 0
fi

release_date=$(date +%Y-%m-%d)
temporary=$(mktemp)
release_in_progress=true
rollback_release() {
  status=$?
  rm -f "$temporary"
  if [[ "$release_in_progress" == true ]]; then
    git tag --delete "$tag" >/dev/null 2>&1 || true
    git reset --hard "$starting_head" >/dev/null 2>&1 || true
  fi
  exit "$status"
}
trap rollback_release EXIT

{
  inserted=false
  while IFS= read -r line; do
    echo "$line"
    if [[ "$line" == "## [Unreleased]" && "$inserted" == false ]]; then
      echo
      echo "## [$version] - $release_date"
      echo
      git log --format='- %s' "$range"
      inserted=true
    fi
  done < CHANGELOG.md
} > "$temporary"
mv "$temporary" CHANGELOG.md

npm version "$version" --no-git-tag-version
git add CHANGELOG.md package.json pnpm-lock.yaml
git commit -m "[Release] $tag"
git tag --annotate "$tag" --message "Release $tag"
git push --atomic origin "$default_branch" "$tag"

release_in_progress=false
trap - EXIT
echo "Released $tag. GitHub Actions will publish the container image and GitHub release."
