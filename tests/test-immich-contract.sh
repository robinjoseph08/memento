#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
compose_file="$root/tests/fixtures/immich-contract.compose.yaml"
project_base="memento-immich-contract-$(date +%s)-$$"
project="$project_base-1"

cleanup() {
  docker compose --project-name "$project" --file "$compose_file" down --volumes >/dev/null 2>&1 || true
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

attempt=1
while [ "$attempt" -le 2 ]; do
  docker compose --project-name "$project" --file "$compose_file" up --detach --quiet-pull
  endpoint=$(docker compose --project-name "$project" --file "$compose_file" port server 2283)
  base_url="http://127.0.0.1:${endpoint##*:}"

  ready=false
  if wait_for_server "$base_url"; then
    # The initial connection can cache Immich's custom PostgreSQL types before
    # fresh-database migrations create them. Restart after migration so enum
    # arrays use the right type on the second connection.
    docker compose --project-name "$project" --file "$compose_file" restart server
    endpoint=$(docker compose --project-name "$project" --file "$compose_file" port server 2283)
    base_url="http://127.0.0.1:${endpoint##*:}"
    if wait_for_server "$base_url"; then
      ready=true
    fi
  fi

  if [ "$ready" = true ] && MEMENTO_TEST_IMMICH_URL="$base_url" \
    go test -count=1 -tags=immichcontract ./pkg/immich; then
    exit 0
  fi

  docker compose --project-name "$project" --file "$compose_file" logs server database redis >&2
  cleanup
  if [ "$attempt" -eq 2 ]; then
    echo "Immich v3.0.3 contract failed twice" >&2
    exit 1
  fi

  echo "Immich v3.0.3 fixture failed during fresh bootstrap; retrying once" >&2
  attempt=$((attempt + 1))
  project="$project_base-$attempt"
done
