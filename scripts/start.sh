#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
instance=${MEMENTO_DEV_INSTANCE:-"$(date +%s)-$$"}
suffix=$(printf '%s-%s' "$instance" "$$" | tr -c '[:alnum:]_.-' '-')
postgres_container="memento-dev-postgres-$suffix"
immich_container="memento-dev-immich-$suffix"
servers_pid=
api_port_lock=
web_port_lock=
managed_postgres=false
managed_immich=false

reserve_port() {
  excluded_port=${1:-}
  for _ in $(seq 1 20); do
    reserved_port=$(node -e 'const net = require("node:net"); const server = net.createServer(); server.listen(0, "127.0.0.1", () => { console.log(server.address().port); server.close(); });')
    if [ "$reserved_port" = "$excluded_port" ]; then
      continue
    fi
    reserved_lock="${TMPDIR:-/tmp}/memento-dev-port-$reserved_port.lock"
    if mkdir "$reserved_lock" 2>/dev/null; then
      return 0
    fi
  done
  echo "Unable to reserve a development server port" >&2
  return 1
}

stop_servers() {
  kill "$servers_pid" 2>/dev/null || true
  (
    sleep 12
    kill -KILL "$servers_pid" 2>/dev/null || true
  ) &
  watchdog_pid=$!
  wait "$servers_pid" 2>/dev/null || true
  kill "$watchdog_pid" 2>/dev/null || true
  wait "$watchdog_pid" 2>/dev/null || true
}

cleanup() {
  if [ -n "$servers_pid" ] && kill -0 "$servers_pid" 2>/dev/null; then
    stop_servers
  fi
  if [ "$managed_postgres" = true ]; then
    docker rm --force "$postgres_container" >/dev/null 2>&1 || true
  fi
  if [ "$managed_immich" = true ]; then
    docker rm --force "$immich_container" >/dev/null 2>&1 || true
  fi
  if [ -n "$api_port_lock" ]; then
    rmdir "$api_port_lock" 2>/dev/null || true
  fi
  if [ -n "$web_port_lock" ]; then
    rmdir "$web_port_lock" 2>/dev/null || true
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if [ -z "${MEMENTO_CONFIG_FILE:-}" ]; then
  if [ -z "${MEMENTO_DATABASE_URL:-}" ] && [ -z "${MEMENTO_DATABASE_URL_FILE:-}" ]; then
    managed_postgres=true
  fi
  if [ -z "${MEMENTO_IMMICH_URL:-}" ]; then
    managed_immich=true
  fi
fi

if [ "$managed_postgres" = true ] || [ "$managed_immich" = true ]; then
  if ! docker info >/dev/null 2>&1; then
    echo "Docker must be running for mise start to provide development services" >&2
    exit 1
  fi
fi

if [ "$managed_postgres" = true ]; then
  docker run --detach \
    --name "$postgres_container" \
    --label "com.memento.dev-instance=$instance" \
    --env POSTGRES_DB=postgres \
    --env POSTGRES_USER=postgres \
    --env POSTGRES_PASSWORD=test-admin-only-password \
    --mount "type=bind,source=$root/deploy/init-test-database.sql,target=/docker-entrypoint-initdb.d/init-test-database.sql,readonly" \
    --publish 127.0.0.1::5432 \
    --tmpfs /var/lib/postgresql/data \
    postgres:17.7-alpine3.23 >/dev/null

  postgres_ready=false
  postgres_endpoint=
  for _ in $(seq 1 60); do
    postgres_endpoint=$(docker port "$postgres_container" 5432/tcp 2>/dev/null | head -n 1 || true)
    if [ -n "$postgres_endpoint" ] && docker exec "$postgres_container" \
      psql --username memento_app --dbname memento --command 'SELECT 1' >/dev/null 2>&1; then
      postgres_ready=true
      break
    fi
    sleep 0.5
  done
  if [ "$postgres_ready" != true ]; then
    docker logs "$postgres_container" >&2
    echo "Development PostgreSQL did not become ready" >&2
    exit 1
  fi
  postgres_port=${postgres_endpoint##*:}
  export MEMENTO_DATABASE_URL="postgresql://memento_app:test-only-password@127.0.0.1:$postgres_port/memento?sslmode=disable"
fi

if [ "$managed_immich" = true ]; then
  docker run --detach \
    --name "$immich_container" \
    --label "com.memento.dev-instance=$instance" \
    --publish 127.0.0.1::3001 \
    --volume "$root/deploy/test-immich.Caddyfile:/etc/caddy/Caddyfile:ro" \
    caddy:2.10.2-alpine \
    caddy run --config /etc/caddy/Caddyfile --adapter caddyfile >/dev/null

  immich_ready=false
  immich_endpoint=
  for _ in $(seq 1 60); do
    immich_endpoint=$(docker port "$immich_container" 3001/tcp 2>/dev/null | head -n 1 || true)
    if [ -n "$immich_endpoint" ] && curl --silent --fail \
      "http://$immich_endpoint/api/server/version" >/dev/null 2>&1; then
      immich_ready=true
      break
    fi
    sleep 0.5
  done
  if [ "$immich_ready" != true ]; then
    docker logs "$immich_container" >&2
    echo "Development Immich stub did not become ready" >&2
    exit 1
  fi
  export MEMENTO_IMMICH_URL="http://$immich_endpoint"
  if [ -z "${MEMENTO_IMMICH_API_KEY:-}" ] && [ -z "${MEMENTO_IMMICH_API_KEY_FILE:-}" ]; then
    export MEMENTO_IMMICH_API_KEY=test-only-key
  fi
fi

if [ -z "${MEMENTO_CONFIG_FILE:-}" ] && [ -z "${MEMENTO_HTTP_ADDRESS:-}" ]; then
  reserve_port "${MEMENTO_WEB_PORT:-}"
  export MEMENTO_HTTP_ADDRESS="127.0.0.1:$reserved_port"
  api_port_lock=$reserved_lock
fi
if [ -z "${MEMENTO_WEB_PORT:-}" ]; then
  reserve_port "${MEMENTO_HTTP_ADDRESS##*:}"
  export MEMENTO_WEB_PORT=$reserved_port
  web_port_lock=$reserved_lock
fi
if [ -z "${MEMENTO_API_PROXY_TARGET:-}" ] && [ -n "${MEMENTO_HTTP_ADDRESS:-}" ]; then
  case "$MEMENTO_HTTP_ADDRESS" in
    :*) export MEMENTO_API_PROXY_TARGET="http://127.0.0.1$MEMENTO_HTTP_ADDRESS" ;;
    0.0.0.0:*) export MEMENTO_API_PROXY_TARGET="http://127.0.0.1:${MEMENTO_HTTP_ADDRESS##*:}" ;;
    *) export MEMENTO_API_PROXY_TARGET="http://$MEMENTO_HTTP_ADDRESS" ;;
  esac
fi

printf 'Memento API: %s\n' "${MEMENTO_API_PROXY_TARGET:-http://127.0.0.1:8081}"
printf 'Memento web: http://127.0.0.1:%s\n' "$MEMENTO_WEB_PORT"
if [ "$managed_postgres" = true ] || [ "$managed_immich" = true ]; then
  echo "Using disposable development services managed by mise start"
fi

node ./scripts/run-development-servers.mjs &
servers_pid=$!
set +e
wait "$servers_pid"
status=$?
set -e
servers_pid=
exit "$status"
