#!/usr/bin/env bash
# Exercise the shipped image over HTTP with Immich offline before and after claim.
set -euo pipefail
set +x
image=${1:?Usage: production-smoke.sh IMAGE, with DATABASE_URL pointing to a disposable database}
: "${DATABASE_URL:?A disposable DATABASE_URL is required}"
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
umask 077
mkdir -p "$root/tmp"
work=$(mktemp -d "$root/tmp/production-smoke.XXXXXXXX")
mkdir "$work/artifacts"
container=
cleanup() {
  status=$?
  trap - EXIT
  set +e
  if [[ -n $container ]]; then
    docker logs "$container" 2>&1 | python3 "$root/scripts/smoke-redact.py" > "$work/artifacts/container.log"
    docker rm --force "$container" >/dev/null || status=1
  fi
  rm -f -- "$work/app.yaml" "$work/cookies" "$work/api-missing"
  printf 'exit_code=%s\n' "$status" > "$work/artifacts/result.txt"
  printf 'Production smoke evidence: %s/artifacts\n' "$work"
  exit "$status"
}
trap cleanup EXIT
trap 'printf "Production smoke failed at line %s\n" "$LINENO" | tee "$work/artifacts/failure.txt" >&2' ERR
trap 'exit 130' INT
trap 'exit 143' TERM
# Public URL and offline integration come from YAML. The environment overrides
# the YAML listener port, and supplies credentials without putting them in YAML.
printf '%s\n' 'public_url: http://127.0.0.1:18080' 'immich_url: http://127.0.0.1:1' \
  'server_host: 127.0.0.1' 'server_port: 1' > "$work/app.yaml"
# The non-root application user needs to read this non-secret fixture file.
chmod 644 "$work/app.yaml"
container=$(docker run --detach --network host \
  --mount "type=bind,source=$work/app.yaml,target=/config/app.yaml,readonly" \
  --env DATABASE_URL --env SERVER_PORT=18080 \
  --env APP_ENV=test --env AUTH_MODE=fake \
  --env IMMICH_API_KEY=smoke-fixture-only-key "$image")
base=http://127.0.0.1:18080
for attempt in $(seq 1 30); do
  if curl --fail --silent "$base/health" >/dev/null; then break; fi
  if [[ $attempt == 30 ]]; then printf 'Production health check failed\n' >&2; exit 1; fi
  sleep 1
done
curl --fail --silent "$base/" >/dev/null
curl --fail --silent "$base/setup" | grep '<div id="root"></div>' >/dev/null
asset_status=$(curl --silent --output /dev/null --write-out '%{http_code}' "$base/assets/missing.js")
api_status=$(curl --silent --output "$work/api-missing" --write-out '%{http_code}' "$base/api/missing")
test "$asset_status" = 404
test "$api_status" = 404
grep -q 'not_found' "$work/api-missing"
curl --fail --silent "$base/api/identity/status" | python3 -c 'import json,sys; assert json.load(sys.stdin)["claimed"] is False'
curl --fail --silent "$base/api/setup/connection" | python3 -c 'import json,sys; r=json.load(sys.stdin); assert r["usable"] is False and r["message"]'
# Fake authentication is an explicit test-only setting, not a production default.
curl --fail --silent --cookie-jar "$work/cookies" --header 'Content-Type: application/json' \
  --header "Origin: $base" --data '{"email":"smoke@example.test","display_name":"Smoke curator"}' \
  "$base/api/identity/fake-sign-in" | python3 -c 'import json,sys; assert json.load(sys.stdin)["is_curator"] is True'
curl --fail --silent --cookie "$work/cookies" "$base/api/identity/status" | python3 -c 'import json,sys; assert json.load(sys.stdin)["claimed"] is True'
# Repeat the diagnostic to prove an outage is retryable after the claim.
for attempt in 1 2; do
  curl --fail --silent --cookie "$work/cookies" "$base/api/curator/connection" | python3 -c 'import json,sys; r=json.load(sys.stdin); assert r["usable"] is False and r["message"]'
  curl --fail --silent "$base/health" >/dev/null
done
# Chapter extraction runs the bundled binary as the app user.
docker exec "$container" ffprobe -version | grep '^ffprobe version' >/dev/null
test "$(docker exec "$container" sh -c 'command -v ffprobe')" = /usr/bin/ffprobe
test "$(docker exec "$container" id -u)" != 0
test "$(docker exec "$container" readlink /proc/1/exe)" = /usr/local/bin/app
printf 'PASS production image: YAML and environment, embedded UI, non-root app, bundled ffprobe, and Immich outage before/after claim\n' | tee "$work/artifacts/checks.txt"
