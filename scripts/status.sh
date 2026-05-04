#!/usr/bin/env bash
# 양쪽 스택 상태 한눈 — Python + Go 컨테이너, 헬스 엔드포인트, 임베딩 큐 깊이.
#
# Usage:
#   scripts/status.sh

source "$(dirname "$0")/_lib.sh"

require_docker

COMPOSE_ARGS="$(resolve_compose_args all)"

echo "── 컨테이너 상태 ──────────────────────────────────────"
docker compose $COMPOSE_ARGS ps \
  --format 'table {{.Service}}\t{{.Status}}\t{{.Ports}}' \
  bot web worker bot-go web-go worker-go 2>/dev/null \
  || docker compose $COMPOSE_ARGS ps

echo ""
echo "── 헬스 엔드포인트 ────────────────────────────────────"
check() {
  local label="$1" url="$2"
  printf '  %-22s ' "$label"
  if curl -fsS --max-time 3 "$url" >/dev/null 2>&1; then
    echo "✅ OK"
  else
    echo "❌ unreachable"
  fi
}
check "Python web :8080"  "http://localhost:8080/"
check "Go web    :8090"   "http://localhost:8090/health"

echo ""
echo "── 임베딩 큐 깊이 (양쪽 워커가 같이 비워야 함) ─────────"
docker compose $COMPOSE_ARGS exec -T logbot-postgres \
  psql -U logbot -d logbot -At \
  -c "SELECT count(*) FROM messages WHERE embedding IS NULL AND action != 'delete';" \
  2>/dev/null \
  | head -1 \
  | awk '{print "  pending: " $1 " rows"}' \
  || echo "  (logbot-postgres 컨테이너에 접근 실패)"

echo ""
echo "── 최근 처리량 (최근 200 로그라인 기준 batch 횟수) ─────"
for svc in worker worker-go; do
  count=$(docker compose $COMPOSE_ARGS logs --tail=200 "$svc" 2>/dev/null \
    | grep -cE 'batch (complete|완료)' || echo 0)
  printf '  %-12s batches: ~%s\n' "$svc" "$count"
done
