#!/bin/sh
# Bring services up.
#
# Usage:
#   scripts/up.sh           # = scripts/up.sh go        — Go web/worker only (default)
#   scripts/up.sh go        # Go web (:8090) + Go worker, Python untouched
#   scripts/up.sh all       # Python bot/web/worker + Go web/worker (everything)
#   scripts/up.sh py        # Python only
#   scripts/up.sh go --build       # force rebuild before starting
#   scripts/up.sh all --build      # rebuild everything

set -eu

cd "$(dirname "$0")/.."
. ./scripts/_lib.sh

require_docker

TARGET="${1:-go}"
[ $# -gt 0 ] && shift

BUILD_FLAG=""
for arg in "$@"; do
  if [ "$arg" = "--build" ]; then
    BUILD_FLAG="--build"
  fi
done

COMPOSE_ARGS="$(resolve_compose_args "$TARGET")"
SERVICES="$(resolve_services "$TARGET")"

echo "▶ starting (target=$TARGET)"
echo "  services: $SERVICES"
[ -n "$BUILD_FLAG" ] && echo "  --build (force rebuild)"

# shellcheck disable=SC2086
docker compose $COMPOSE_ARGS up -d $BUILD_FLAG $SERVICES

echo ""
echo "✅ up"
case "$TARGET" in
  go)
    echo "  Go web:    http://localhost:8090   (Python web on :8080 untouched)"
    echo "  Go worker: health :8083"
    ;;
  all)
    echo "  Python web: http://localhost:8080"
    echo "  Go web:     http://localhost:8090"
    ;;
esac
echo ""
echo "  logs:    scripts/logs.sh $TARGET"
echo "  status:  scripts/status.sh"
echo "  down:    scripts/down.sh $TARGET"
