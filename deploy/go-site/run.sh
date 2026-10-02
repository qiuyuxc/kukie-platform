#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
: "${KUKIE_DATA_DIR:?Set KUKIE_DATA_DIR to a persistent directory outside the release}"
export KUKIE_REPOSITORY="$root"
export KUKIE_SITE_DIR="$root/site"
export KUKIE_SECURE_COOKIE="${KUKIE_SECURE_COOKIE:-1}"
exec "$root/bin/kukie-api"
