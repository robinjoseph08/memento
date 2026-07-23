#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)

run_tests() {
  go test -count=1 -tags=integration ./...
}

if [ -n "${MEMENTO_TEST_DATABASE_URL:-}" ]; then
  run_tests
  exit
fi

container="memento-integration-$(date +%s)-$$"
cleanup() {
  docker rm --force "$container" >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

docker run --detach \
  --name "$container" \
  --env POSTGRES_DB=postgres \
  --env POSTGRES_USER=postgres \
  --env POSTGRES_PASSWORD=test-admin-only-password \
  --mount "type=bind,source=$root/deploy/init-test-database.sql,target=/docker-entrypoint-initdb.d/init-test-database.sql,readonly" \
  --publish 127.0.0.1::5432 \
  --tmpfs /var/lib/postgresql/data \
  postgres:17.7-alpine3.23 >/dev/null

ready=false
endpoint=
for _ in $(seq 1 60); do
  endpoint=$(docker port "$container" 5432/tcp 2>/dev/null | head -n 1 || true)
  if [ -n "$endpoint" ] && docker exec "$container" \
    psql --username memento_app --dbname memento --command 'SELECT 1' >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done

if [ "$ready" != true ]; then
  docker logs "$container" >&2
  echo "integration PostgreSQL did not become ready" >&2
  exit 1
fi

port=${endpoint##*:}
MEMENTO_TEST_DATABASE_URL="postgresql://memento_app:test-only-password@127.0.0.1:$port/memento?sslmode=disable" \
  run_tests
