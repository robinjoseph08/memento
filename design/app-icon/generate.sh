#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
design="$root/design/app-icon"
public="$root/public"
build="${TMPDIR:-/tmp}/memento-app-icon-build"

mkdir -p "$build" "$public"

uvx --from cairosvg cairosvg "$design/memento-icon-dark.svg" -o "$build/tile-1024.png" --output-width 1024 --output-height 1024
uvx --from cairosvg cairosvg "$design/memento-icon-maskable.svg" -o "$build/maskable-1024.png" --output-width 1024 --output-height 1024
uvx --from cairosvg cairosvg "$design/memento-icon-mono.svg" -o "$build/mono-1024.png" --output-width 1024 --output-height 1024

magick "$build/tile-1024.png" -resize 32x32 "$build/favicon-32.png"
magick "$build/tile-1024.png" -resize 16x16 "$build/favicon-16.png"
magick "$build/favicon-32.png" "$build/favicon-16.png" "$public/favicon.ico"
magick "$build/tile-1024.png" -background '#070d13' -alpha remove -alpha off -resize 180x180 "$public/apple-touch-icon.png"
magick "$build/tile-1024.png" -resize 192x192 "$public/icon-192.png"
magick "$build/tile-1024.png" -resize 512x512 "$public/icon-512.png"
magick "$build/maskable-1024.png" -alpha remove -alpha off -resize 512x512 "$public/icon-mask.png"
magick "$build/mono-1024.png" -resize 512x512 "$public/icon-monochrome.png"

printf 'Generated Memento web icons in %s\n' "$public"
