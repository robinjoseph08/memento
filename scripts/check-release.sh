#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
tag=${1:-}

if ! printf '%s\n' "$tag" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'; then
  echo "release tag must be vMAJOR.MINOR.PATCH with an optional SemVer prerelease" >&2
  exit 1
fi

version=${tag#v}
prerelease=${version#*-}
if [ "$prerelease" != "$version" ]; then
  old_ifs=$IFS
  IFS=.
  for identifier in $prerelease; do
    case "$identifier" in
      ''|*[!0-9]*) ;;
      0) ;;
      0*)
        echo "numeric prerelease identifiers must not have leading zeroes" >&2
        exit 1
        ;;
    esac
  done
  IFS=$old_ifs
fi

package_version=$(sed -n 's/^[[:space:]]*"version": "\([^"]*\)",$/\1/p' "$root/package.json")
if [ -z "$package_version" ] || [ "$version" != "$package_version" ]; then
  printf 'release version %s does not match package.json version %s\n' "$version" "${package_version:-<missing>}" >&2
  exit 1
fi

for required in \
  LICENSE \
  Dockerfile \
  deploy/compose.production.yaml \
  docs/operator-runbook.md \
  docs/dependency-drills.md \
  docs/release-exercise.md; do
  if [ ! -f "$root/$required" ]; then
    printf 'required release file is missing: %s\n' "$required" >&2
    exit 1
  fi
done

if ! grep -q 'MIT License' "$root/LICENSE"; then
  echo "LICENSE does not contain the MIT License" >&2
  exit 1
fi

printf 'tag=%s\nversion=%s\nlicense=MIT\n' "$tag" "$version"
