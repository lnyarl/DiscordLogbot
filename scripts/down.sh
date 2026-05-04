#!/usr/bin/env bash
# 서비스 정지.
#
# Usage:
#   scripts/down.sh         # = scripts/down.sh go    — Go 측만 정지 (기본, 안전)
#   scripts/down.sh go
#   scripts/down.sh all     # 전체 정지 (Python 운영봇도 내려감 — 주의)
#   scripts/down.sh py      # Python 측만
#   scripts/down.sh <T> --rm   # 컨테이너까지 삭제 (이미지/볼륨은 보존)

source "$(dirname "$0")/_lib.sh"

require_docker

TARGET="${1:-go}"
shift || true

REMOVE=false
for arg in "$@"; do
  if [[ "$arg" == "--rm" ]]; then
    REMOVE=true
  fi
done

COMPOSE_ARGS="$(resolve_compose_args "$TARGET")"
SERVICES="$(resolve_services "$TARGET")"

if $REMOVE; then
  echo "▶ 정지 + 컨테이너 삭제 (target=$TARGET)"
  docker compose $COMPOSE_ARGS rm -sf $SERVICES
else
  echo "▶ 정지 (target=$TARGET, 컨테이너 보존)"
  docker compose $COMPOSE_ARGS stop $SERVICES
fi

echo "✅ 정지 완료"
[[ "$TARGET" == "go" ]] && echo "  Python 측 서비스는 영향 없음."
