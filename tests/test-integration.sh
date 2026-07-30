#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)

run_tests() {
  (cd "$root" && go test -p 4 -count=1 -tags=integration ./...)
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
  --publish 127.0.0.1::5432 \
  --tmpfs /var/lib/postgresql/data \
  postgres:17.7-alpine3.23@sha256:bb377b7239d2774ac8cc76f481596ce96c5a6b5e9d141f6d0a0ee371a6e7c0f2 >/dev/null

ready=false
endpoint=
for _ in $(seq 1 60); do
  endpoint=$(docker port "$container" 5432/tcp 2>/dev/null | head -n 1 || true)
  # The image's initialization server only listens on its Unix socket and
  # shuts down before the final server starts. Probe TCP so that temporary
  # initialization readiness cannot race with loading the fixture.
  if [ -n "$endpoint" ] && docker exec "$container" \
    psql --host 127.0.0.1 --username postgres --dbname postgres \
      --command 'SELECT 1' >/dev/null 2>&1; then
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

docker exec --interactive "$container" \
  psql --username postgres --dbname postgres --set ON_ERROR_STOP=1 \
  < "$root/tests/fixtures/init-database.sql" >/dev/null

port=${endpoint##*:}
MEMENTO_TEST_DATABASE_URL="postgresql://memento_app:test-only-password@127.0.0.1:$port/memento?sslmode=disable" \
  run_tests
