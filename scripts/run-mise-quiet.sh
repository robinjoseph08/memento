#!/bin/sh
set -eu

task=${1:-}
if [ -z "$task" ]; then
  echo "usage: run-mise-quiet.sh <task>" >&2
  exit 2
fi

mise_bin=${MISE_BIN:-mise}
temporary=$(mktemp -d)
output=$temporary/output
child_pid=
cleanup() {
  rm -rf "$temporary"
}
forward_signal() {
  signal=$1
  status=$2
  if [ -n "$child_pid" ] && kill -0 "$child_pid" 2>/dev/null; then
    kill -"$signal" "$child_pid" 2>/dev/null || true
    wait "$child_pid" 2>/dev/null || true
  fi
  child_pid=
  exit "$status"
}
trap cleanup EXIT
# POSIX shells may start asynchronous children with SIGINT ignored. Translate
# an interrupt to TERM for the Mise child, while preserving exit 130 here.
trap 'forward_signal TERM 130' INT
trap 'forward_signal TERM 143' TERM

printf 'Running %s...\n' "$task"
"$mise_bin" run "$task" >"$output" 2>&1 &
child_pid=$!
status=0
if wait "$child_pid"; then
  child_pid=
  printf '%s:quiet PASSED\n' "$task"
  exit 0
else
  status=$?
  child_pid=
fi

printf '\nFAILED TASKS:\n'
failed=$(sed -n 's/^\[\([^]]*\)\] ERROR task failed$/\1/p' "$output" | sort -u)
if [ -n "$failed" ]; then
  printf '%s\n' "$failed" | sed 's/^/  /'
else
  printf '  %s (see captured output)\n' "$task"
fi

printf '\nCAPTURED OUTPUT:\n'
cat "$output"
printf '\n%s:quiet FAILED\n' "$task"
exit "$status"
