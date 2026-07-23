#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
project=memento-shell-test
compose="docker compose --project-name $project --file $root/deploy/compose.test.yml"
temporary=$(mktemp -d)

cleanup() {
  $compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$temporary"
}
trap cleanup EXIT INT TERM

$compose down --volumes --remove-orphans >/dev/null 2>&1 || true
$compose up --build --detach --wait --wait-timeout 90

ready_body=$temporary/ready.json
ready_code=000
for _ in $(seq 1 60); do
  ready_code=$(curl --silent --output "$ready_body" --write-out '%{http_code}' http://127.0.0.1:18080/api/health/ready || true)
  [ "$ready_code" = 200 ] && break
  sleep 1
done
[ "$ready_code" = 200 ] || {
  $compose logs
  printf 'readiness did not become healthy: HTTP %s\n' "$ready_code" >&2
  exit 1
}
grep -q '"status":"ready"' "$ready_body"
grep -q '"postgresql":"ok"' "$ready_body"
grep -q '"migrations":"ok"' "$ready_body"
grep -q '"worker":"ok"' "$ready_body"
grep -q '"immich":"ok"' "$ready_body"

curl --fail --silent http://127.0.0.1:18080/ | grep -q '<title>Memento</title>'
[ "$(curl --fail --silent http://127.0.0.1:18080/api/health/live)" = '{"status":"live"}' ]
curl --fail --silent --dump-header "$temporary/headers" --output /dev/null http://127.0.0.1:18080/
grep -qi '^Content-Security-Policy:' "$temporary/headers"
if grep -qi '^Server:' "$temporary/headers"; then
  printf 'Caddy exposed its Server header\n' >&2
  exit 1
fi

$compose exec --no-TTY postgres psql --username memento_app --dbname memento --tuples-only --command \
  "SELECT count(*) FROM pg_extension WHERE extname IN ('unaccent', 'pg_trgm');" | grep -Eq '^[[:space:]]*2[[:space:]]*$'
$compose exec --no-TTY postgres psql --username memento_app --dbname memento --tuples-only --command \
  "SELECT count(*) FROM bun_migrations;" | grep -Eq '^[[:space:]]*1[[:space:]]*$'
$compose exec --no-TTY memento sh -c "ps | grep -q '[m]emento' && ps | grep -q '[c]addy'"

$compose stop immich
ready_code=$(curl --silent --output "$ready_body" --write-out '%{http_code}' http://127.0.0.1:18080/api/health/ready)
[ "$ready_code" = 503 ]
grep -q '"immich":"unavailable"' "$ready_body"
if grep -Eq 'test-only-key|postgresql://|http://immich|test-only-password' "$ready_body"; then
  printf 'readiness exposed private dependency configuration\n' >&2
  exit 1
fi
[ "$(curl --fail --silent http://127.0.0.1:18080/api/health/live)" = '{"status":"live"}' ]
$compose start immich

container=$($compose ps --quiet memento)
started=$(date +%s)
docker kill --signal TERM "$container" >/dev/null
for _ in $(seq 1 12); do
  running=$(docker inspect --format '{{.State.Running}}' "$container")
  [ "$running" = false ] && break
  sleep 1
done
running=$(docker inspect --format '{{.State.Running}}' "$container")
[ "$running" = false ]
status=$(docker inspect --format '{{.State.ExitCode}}' "$container")
[ "$status" = 0 ]
elapsed=$(($(date +%s) - started))
[ "$elapsed" -le 10 ]
$compose exec --no-TTY postgres psql --username memento_app --dbname memento --tuples-only --command \
  "SELECT count(*) FROM jobs WHERE status = 'running' AND lease_expires_at IS NULL;" | grep -Eq '^[[:space:]]*0[[:space:]]*$'

if $compose logs memento | grep -Eq 'test-only-key|postgresql://|test-only-password'; then
  printf 'container logs exposed a configured secret\n' >&2
  exit 1
fi
