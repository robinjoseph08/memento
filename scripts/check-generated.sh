#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT INT TERM

cp -R "$root/app/types/generated" "$temporary/generated"
cd "$root"
go tool tygo generate

diff -ru "$temporary/generated" "$root/app/types/generated"
