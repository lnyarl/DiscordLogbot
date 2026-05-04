#!/usr/bin/env bash
# 로그 follow.
#
# Usage:
#   scripts/logs.sh                   # = scripts/logs.sh go    — Go 측 합쳐서 follow
#   scripts/logs.sh go
#   scripts/logs.sh all               # Python+Go 전체
#   scripts/logs.sh py                # Python 측만
#   scripts/logs.sh go web-go         # 한 서비스만

source "$(dirname "$0")/_lib.sh"

require_docker

TARGET="${1:-go}"
shift || true

mapfile -t LINES < <(resolve_target "$TARGET")
COMPOSE_ARGS="${LINES[0]}"
DEFAULT_SERVICES="${LINES[1]}"

# 추가 인자가 있으면 그 서비스만 follow.
if [[ $# -gt 0 ]]; then
  SERVICES="$*"
else
  SERVICES="$DEFAULT_SERVICES"
fi

docker compose $COMPOSE_ARGS logs -f --tail=200 $SERVICES
