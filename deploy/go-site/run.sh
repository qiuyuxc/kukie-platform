#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
: "${KUKIE_DATA_DIR:?Set KUKIE_DATA_DIR to a persistent directory outside the release}"
: "${KUKIE_SITE_URL:?Set KUKIE_SITE_URL to the same origin used when building}"
: "${KUKIE_CONSOLE_ORIGINS:?Set KUKIE_CONSOLE_ORIGINS to the HTTPS console origin}"
export KUKIE_REPOSITORY="$root"
export KUKIE_SITE_DIR="$root/site"
export KUKIE_SECURE_COOKIE="${KUKIE_SECURE_COOKIE:-1}"
exec "$root/bin/kukie-api"
