#!/bin/sh
set -u

memento &
app_pid=$!
caddy run --config /etc/caddy/Caddyfile --adapter caddyfile &
caddy_pid=$!
stopping=0

shutdown() {
  stopping=1
  kill -TERM "$app_pid" "$caddy_pid" 2>/dev/null || true
}
trap shutdown INT TERM

while kill -0 "$app_pid" 2>/dev/null && kill -0 "$caddy_pid" 2>/dev/null; do
  sleep 1 &
  wait $! || true
done

if [ "$stopping" -eq 0 ]; then
  kill -TERM "$app_pid" "$caddy_pid" 2>/dev/null || true
fi

wait "$app_pid"
app_status=$?
wait "$caddy_pid"
caddy_status=$?

if [ "$stopping" -eq 1 ] && [ "$app_status" -eq 0 ]; then
  exit 0
fi
if [ "$app_status" -ne 0 ]; then
  exit "$app_status"
fi
exit "$caddy_status"
