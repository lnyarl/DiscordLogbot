#!/usr/bin/env bash
# 코드 변경 후 이미지 재빌드 + 무중단 재기동.
#
# Usage:
#   scripts/rebuild.sh        # = scripts/rebuild.sh go
#   scripts/rebuild.sh go     # Go web/worker 만 재빌드+재기동
#   scripts/rebuild.sh all    # Python+Go 전체 재빌드+재기동
#   scripts/rebuild.sh py     # Python 만

source "$(dirname "$0")/_lib.sh"

require_docker

TARGET="${1:-go}"

mapfile -t LINES < <(resolve_target "$TARGET")
COMPOSE_ARGS="${LINES[0]}"
SERVICES="${LINES[1]}"

echo "▶ 이미지 재빌드 (target=$TARGET)"
docker compose $COMPOSE_ARGS build $SERVICES

echo ""
echo "▶ 재기동 (--force-recreate 로 새 이미지 적용)"
docker compose $COMPOSE_ARGS up -d --force-recreate $SERVICES

echo ""
echo "✅ 재기동 완료"
docker compose $COMPOSE_ARGS ps $SERVICES
