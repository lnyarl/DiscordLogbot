#!/bin/sh
# Stop services.
#
# Usage:
#   scripts/down.sh         # = scripts/down.sh go    — stop Go side only (default, safe)
#   scripts/down.sh go
#   scripts/down.sh all     # stop everything (Python prod bot too — careful)
#   scripts/down.sh py      # Python only
#   scripts/down.sh <T> --rm   # remove containers (images/volumes preserved)

set -eu

cd "$(dirname "$0")/.."
. ./scripts/_lib.sh

require_docker

TARGET="${1:-go}"
[ $# -gt 0 ] && shift

REMOVE=0
for arg in "$@"; do
  if [ "$arg" = "--rm" ]; then
    REMOVE=1
  fi
done

COMPOSE_ARGS="$(resolve_compose_args "$TARGET")"
SERVICES="$(resolve_services "$TARGET")"

if [ "$REMOVE" -eq 1 ]; then
  echo "▶ stop + remove containers (target=$TARGET)"
  # shellcheck disable=SC2086
  docker compose $COMPOSE_ARGS rm -sf $SERVICES
else
  echo "▶ stop (target=$TARGET, containers preserved)"
  # shellcheck disable=SC2086
  docker compose $COMPOSE_ARGS stop $SERVICES
fi

echo "✅ stopped"
[ "$TARGET" = "go" ] && echo "  Python services unaffected."
