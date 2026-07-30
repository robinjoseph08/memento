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
wait_for_readiness() {
  expected_code=$1
  expected_pattern=$2
  attempts=$3
  failure_message=$4
  shift 4
  ready_code=000
  for _ in $(seq 1 "$attempts"); do
    ready_code=$(curl --silent --max-time 5 --output "$ready_body" --write-out '%{http_code}' "$base_url/api/health/ready" || true)
    if [ "$ready_code" = "$expected_code" ] && grep -q "$expected_pattern" "$ready_body"; then
      return 0
    fi
    sleep 1
  done
  compose logs "$@" >&2 || true
  printf '%s: last HTTP status %s, body: ' "$failure_message" "$ready_code" >&2
  cat "$ready_body" >&2 || true
  printf '\n' >&2
  return 1
}

wait_for_readiness 200 '"status":"ready"' 60 'readiness did not become healthy' postgres immich memento
grep -q '"status":"ready"' "$ready_body"
grep -q '"postgresql":"ok"' "$ready_body"
grep -q '"migrations":"ok"' "$ready_body"
grep -q '"worker":"ok"' "$ready_body"
grep -q '"immich":"ok"' "$ready_body"

curl --fail --silent --dump-header "$temporary/headers" --output "$temporary/index.html" "$base_url/"
grep -q '<title>Memento</title>' "$temporary/index.html"
grep -qi "^Content-Security-Policy: default-src 'self'.*frame-ancestors 'none'" "$temporary/headers"
grep -qi '^Cross-Origin-Opener-Policy: same-origin' "$temporary/headers"
grep -qi '^Cross-Origin-Resource-Policy: same-origin' "$temporary/headers"
grep -qi '^Permissions-Policy: camera=(), geolocation=(), microphone=()' "$temporary/headers"
grep -qi '^Referrer-Policy: no-referrer' "$temporary/headers"
grep -qi '^Strict-Transport-Security: max-age=31536000; includeSubDomains' "$temporary/headers"
grep -qi '^X-Content-Type-Options: nosniff' "$temporary/headers"
grep -qi '^X-Frame-Options: DENY' "$temporary/headers"
if grep -qi '^Server:' "$temporary/headers"; then
  printf 'Caddy exposed its Server header\n' >&2
  exit 1
fi
for asset in $(grep -Eo '(src|href)="/assets/[^"]+"' "$temporary/index.html" | cut -d'"' -f2); do
  curl --fail --silent --output /dev/null "$base_url$asset"
done
curl --fail --silent --output /dev/null "$base_url/manifest.webmanifest"
curl --fail --silent --output /dev/null "$base_url/service-worker.js"
[ "$(curl --fail --silent --max-time 5 "$base_url/api/health/live")" = '{"status":"live"}' ]
curl --fail --silent --dump-header "$temporary/setup-headers" --output "$temporary/setup.json" "$base_url/api/setup"
grep -q '"status":"available"' "$temporary/setup.json"
grep -qi '^Cache-Control: no-store' "$temporary/setup-headers"
curl --fail --silent --dump-header "$temporary/invitation-headers" --output "$temporary/invitation.html" \
  "$base_url/invitation?token=test"
grep -q '<title>Memento</title>' "$temporary/invitation.html"
grep -qi "$(printf '^Referrer-Policy: no-referrer\r$')" "$temporary/invitation-headers"
grep -qi '^Cache-Control: no-store' "$temporary/invitation-headers"

hostile_origin_code=$(curl --silent --output "$temporary/hostile-origin.json" --write-out '%{http_code}' \
  --header 'Origin: https://evil.example' --header 'Content-Type: application/json' --data '{}' \
  "$base_url/api/setup/complete")
[ "$hostile_origin_code" = 403 ]
if grep -q 'evil.example' "$temporary/hostile-origin.json"; then
  printf 'origin denial reflected an unapproved origin\n' >&2
  exit 1
fi
approved_origin_code=$(curl --silent --dump-header "$temporary/approved-origin-headers" \
  --output "$temporary/approved-origin.json" --write-out '%{http_code}' \
  --header 'Origin: http://localhost:8080' --header 'Content-Type: application/json' --data '{}' \
  "$base_url/api/setup/complete")
[ "$approved_origin_code" != 403 ]
grep -qi '^Access-Control-Allow-Origin: http://localhost:8080' "$temporary/approved-origin-headers"
simple_mutation_code=$(curl --silent --output "$temporary/simple-mutation.json" --write-out '%{http_code}' \
  --header 'Content-Type: text/plain' --data 'private input' "$base_url/api/setup/complete")
[ "$simple_mutation_code" = 415 ]
preflight_code=$(curl --silent --dump-header "$temporary/preflight-headers" --output /dev/null --write-out '%{http_code}' \
  --request OPTIONS --header 'Origin: http://localhost:8080' \
  --header 'Access-Control-Request-Method: POST' \
  --header 'Access-Control-Request-Headers: content-type, x-memento-csrf' \
  "$base_url/api/setup/complete")
[ "$preflight_code" = 204 ]
grep -qi '^Access-Control-Allow-Origin: http://localhost:8080' "$temporary/preflight-headers"
grep -qi '^Access-Control-Allow-Credentials: true' "$temporary/preflight-headers"
protected_code=$(curl --silent --dump-header "$temporary/protected-headers" \
  --output "$temporary/protected.json" --write-out '%{http_code}' "$base_url/api/me/photos")
[ "$protected_code" = 401 ]
grep -qi '^Cache-Control: private, no-store' "$temporary/protected-headers"

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
  if curl --insecure --fail --silent --max-time 5 "$front_url/api/setup" > "$temporary/front-setup.json"; then
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
compose exec --no-TTY memento test -x /usr/local/bin/memento-migrations
compose exec --no-TTY memento grep -q 'MIT License' /usr/share/licenses/memento/LICENSE
[ "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.licenses" }}' "memento:$image_tag")" = MIT ]
[ "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.version" }}' "memento:$image_tag")" = dev ]
[ "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "memento:$image_tag")" = unknown ]
[ "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.source" }}' "memento:$image_tag")" = https://github.com/robinjoseph08/memento ]
container=$(compose ps --quiet memento)
image_user=$(docker inspect --format '{{.Config.User}}' "$container")
[ -n "$image_user" ] && [ "$image_user" != 0 ] && [ "$image_user" != root ]
[ "$(compose exec --no-TTY memento id -u)" -ne 0 ]
if compose exec --no-TTY immich wget -q -O /dev/null http://memento:8091/api/health/live 2>/dev/null; then
  printf 'Go API is reachable outside its loopback production boundary\n' >&2
  exit 1
fi

compose stop immich
wait_for_readiness 503 '"immich":"unavailable"' 15 'readiness did not report the Immich outage' immich memento
if grep -Eq 'test-only-key|test-only-security-secret|postgresql://|http://immich|test-only-password' "$ready_body"; then
  printf 'readiness exposed private dependency configuration\n' >&2
  exit 1
fi
live_code=$(curl --silent --max-time 5 --output "$temporary/live.json" --write-out '%{http_code}' "$base_url/api/health/live")
[ "$live_code" = 200 ]
grep -qx '{"status":"live"}' "$temporary/live.json"

compose start immich >/dev/null
wait_for_readiness 200 '"status":"ready"' 60 'readiness did not recover after Immich restart' immich memento

postgres_container=$(compose ps --quiet postgres)
postgres_network=$(docker inspect --format '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}}{{end}}' "$postgres_container")
[ -n "$postgres_network" ]
docker network disconnect "$postgres_network" "$postgres_container"
wait_for_readiness 503 '"postgresql":"unavailable"' 15 'readiness did not report the PostgreSQL outage' postgres memento
if grep -Eq 'test-only-key|test-only-security-secret|postgresql://|http://immich|test-only-password' "$ready_body"; then
  printf 'PostgreSQL outage readiness exposed private dependency configuration\n' >&2
  exit 1
fi
live_code=$(curl --silent --max-time 5 --output "$temporary/live.json" --write-out '%{http_code}' "$base_url/api/health/live")
[ "$live_code" = 200 ]
grep -qx '{"status":"live"}' "$temporary/live.json"

docker network connect --alias postgres "$postgres_network" "$postgres_container"
wait_for_readiness 200 '"status":"ready"' 60 'readiness did not recover after PostgreSQL network restoration' postgres memento

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
