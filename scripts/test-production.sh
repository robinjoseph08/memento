#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
project="memento-shell-test-$(date +%s)-$$"
image_tag=$project
export MEMENTO_TEST_IMAGE_TAG=$image_tag
temporary=$(mktemp -d)

compose() {
  docker compose --project-name "$project" --file "$root/deploy/compose.test.yml" "$@"
}

cleanup() {
  compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  docker image rm "memento:$image_tag" >/dev/null 2>&1 || true
  rm -rf "$temporary"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

compose up --build --detach --wait --wait-timeout 90
endpoint=$(compose port memento 8080 | head -n 1)
[ -n "$endpoint" ] || {
  compose logs
  echo "production test port was not published" >&2
  exit 1
}
base_url="http://$endpoint"

ready_body=$temporary/ready.json
ready_code=000
for _ in $(seq 1 60); do
  ready_code=$(curl --silent --output "$ready_body" --write-out '%{http_code}' "$base_url/api/health/ready" || true)
  [ "$ready_code" = 200 ] && break
  sleep 1
done
[ "$ready_code" = 200 ] || {
  compose logs
  printf 'readiness did not become healthy: HTTP %s\n' "$ready_code" >&2
  exit 1
}
grep -q '"status":"ready"' "$ready_body"
grep -q '"postgresql":"ok"' "$ready_body"
grep -q '"migrations":"ok"' "$ready_body"
grep -q '"worker":"ok"' "$ready_body"
grep -q '"immich":"ok"' "$ready_body"

curl --fail --silent "$base_url/" > "$temporary/index.html"
grep -q '<title>Memento</title>' "$temporary/index.html"
for asset in $(grep -Eo '(src|href)="/assets/[^"]+"' "$temporary/index.html" | cut -d'"' -f2); do
  curl --fail --silent --output /dev/null "$base_url$asset"
done
curl --fail --silent --output /dev/null "$base_url/manifest.webmanifest"
curl --fail --silent --output /dev/null "$base_url/service-worker.js"
[ "$(curl --fail --silent "$base_url/api/health/live")" = '{"status":"live"}' ]
api_code=$(curl --silent --output "$temporary/api.json" --write-out '%{http_code}' "$base_url/api")
[ "$api_code" = 404 ]
grep -q '"code":"not_found"' "$temporary/api.json"
curl --fail --silent --dump-header "$temporary/headers" --output /dev/null "$base_url/"
grep -qi '^Content-Security-Policy:' "$temporary/headers"
if grep -qi '^Server:' "$temporary/headers"; then
  printf 'Caddy exposed its Server header\n' >&2
  exit 1
fi

compose exec --no-TTY postgres psql --username memento_app --dbname memento --tuples-only --command \
  "SELECT count(*) FROM pg_extension WHERE extname IN ('unaccent', 'pg_trgm');" | grep -Eq '^[[:space:]]*2[[:space:]]*$'
compose exec --no-TTY postgres psql --username memento_app --dbname memento --tuples-only --command \
  "SELECT count(*) FROM bun_migrations;" | grep -Eq '^[[:space:]]*1[[:space:]]*$'
compose exec --no-TTY postgres psql --username postgres --dbname postgres --tuples-only --command \
  "SELECT rolsuper FROM pg_roles WHERE rolname = 'memento_app';" | grep -Eq '^[[:space:]]*f[[:space:]]*$'
compose exec --no-TTY memento sh -c "ps | grep -q '[m]emento' && ps | grep -q '[c]addy'"
container=$(compose ps --quiet memento)
image_user=$(docker inspect --format '{{.Config.User}}' "$container")
[ -n "$image_user" ] && [ "$image_user" != 0 ] && [ "$image_user" != root ]
[ "$(compose exec --no-TTY memento id -u)" -ne 0 ]
if compose exec --no-TTY immich wget -q -O /dev/null http://memento:8091/api/health/live 2>/dev/null; then
  printf 'Go API is reachable outside its loopback production boundary\n' >&2
  exit 1
fi

compose stop immich
ready_code=$(curl --silent --output "$ready_body" --write-out '%{http_code}' "$base_url/api/health/ready")
[ "$ready_code" = 503 ]
grep -q '"immich":"unavailable"' "$ready_body"
if grep -Eq 'test-only-key|postgresql://|http://immich|test-only-password' "$ready_body"; then
  printf 'readiness exposed private dependency configuration\n' >&2
  exit 1
fi
[ "$(curl --fail --silent "$base_url/api/health/live")" = '{"status":"live"}' ]
compose start immich

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
compose exec --no-TTY postgres psql --username memento_app --dbname memento --tuples-only --command \
  "SELECT count(*) FROM jobs WHERE status = 'running' AND lease_expires_at IS NULL;" | grep -Eq '^[[:space:]]*0[[:space:]]*$'

if compose logs memento | grep -Eq 'test-only-key|postgresql://|test-only-password'; then
  printf 'container logs exposed a configured secret\n' >&2
  exit 1
fi
