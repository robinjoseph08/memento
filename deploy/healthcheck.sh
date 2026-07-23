#!/bin/sh
set -eu

address=${MEMENTO_HTTP_ADDRESS:-127.0.0.1:8081}
case "$address" in
  :*) address="127.0.0.1$address" ;;
  0.0.0.0:*) address="127.0.0.1:${address##*:}" ;;
esac

wget -q -O /dev/null "http://$address/api/health/live"
