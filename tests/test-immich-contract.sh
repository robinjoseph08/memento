#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
compose_file="$root/tests/fixtures/immich-contract.compose.yaml"
project="memento-immich-contract-$(date +%s)-$$"

cleanup() {
  docker compose --project-name "$project" --file "$compose_file" down --volumes >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

docker compose --project-name "$project" --file "$compose_file" up --detach --quiet-pull
endpoint=$(docker compose --project-name "$project" --file "$compose_file" port server 2283)
port=${endpoint##*:}

ready=false
for _ in $(seq 1 180); do
  if curl --fail --silent --show-error "http://127.0.0.1:$port/api/server/ping" >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done

if [ "$ready" != true ]; then
  docker compose --project-name "$project" --file "$compose_file" logs server >&2
  echo "Immich v3.0.3 contract fixture did not become ready" >&2
  exit 1
fi

if ! MEMENTO_TEST_IMMICH_URL="http://127.0.0.1:$port" \
  go test -count=1 -tags=immichcontract ./pkg/immich; then
  docker compose --project-name "$project" --file "$compose_file" logs server database redis >&2
  exit 1
fi
