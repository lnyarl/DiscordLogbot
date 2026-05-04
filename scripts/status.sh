#!/bin/sh
# Both stacks at a glance — Python + Go containers, health endpoints,
# embedding queue depth.
#
# Usage:
#   scripts/status.sh

set -eu

cd "$(dirname "$0")/.."
. ./scripts/_lib.sh

require_docker

COMPOSE_ARGS="$(resolve_compose_args all)"

echo "── containers ──────────────────────────────────────────"
# shellcheck disable=SC2086
docker compose $COMPOSE_ARGS ps \
  --format 'table {{.Service}}\t{{.Status}}\t{{.Ports}}' \
  bot web worker bot-go web-go worker-go 2>/dev/null \
  || docker compose $COMPOSE_ARGS ps

echo ""
echo "── health endpoints ────────────────────────────────────"
check_url() {
  label="$1"
  url="$2"
  printf '  %-22s ' "$label"
  if curl -fsS --max-time 3 "$url" >/dev/null 2>&1; then
    echo "✅ OK"
  else
    echo "❌ unreachable"
  fi
}
check_url "Python web :8080"  "http://localhost:8080/"
check_url "Go web    :8090"   "http://localhost:8090/health"

echo ""
echo "── embedding queue depth (both workers should drain it) ─"
# shellcheck disable=SC2086
queue=$(docker compose $COMPOSE_ARGS exec -T logbot-postgres \
  psql -U logbot -d logbot -At \
  -c "SELECT count(*) FROM messages WHERE embedding IS NULL AND action != 'delete';" \
  2>/dev/null | head -1)
if [ -n "${queue:-}" ]; then
  echo "  pending: $queue rows"
else
  echo "  (logbot-postgres exec failed)"
fi

echo ""
echo "── recent throughput (last 200 log lines, batch count) ──"
for svc in worker worker-go; do
  # shellcheck disable=SC2086
  count=$(docker compose $COMPOSE_ARGS logs --tail=200 "$svc" 2>/dev/null \
    | grep -cE 'batch (complete|완료)' || true)
  printf '  %-12s batches: ~%s\n' "$svc" "${count:-0}"
done
