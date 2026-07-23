#!/bin/sh
set -eu

files=$(gofmt -l ./cmd ./internal ./pkg)
if [ -n "$files" ]; then
  printf 'Go files need formatting:\n%s\n' "$files" >&2
  exit 1
fi
