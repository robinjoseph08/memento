#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
compose_file="$root/tests/fixtures/immich-contract.compose.yaml"
project="memento-immich-contract-$(date +%s)-$$"

compose() {
  docker compose --project-name "$project" --file "$compose_file" "$@"
}

stage() {
  printf '%s\n' "$1"
}

service_state() {
  state=$(compose ps --all --format '{{.State}}' "$1" 2>/dev/null || true)
  case "$state" in
    running|exited|restarting|paused|created|dead|removing) ;;
    "") state=unavailable ;;
    *) state=unknown ;;
  esac
  printf '%s_state=%s\n' "$1" "$state" >&2
}

cleanup() {
  status=$?
  trap - EXIT
  if ! compose down --volumes --remove-orphans --timeout 15 >/dev/null 2>&1; then
    printf '%s\n' "Pinned Immich fixture cleanup failed" >&2
    if [ "$status" -eq 0 ]; then
      status=1
    fi
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

wait_for_server() {
  for _ in $(seq 1 180); do
    if curl --fail --silent "$1/api/server/ping" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

fail_with_status() {
  service_state server
  service_state database
  service_state redis
  printf '%s\n' "$1" >&2
  exit 1
}

stage "Starting pinned Immich contract fixture"
if ! compose up --detach --quiet-pull >/dev/null 2>&1; then
  fail_with_status "Pinned Immich fixture failed to start"
fi
endpoint=$(compose port server 2283 2>/dev/null) || fail_with_status "Pinned Immich server port was unavailable"
base_url="http://127.0.0.1:${endpoint##*:}"
wait_for_server "$base_url" || fail_with_status "Pinned Immich fixture did not finish initial bootstrap"

# The initial connection caches PostgreSQL array types before fresh-database
# migrations create them. Verify the enum exists, then restart so the API
# process loads the complete type map instead of serializing arrays as scalars.
stage "Validating pinned Immich schema"
schema_ready=$(compose exec --no-TTY database \
  psql --username postgres --dbname immich --tuples-only \
    --command "SELECT to_regtype('album_user_role_enum[]') IS NOT NULL;" 2>/dev/null || true)
printf '%s\n' "$schema_ready" | grep -Eq '^[[:space:]]*t[[:space:]]*$' \
  || fail_with_status "Pinned Immich fixture did not create its album role array type"

stage "Restarting pinned Immich after schema bootstrap"
if ! compose restart server >/dev/null 2>&1; then
  fail_with_status "Pinned Immich fixture failed its planned restart"
fi
endpoint=$(compose port server 2283 2>/dev/null) || fail_with_status "Pinned Immich server port was unavailable after restart"
base_url="http://127.0.0.1:${endpoint##*:}"
wait_for_server "$base_url" || fail_with_status "Pinned Immich fixture did not become ready after its planned restart"
database_endpoint=$(compose port database 5432 2>/dev/null) || fail_with_status "Pinned Immich database port was unavailable"
database_url="postgres://postgres:testpassword@127.0.0.1:${database_endpoint##*:}/immich?sslmode=disable"

stage "Running pinned Immich live contract"
if ! MEMENTO_TEST_IMMICH_URL="$base_url" \
  MEMENTO_TEST_IMMICH_DATABASE_URL="$database_url" \
  go test -count=1 -tags=immichcontract ./pkg/immich; then
  fail_with_status "Pinned Immich live contract failed"
fi
stage "Pinned Immich live contract passed"
