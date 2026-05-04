#!/bin/sh
# Common helpers for scripts/*.sh — POSIX sh compatible (dash, ash,
# bash sh-mode, zsh sh-mode, busybox sh).
#
# This file is meant to be sourced via `. scripts/_lib.sh` from the
# repo root. It does not cd by itself — each entry script handles
# the chdir to repo root, because `$BASH_SOURCE` is bashism and
# POSIX sh has no portable way to find the sourced file's path.

set -eu

require_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    echo "❌ docker not found in PATH" >&2
    exit 1
  fi
}

# resolve_compose_args echoes the `-f ...` flags only.
#   target=go|all  → both compose files
#   target=py      → base only
resolve_compose_args() {
  target="${1:-go}"
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

# resolve_services echoes the space-separated service list for the target.
resolve_services() {
  target="${1:-go}"
  case "$target" in
    go)  echo "web-go worker-go" ;;
    all) echo "bot web worker web-go worker-go" ;;
    py|python) echo "bot web worker" ;;
    *)
      echo "❌ unknown target: $target (expected: go | all | py)" >&2
      exit 2
      ;;
  esac
}
