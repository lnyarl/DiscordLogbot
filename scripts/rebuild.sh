#!/bin/sh
# Rebuild images + recreate containers (no downtime gap larger than the
# recreate cycle itself).
#
# Usage:
#   scripts/rebuild.sh        # = scripts/rebuild.sh go
#   scripts/rebuild.sh go     # Go web/worker only
#   scripts/rebuild.sh all    # Python+Go everything
#   scripts/rebuild.sh py     # Python only

set -eu

cd "$(dirname "$0")/.."
. ./scripts/_lib.sh

require_docker

TARGET="${1:-go}"

COMPOSE_ARGS="$(resolve_compose_args "$TARGET")"
SERVICES="$(resolve_services "$TARGET")"

echo "▶ rebuild images (target=$TARGET)"
# shellcheck disable=SC2086
docker compose $COMPOSE_ARGS build $SERVICES

echo ""
echo "▶ recreate (--force-recreate)"
# shellcheck disable=SC2086
docker compose $COMPOSE_ARGS up -d --force-recreate $SERVICES

echo ""
echo "✅ rebuilt"
# shellcheck disable=SC2086
docker compose $COMPOSE_ARGS ps $SERVICES
