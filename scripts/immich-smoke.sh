#!/usr/bin/env bash
# Boot an official release in an isolated project. Retain only redacted evidence.
set -euo pipefail
set +x

release=v3.2.1
probe=false
ml=false
permission_failure=
while (( $# )); do
  case "$1" in
    --version|--permission-failure)
      (( $# >= 2 )) || { printf 'Missing value for %s\n' "$1" >&2; exit 1; }
      if [[ $1 == --version ]]; then release=$2; else permission_failure=$2; fi
      shift 2
      ;;
    --probe) probe=true; shift ;;
    --ml) ml=true; shift ;;
    --help|-h)
      printf 'Usage: mise test:immich [--version vMAJOR.MINOR.PATCH] [--probe] [--ml] [--permission-failure NAME]\nDefault: v3.2.1. --probe bypasses only the test command import policy.\nEvidence is retained in tmp/immich-smoke.*/artifacts.\n'
      exit 0
      ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 1 ;;
  esac
done
[[ $release =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || {
  printf 'Version must be an exact stable tag such as v3.2.1\n' >&2; exit 1;
}
[[ -z $permission_failure || $permission_failure =~ ^[a-z]+\.[a-z]+$ ]] || {
  printf 'Permission must be a name such as asset.read\n' >&2; exit 1;
}

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
for tool in docker curl go shasum python3; do
  command -v "$tool" >/dev/null || { printf 'Missing required tool: %s\n' "$tool" >&2; exit 1; }
done
umask 077
mkdir -p "$root/tmp"
work=$(mktemp -d "$root/tmp/immich-smoke.XXXXXXXX")
artifacts="$work/artifacts"
mkdir "$artifacts"
export SMOKE_WORK_DIR="$work"
COMPOSE_PROJECT_NAME="memento-immich-smoke-$(date +%s)-$$"
export COMPOSE_PROJECT_NAME
# Override operator settings, including bind mounts, credentials, and image tags.
export IMMICH_VERSION="$release" UPLOAD_LOCATION=smoke-upload DB_DATA_LOCATION=smoke-db
export DB_USERNAME=postgres DB_PASSWORD=smoke DB_DATABASE_NAME=immich
unset COMPOSE_FILE COMPOSE_PROFILES COMPOSE_ENV_FILES
compose=(docker compose --project-name "$COMPOSE_PROJECT_NAME" --env-file "$work/.env"
  --file "$work/compose.yaml" --file "$root/scripts/immich-smoke.override.yaml")
# Keep profile selection for logs, digest inspection, and teardown as well as up.
if $ml; then compose+=(--profile ml); fi
redact=(python3 "$root/scripts/smoke-redact.py")
run_logged() { "$@" 2>&1 | "${redact[@]}" | tee -a "$artifacts/run.log"; }
started=false
phase=prerequisites
printf 'requested_version=%s\nprobe=%s\nml=%s\npermission_failure=%s\nproject=%s\n' \
  "$release" "$probe" "$ml" "$permission_failure" "$COMPOSE_PROJECT_NAME" > "$artifacts/request.txt"
record_images() {
  local id image ids
  # Inspect selected fields only. Full inspect/config output contains credentials.
  ids=$("${compose[@]}" ps --all --quiet) || return 1
  [[ -n $ids ]] || return 1
  for id in $ids; do
    docker inspect --format '{{index .Config.Labels "com.docker.compose.service"}} {{.Config.Image}} {{.Image}}' "$id" || return 1
    image=$(docker inspect --format '{{.Image}}' "$id") || return 1
    docker image inspect --format '{{json .RepoDigests}}' "$image" || return 1
  done
}
cleanup() {
  status=$?
  trap - EXIT INT TERM
  set +e
  if $started; then
    if ! record_images 2>&1 | "${redact[@]}" > "$artifacts/images.txt"; then
      if [[ $status == 0 ]]; then phase=evidence; fi
      status=1
    fi
    if ! "${compose[@]}" logs --no-color --timestamps 2>&1 | "${redact[@]}" > "$artifacts/compose.log"; then
      if [[ $status == 0 ]]; then phase=evidence; fi
      status=1
    fi
    if ! run_logged "${compose[@]}" down --timeout 15 --volumes --remove-orphans; then
      printf 'Cleanup failed for project %s. Retry using the private files in %s.\n' "$COMPOSE_PROJECT_NAME" "$work" >&2
      status=1
      phase=cleanup
    else
      rm -f -- "$work/.env" "$work/immich-config.json" "$work/compose.yaml" "$work/openapi.json"
    fi
  else
    rm -f -- "$work/.env" "$work/immich-config.json" "$work/compose.yaml" "$work/openapi.json"
  fi
  printf 'exit_code=%s\nphase=%s\n' "$status" "$phase" > "$artifacts/result.txt"
  printf 'Immich smoke evidence: %s\n' "$artifacts"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
run_logged docker compose version
run_logged docker info --format '{{.ServerVersion}}'
phase=download
run_logged curl --fail --silent --show-error --location --max-time 60 \
  "https://github.com/immich-app/immich/releases/download/$release/docker-compose.yml" \
  --output "$work/compose.yaml"
actual=$(shasum -a 256 "$work/compose.yaml")
printf '%s  compose.yaml\n' "${actual%% *}" > "$artifacts/checksums.txt"
# Checksums of the official assets, not of our override or interpolated config.
case "$release" in
  v3.2.1) expected=fa98c3eb0884b0b88aaee11a9c9acb8cfb17a0f495d12c21358deb6d3e8a546b ;;
  v3.1.0) expected=c651a8211c9ab152bf7060ba1f370737ad2b4872da788a6c8a9e7fa7d4217357 ;;
  v3.0.3) expected=da6f0ca9156c1716e69b3067cb8775b735a4a69fb478a8719e56cf7ea3e3d246 ;;
  v2.7.5) expected=69da59c813f1382400a8fe9fce9f51ac00a4d05d7d1571a0d49dcd517ae41ad5 ;;
  *) expected= ;;
esac
if [[ -n $expected ]]; then
  [[ ${actual%% *} == "$expected" ]] || { printf 'Official Compose checksum mismatch\n' >&2; exit 1; }
fi
run_logged curl --fail --silent --show-error --location --max-time 60 \
  "https://raw.githubusercontent.com/immich-app/immich/$release/open-api/immich-openapi-specs.json" \
  --output "$work/openapi.json"
actual=$(shasum -a 256 "$work/openapi.json")
printf '%s  openapi.json\n' "${actual%% *}" >> "$artifacts/checksums.txt"
printf '%s\n' "IMMICH_VERSION=$release" 'UPLOAD_LOCATION=smoke-upload' \
  'DB_DATA_LOCATION=smoke-db' 'DB_USERNAME=postgres' 'DB_PASSWORD=smoke' \
  'DB_DATABASE_NAME=immich' > "$work/.env"
# Core smoke uses manual faces. The separate ML run enables actual recognition.
printf '%s\n' "{\"machineLearning\":{\"enabled\":$ml},\"metadata\":{\"faces\":{\"import\":true}},\"reverseGeocoding\":{\"enabled\":false}}" > "$work/immich-config.json"
printf 'Starting disposable %s with Immich %s\n' "$COMPOSE_PROJECT_NAME" "$IMMICH_VERSION"
phase=startup
started=true
services=(immich-server memento-db)
if $ml; then services+=(immich-machine-learning); fi
run_logged "${compose[@]}" up --detach --wait --wait-timeout 600 "${services[@]}"
immich_address=$("${compose[@]}" port immich-server 2283)
db_address=$("${compose[@]}" port memento-db 5432)
[[ $immich_address == 127.0.0.1:* && $db_address == 127.0.0.1:* ]] || {
  printf 'Smoke services must bind only to loopback\n' >&2; exit 1;
}
export MEMENTO_SMOKE_DISPOSABLE=1
export MEMENTO_SMOKE_IMMICH_URL="http://$immich_address"
export MEMENTO_SMOKE_DATABASE_URL="postgres://smoke:smoke@$db_address/smoke?sslmode=disable"
args=(--version "$release" --openapi "$work/openapi.json")
if $probe; then args+=(--probe); fi
if $ml; then args+=(--ml); fi
if [[ -n $permission_failure ]]; then args+=(--permission-failure "$permission_failure"); fi
phase='test'
run_logged go run ./cmd/immich-smoke "${args[@]}"
phase=complete
