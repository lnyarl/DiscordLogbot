#!/usr/bin/env bash
# 서비스 기동.
#
# Usage:
#   scripts/up.sh           # = scripts/up.sh go        — Go web/worker만 (기본)
#   scripts/up.sh go        # Go web (:8090) + Go worker, Python 무영향
#   scripts/up.sh all       # Python 봇/웹/워커 + Go web/worker (전체)
#   scripts/up.sh py        # Python만
#   scripts/up.sh go --build       # 강제 재빌드 후 기동
#   scripts/up.sh all --build      # 전체 재빌드

source "$(dirname "$0")/_lib.sh"

require_docker

TARGET="${1:-go}"
shift || true   # 첫 인자 소비 (없어도 OK)

BUILD_FLAG=""
for arg in "$@"; do
  if [[ "$arg" == "--build" ]]; then
    BUILD_FLAG="--build"
  fi
done

COMPOSE_ARGS="$(resolve_compose_args "$TARGET")"
SERVICES="$(resolve_services "$TARGET")"

echo "▶ 기동 (target=$TARGET)"
echo "  서비스: $SERVICES"
[[ -n "$BUILD_FLAG" ]] && echo "  --build (강제 재빌드)"

docker compose $COMPOSE_ARGS up -d $BUILD_FLAG $SERVICES

echo ""
echo "✅ 기동 완료"
case "$TARGET" in
  go)
    echo "  Go web:    http://localhost:8090   (Python web 은 :8080 그대로)"
    echo "  Go worker: 헬스 :8083"
    ;;
  all)
    echo "  Python web: http://localhost:8080"
    echo "  Go web:     http://localhost:8090"
    ;;
esac
echo ""
echo "  로그:    scripts/logs.sh $TARGET"
echo "  상태:    scripts/status.sh"
echo "  정지:    scripts/down.sh $TARGET"
