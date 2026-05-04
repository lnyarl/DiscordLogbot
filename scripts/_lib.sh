#!/usr/bin/env bash
# 공통 헬퍼 — scripts/*.sh 가 source 한다.

set -euo pipefail

# 항상 repo 루트에서 docker compose 실행 (어디서 호출하든 동일하게 동작).
cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Compose 인자 빌더 — bash 3.2 호환을 위해 두 함수로 분리.
#   target=go     → docker-compose.yml + docker-compose.go.yml, 대상 = web-go worker-go
#   target=all    → 두 compose 파일 + 대상 = bot web worker web-go worker-go
#   target=py     → docker-compose.yml 만, 대상 = bot web worker

# resolve_compose_args echoes the `-f ...` flags only.
resolve_compose_args() {
  local target="${1:-go}"
  case "$target" in
    go|all)
      echo "-f docker-compose.yml -f docker-compose.go.yml"
      ;;
    py|python)
      echo "-f docker-compose.yml"
      ;;
    *)
      echo "❌ unknown target: $target (expected: go | all | py)" >&2
      exit 2
      ;;
  esac
}

# resolve_services echoes the space-separated service list.
resolve_services() {
  local target="${1:-go}"
  case "$target" in
    go)
      echo "web-go worker-go"
      ;;
    all)
      echo "bot web worker web-go worker-go"
      ;;
    py|python)
      echo "bot web worker"
      ;;
    *)
      echo "❌ unknown target: $target (expected: go | all | py)" >&2
      exit 2
      ;;
  esac
}

require_docker() {
  if ! command -v docker >/dev/null; then
    echo "❌ docker not found in PATH" >&2
    exit 1
  fi
}
