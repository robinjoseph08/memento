#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
log=$(mktemp)
instance="test-$(date +%s)-$$"
start_pid=
server_processes=

port_is_available() {
  node -e 'const net = require("node:net"); const server = net.createServer(); server.once("error", () => process.exit(1)); server.listen(Number(process.argv[1]), "127.0.0.1", () => server.close(() => process.exit(0)));' "$1"
}

descendants() {
  for child in $(pgrep -P "$1" 2>/dev/null || true); do
    echo "$child"
    descendants "$child"
  done
}

cleanup() {
  if [ -n "$start_pid" ] && kill -0 "$start_pid" 2>/dev/null; then
    kill "$start_pid" 2>/dev/null || true
    wait "$start_pid" 2>/dev/null || true
  fi
  for pid in $server_processes; do
    kill "$pid" 2>/dev/null || true
  done
  sleep 0.25
  for pid in $server_processes; do
    kill -KILL "$pid" 2>/dev/null || true
  done
  containers=$(docker ps --all --quiet --filter "label=com.memento.dev-instance=$instance" 2>/dev/null || true)
  if [ -n "$containers" ]; then
    docker rm --force $containers >/dev/null 2>&1 || true
  fi
  rm -f "$log"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

(
  unset MEMENTO_CONFIG_FILE
  unset MEMENTO_HTTP_ADDRESS MEMENTO_HTTP_SHUTDOWN_TIMEOUT
  unset MEMENTO_API_PROXY_TARGET MEMENTO_WEB_PORT
  unset MEMENTO_DATABASE_URL MEMENTO_DATABASE_URL_FILE MEMENTO_DATABASE_NAME
  unset MEMENTO_DATABASE_MAX_OPEN_CONNS MEMENTO_DATABASE_HEALTH_TIMEOUT
  unset MEMENTO_IMMICH_URL MEMENTO_IMMICH_API_KEY MEMENTO_IMMICH_API_KEY_FILE
  unset MEMENTO_IMMICH_HEALTH_TIMEOUT
  unset MEMENTO_WORKER_POLL_INTERVAL MEMENTO_WORKER_HEARTBEAT_INTERVAL
  unset MEMENTO_WORKER_HEARTBEAT_MAX_AGE MEMENTO_WORKER_LEASE_DURATION
  unset MEMENTO_WORKER_DRAIN_TIMEOUT
  export MEMENTO_DEV_INSTANCE="$instance"
  cd "$root"
  exec mise start
) >"$log" 2>&1 &
start_pid=$!

ready=false
api_url=
web_url=
for _ in $(seq 1 180); do
  if grep -q "database.url is required" "$log"; then
    cat "$log" >&2
    echo "mise start did not supply development runtime configuration" >&2
    exit 1
  fi
  if ! kill -0 "$start_pid" 2>/dev/null; then
    cat "$log" >&2
    echo "mise start exited before the development servers became ready" >&2
    exit 1
  fi
  if [ -z "$api_url" ]; then
    api_url=$(awk '/^Memento API: / { print $3; exit }' "$log")
  fi
  if [ -z "$web_url" ]; then
    web_url=$(awk '/^Memento web: / { print $3; exit }' "$log")
  fi
  if [ -n "$api_url" ] && [ -n "$web_url" ] \
    && curl --silent --fail "$api_url/api/health/ready" >/dev/null 2>&1 \
    && curl --silent --fail "$web_url" >/dev/null 2>&1 \
    && curl --silent --fail "$web_url/api/health/ready" >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 0.5
done

if [ "$ready" != true ]; then
  cat "$log" >&2
  echo "mise start did not produce a usable development environment" >&2
  exit 1
fi

api_port=${api_url##*:}
web_port=${web_url##*:}
api_port_lock="${TMPDIR:-/tmp}/memento-dev-port-$api_port.lock"
web_port_lock="${TMPDIR:-/tmp}/memento-dev-port-$web_port.lock"
server_processes=$(descendants "$start_pid")
kill -INT "$start_pid"
wait "$start_pid" 2>/dev/null || true
start_pid=

for _ in $(seq 1 40); do
  processes_gone=true
  for pid in $server_processes; do
    if kill -0 "$pid" 2>/dev/null; then
      processes_gone=false
      break
    fi
  done
  containers=$(docker ps --all --quiet --filter "label=com.memento.dev-instance=$instance")
  if [ "$processes_gone" = true ] && [ -z "$containers" ] \
    && [ ! -d "$api_port_lock" ] && [ ! -d "$web_port_lock" ] \
    && port_is_available "$api_port" && port_is_available "$web_port"; then
    exit 0
  fi
  sleep 0.25
done

cat "$log" >&2
echo "mise start did not clean up all development processes, ports, and containers" >&2
exit 1
