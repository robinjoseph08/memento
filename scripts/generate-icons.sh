#!/bin/sh
set -eu

command -v rsvg-convert >/dev/null || {
  echo "rsvg-convert is required to generate icons" >&2
  exit 1
}
command -v magick >/dev/null || {
  echo "ImageMagick is required to package icons" >&2
  exit 1
}

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

rsvg-convert -w 1024 -h 1024 "$root/design/icon-master.svg" -o "$tmp/master.png"
rsvg-convert -w 512 -h 512 "$root/design/icon-mask.svg" -o "$root/public/icon-mask.png"
rsvg-convert -w 512 -h 512 --stylesheet "$root/design/mono.css" "$root/design/icon-mono.svg" -o "$root/public/icon-monochrome.png"

magick "$tmp/master.png" -resize 192x192 "$root/public/icon-192.png"
magick "$tmp/master.png" -resize 512x512 "$root/public/icon-512.png"
magick "$tmp/master.png" -background '#050a13' -alpha remove -alpha off -resize 180x180 "$root/public/apple-touch-icon.png"
magick "$tmp/master.png" -resize 32x32 "$tmp/favicon-32.png"
magick "$tmp/master.png" -resize 16x16 "$tmp/favicon-16.png"
magick "$tmp/favicon-32.png" "$tmp/favicon-16.png" "$root/public/favicon.ico"
