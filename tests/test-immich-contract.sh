#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
compose_file="$root/tests/fixtures/immich-contract.compose.yaml"
project="memento-immich-contract-$(date +%s)-$$"

compose() {
  docker compose --project-name "$project" --file "$compose_file" "$@"
}

cleanup() {
  compose down --volumes >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

wait_for_server() {
  for _ in $(seq 1 180); do
    if curl --fail --silent --show-error "$1/api/server/ping" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

fail_with_logs() {
  compose ps --all >&2 || true
  compose logs server database redis >&2
  echo "$1" >&2
  exit 1
}

compose up --detach --quiet-pull
endpoint=$(compose port server 2283)
base_url="http://127.0.0.1:${endpoint##*:}"
wait_for_server "$base_url" || fail_with_logs "Immich v3.0.3 did not finish its initial bootstrap"

# The initial connection caches PostgreSQL array types before fresh-database
# migrations create them. Verify the enum exists, then restart so the API
# process loads the complete type map instead of serializing arrays as scalars.
compose exec --no-TTY database \
  psql --username postgres --dbname immich --tuples-only \
    --command "SELECT to_regtype('album_user_role_enum[]') IS NOT NULL;" \
  | grep -Eq '^[[:space:]]*t[[:space:]]*$' \
  || fail_with_logs "Immich v3.0.3 did not create its album role array type"
compose restart server
endpoint=$(compose port server 2283)
base_url="http://127.0.0.1:${endpoint##*:}"
wait_for_server "$base_url" || fail_with_logs "Immich v3.0.3 did not become ready after its planned restart"
database_endpoint=$(compose port database 5432)
database_url="postgres://postgres:testpassword@127.0.0.1:${database_endpoint##*:}/immich?sslmode=disable"

if ! MEMENTO_TEST_IMMICH_URL="$base_url" \
  MEMENTO_TEST_IMMICH_DATABASE_URL="$database_url" \
  go test -count=1 -tags=immichcontract ./pkg/immich; then
  fail_with_logs "Immich v3.0.3 contract failed after deterministic bootstrap"
fi
