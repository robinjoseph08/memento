#!/bin/sh
set -eu

if [ "$(uname -s)" = Linux ]; then
  pnpm exec playwright install --with-deps chromium firefox
else
  pnpm exec playwright install chromium firefox
fi
