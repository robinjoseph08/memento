#!/usr/bin/env bash
# Boot a fresh official Immich release, run the production import, and remove only this project.
set -euo pipefail
set +x

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
export IMMICH_VERSION=v3.1.0 UPLOAD_LOCATION=smoke-upload DB_DATA_LOCATION=smoke-db
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
  https://github.com/immich-app/immich/releases/download/v3.1.0/docker-compose.yml \
  --output "$work/compose.yaml"
expected=c651a8211c9ab152bf7060ba1f370737ad2b4872da788a6c8a9e7fa7d4217357
actual=$(shasum -a 256 "$work/compose.yaml")
[[ ${actual%% *} == "$expected" ]] || { printf 'Official Compose checksum mismatch\n' >&2; exit 1; }
printf '%s\n' 'IMMICH_VERSION=v3.1.0' 'UPLOAD_LOCATION=smoke-upload' \
  'DB_DATA_LOCATION=smoke-db' 'DB_USERNAME=postgres' 'DB_PASSWORD=smoke' \
  'DB_DATABASE_NAME=immich' > "$work/.env"
printf '%s\n' '{"machineLearning":{"enabled":false},"reverseGeocoding":{"enabled":false}}' > "$work/immich-config.json"
printf 'Starting disposable %s with Immich %s\n' "$COMPOSE_PROJECT_NAME" "$IMMICH_VERSION"
started=true
"${compose[@]}" up --detach --wait --wait-timeout 300 immich-server memento-db
immich_address=$("${compose[@]}" port immich-server 2283)
db_address=$("${compose[@]}" port memento-db 5432)
export MEMENTO_SMOKE_DISPOSABLE=1
export MEMENTO_SMOKE_IMMICH_URL="http://$immich_address"
export MEMENTO_SMOKE_DATABASE_URL="postgres://smoke:smoke@$db_address/smoke?sslmode=disable"
go run ./cmd/immich-smoke
