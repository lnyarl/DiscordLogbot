#!/bin/sh
# Follow logs.
#
# Usage:
#   scripts/logs.sh                   # = scripts/logs.sh go    — Go side combined
#   scripts/logs.sh go
#   scripts/logs.sh all               # Python+Go everything
#   scripts/logs.sh py                # Python only
#   scripts/logs.sh go web-go         # one service

set -eu

cd "$(dirname "$0")/.."
. ./scripts/_lib.sh

require_docker

TARGET="${1:-go}"
[ $# -gt 0 ] && shift

COMPOSE_ARGS="$(resolve_compose_args "$TARGET")"
DEFAULT_SERVICES="$(resolve_services "$TARGET")"

if [ $# -gt 0 ]; then
  SERVICES="$*"
else
  SERVICES="$DEFAULT_SERVICES"
fi

# shellcheck disable=SC2086
docker compose $COMPOSE_ARGS logs -f --tail=200 $SERVICES
