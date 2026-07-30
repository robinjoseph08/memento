#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
report_dir=${MEMENTO_PERFORMANCE_REPORT_DIR:-"$root/tmp/performance"}
mkdir -p "$report_dir"

run_tests() {
  (cd "$root" && MEMENTO_PERFORMANCE_REPORT_DIR="$report_dir" \
    go test -count=1 -timeout=50m -tags='integration performance' ./tests/performance)
}

if [ -n "${MEMENTO_TEST_DATABASE_URL:-}" ]; then
  run_tests
  exit
fi

container="memento-performance-$(date +%s)-$$"
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
  echo "performance PostgreSQL did not become ready" >&2
  exit 1
fi

docker exec --interactive "$container" \
  psql --username postgres --dbname postgres --set ON_ERROR_STOP=1 \
  < "$root/tests/fixtures/init-database.sql" >/dev/null

port=${endpoint##*:}
MEMENTO_TEST_DATABASE_URL="postgresql://memento_app:test-only-password@127.0.0.1:$port/memento?sslmode=disable" \
  run_tests
