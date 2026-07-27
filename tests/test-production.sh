#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
project="memento-shell-test-$(date +%s)-$$"
image_tag=$project
export MEMENTO_TEST_IMAGE_TAG=$image_tag
temporary=$(mktemp -d)

compose() {
  docker compose --project-name "$project" --file "$root/tests/compose.yaml" "$@"
}

cleanup() {
  compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  docker image rm "memento:$image_tag" >/dev/null 2>&1 || true
  rm -rf "$temporary"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

compose up --no-build --detach --wait --wait-timeout 90 postgres immich
compose exec --no-TTY postgres \
  psql --username postgres --dbname postgres --set ON_ERROR_STOP=1 \
  < "$root/tests/fixtures/init-database.sql" >/dev/null
docker build --tag "memento:$image_tag" "$root"
compose up --no-build --detach --wait --wait-timeout 90 memento
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

curl --fail --silent --dump-header "$temporary/headers" --output "$temporary/index.html" "$base_url/"
grep -q '<title>Memento</title>' "$temporary/index.html"
grep -qi '^Content-Security-Policy:' "$temporary/headers"
if grep -qi '^Server:' "$temporary/headers"; then
  printf 'Caddy exposed its Server header\n' >&2
  exit 1
fi
for asset in $(grep -Eo '(src|href)="/assets/[^"]+"' "$temporary/index.html" | cut -d'"' -f2); do
  curl --fail --silent --output /dev/null "$base_url$asset"
done
curl --fail --silent --output /dev/null "$base_url/manifest.webmanifest"
curl --fail --silent --output /dev/null "$base_url/service-worker.js"
[ "$(curl --fail --silent "$base_url/api/health/live")" = '{"status":"live"}' ]
curl --fail --silent --dump-header "$temporary/setup-headers" --output "$temporary/setup.json" "$base_url/api/setup"
grep -q '"status":"available"' "$temporary/setup.json"
grep -qi '^Cache-Control: no-store' "$temporary/setup-headers"
curl --fail --silent --dump-header "$temporary/invitation-headers" --output "$temporary/invitation.html" \
  "$base_url/invitation?token=test"
grep -q '<title>Memento</title>' "$temporary/invitation.html"
grep -qi '^Referrer-Policy: no-referrer' "$temporary/invitation-headers"
grep -qi '^Cache-Control: no-store' "$temporary/invitation-headers"

compose up --no-build --detach --no-deps front
front_endpoint=$(compose port front 8443 | head -n 1)
[ -n "$front_endpoint" ] || {
  compose logs front
  printf 'front proxy test port was not published\n' >&2
  exit 1
}
front_url="https://localhost:${front_endpoint##*:}"
front_ready=false
for _ in $(seq 1 30); do
  if curl --insecure --fail --silent "$front_url/api/setup" > "$temporary/front-setup.json"; then
    front_ready=true
    break
  fi
  sleep 1
done
[ "$front_ready" = true ] || {
  compose logs front memento
  printf 'front proxy did not become ready\n' >&2
  exit 1
}
grep -q '"status":"available"' "$temporary/front-setup.json"
front_complete_code=$(curl --insecure --silent --output "$temporary/front-complete.json" --write-out '%{http_code}' \
  --header 'Content-Type: application/json' \
  --data '{"verification_token":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","privacy_acknowledged":true,"engagement_acknowledged":true,"interest_list_acknowledged":true,"email_previews_acknowledged":true,"push_guidance_acknowledged":true,"email_preference":"immediate","session_type":"trusted"}' \
  "$front_url/api/setup/complete")
[ "$front_complete_code" = 400 ]
grep -q 'Setup verification is invalid or expired' "$temporary/front-complete.json"
if grep -q 'requires HTTPS' "$temporary/front-complete.json"; then
  printf 'bundled Caddy lost the trusted front proxy HTTPS scheme\n' >&2
  exit 1
fi

compose exec --no-TTY postgres psql --username memento_app --dbname memento --tuples-only --command \
  "SELECT count(*) FROM pg_extension WHERE extname IN ('unaccent', 'pg_trgm');" | grep -Eq '^[[:space:]]*2[[:space:]]*$'
compose exec --no-TTY postgres psql --username postgres --dbname postgres --tuples-only --command \
  "SELECT rolsuper FROM pg_roles WHERE rolname = 'memento_app';" | grep -Eq '^[[:space:]]*f[[:space:]]*$'
compose exec --no-TTY memento sh -c "ps | grep -q '[m]emento' && ps | grep -q '[c]addy'"
compose exec --no-TTY memento sh -c "grep -aq 'America/Los_Angeles' /usr/local/bin/memento"
container=$(compose ps --quiet memento)
image_user=$(docker inspect --format '{{.Config.User}}' "$container")
[ -n "$image_user" ] && [ "$image_user" != 0 ] && [ "$image_user" != root ]
[ "$(compose exec --no-TTY memento id -u)" -ne 0 ]
if compose exec --no-TTY immich wget -q -O /dev/null http://memento:8091/api/health/live 2>/dev/null; then
  printf 'Go API is reachable outside its loopback production boundary\n' >&2
  exit 1
fi

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

if compose logs memento | grep -Eq 'test-only-key|test-only-security-secret|postgresql://|test-only-password'; then
  printf 'container logs exposed a configured secret\n' >&2
  exit 1
fi
