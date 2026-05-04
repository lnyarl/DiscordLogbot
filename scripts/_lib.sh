#!/usr/bin/env bash
# 공통 헬퍼 — scripts/*.sh 가 source 한다.

set -euo pipefail

# 항상 repo 루트에서 docker compose 실행 (어디서 호출하든 동일하게 동작).
cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Compose 인자 빌더.
#   target=go     → docker-compose.yml + docker-compose.go.yml, 대상 = web-go worker-go
#   target=all    → 두 compose 파일 + 대상 = bot web worker web-go worker-go
#   target=py     → docker-compose.yml 만, 대상 = bot web worker
#
# echo 로 두 개의 라인을 반환:
#   1줄: docker compose 인자 (-f ... -f ...)
#   2줄: 대상 서비스 목록
resolve_target() {
  local target="${1:-go}"
  case "$target" in
    go)
      echo "-f docker-compose.yml -f docker-compose.go.yml"
      echo "web-go worker-go"
      ;;
    all)
      echo "-f docker-compose.yml -f docker-compose.go.yml"
      echo "bot web worker web-go worker-go"
      ;;
    py|python)
      echo "-f docker-compose.yml"
      echo "bot web worker"
      ;;
    *)
      echo "❌ unknown target: $target (expected: go | all | py)" >&2
      exit 2
      ;;
  esac
}

# Compose 인자만 빼서 반환 (logs/status 가 사용).
resolve_compose_args() {
  resolve_target "$1" | head -1
}

require_docker() {
  if ! command -v docker >/dev/null; then
    echo "❌ docker not found in PATH" >&2
    exit 1
  fi
}
