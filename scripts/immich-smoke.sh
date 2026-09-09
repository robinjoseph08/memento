#!/usr/bin/env bash
# Boot a fresh official Immich release, run the production import, and remove only this project.
set -euo pipefail
set +x

release=v3.1.0
while (( $# )); do
  case "$1" in
    --version)
      (( $# >= 2 )) || { printf 'Missing value for --version\n' >&2; exit 1; }
      release=$2
      shift 2
      ;;
    --help|-h)
      printf 'Usage: mise test:immich [--version vMAJOR.MINOR.PATCH]\nDefault: v3.1.0. Requires stable Immich 3.0.x or 3.1.x.\n'
      exit 0
      ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 1 ;;
  esac
done
[[ $release =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || {
  printf 'Version must be an exact stable tag such as v3.1.0\n' >&2; exit 1;
}
case "$release" in
  v3.0.*|v3.1.*) ;;
  *) printf 'Unsupported Immich release %s: imports require stable 3.0.x or 3.1.x\n' "$release" >&2; exit 1 ;;
esac

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
for tool in docker curl go shasum; do
  command -v "$tool" >/dev/null || { printf 'Missing required tool: %s\n' "$tool" >&2; exit 1; }
done
docker compose version >/dev/null
docker info >/dev/null
umask 077
mkdir -p "$root/tmp"
work=$(mktemp -d "$root/tmp/immich-smoke.XXXXXXXX")
export SMOKE_WORK_DIR="$work"
COMPOSE_PROJECT_NAME="memento-immich-smoke-$(date +%s)-$$"
export COMPOSE_PROJECT_NAME
# Override inherited operator configuration, including bind mounts and image tags.
export IMMICH_VERSION="$release" UPLOAD_LOCATION=smoke-upload DB_DATA_LOCATION=smoke-db
export DB_USERNAME=postgres DB_PASSWORD=smoke DB_DATABASE_NAME=immich
compose=(docker compose --project-name "$COMPOSE_PROJECT_NAME" --env-file "$work/.env"
  --file "$work/compose.yaml" --file "$root/scripts/immich-smoke.override.yaml")
started=false
cleanup() {
  status=$?
  trap - EXIT INT TERM
  if $started; then
    if ! "${compose[@]}" down --timeout 15 --volumes; then
      printf 'Cleanup failed for disposable project %s. Its Compose files remain in %s.\n' "$COMPOSE_PROJECT_NAME" "$work" >&2
      exit 1
    fi
  fi
  # This exact mktemp directory contains Compose/config files, never bind-mounted data.
  rm -rf -- "$work"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

curl --fail --silent --show-error --location --max-time 60 \
  "https://github.com/immich-app/immich/releases/download/$release/docker-compose.yml" \
  --output "$work/compose.yaml"
# SHA-256 of the official release assets downloaded from the URLs above.
case "$release" in
  v3.1.0) expected=c651a8211c9ab152bf7060ba1f370737ad2b4872da788a6c8a9e7fa7d4217357 ;;
  v3.0.3) expected=da6f0ca9156c1716e69b3067cb8775b735a4a69fb478a8719e56cf7ea3e3d246 ;;
  *) expected= ;;
esac
if [[ -n $expected ]]; then
  actual=$(shasum -a 256 "$work/compose.yaml")
  [[ ${actual%% *} == "$expected" ]] || { printf 'Official Compose checksum mismatch\n' >&2; exit 1; }
else
  printf 'No pinned Compose checksum for %s; testing its official release asset.\n' "$release"
fi
printf '%s\n' "IMMICH_VERSION=$release" 'UPLOAD_LOCATION=smoke-upload' \
  'DB_DATA_LOCATION=smoke-db' 'DB_USERNAME=postgres' 'DB_PASSWORD=smoke' \
  'DB_DATABASE_NAME=immich' > "$work/.env"
# Immich 3.0.3 skips person thumbnail jobs when both facial recognition and face import are disabled.
printf '%s\n' '{"machineLearning":{"enabled":false},"metadata":{"faces":{"import":true}},"reverseGeocoding":{"enabled":false}}' > "$work/immich-config.json"
printf 'Starting disposable %s with Immich %s\n' "$COMPOSE_PROJECT_NAME" "$IMMICH_VERSION"
started=true
"${compose[@]}" up --detach --wait --wait-timeout 300 immich-server memento-db
immich_address=$("${compose[@]}" port immich-server 2283)
db_address=$("${compose[@]}" port memento-db 5432)
export MEMENTO_SMOKE_DISPOSABLE=1
export MEMENTO_SMOKE_IMMICH_URL="http://$immich_address"
export MEMENTO_SMOKE_DATABASE_URL="postgres://smoke:smoke@$db_address/smoke?sslmode=disable"
go run ./cmd/immich-smoke --version "$release"
