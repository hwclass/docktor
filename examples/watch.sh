#!/usr/bin/env bash
set -euo pipefail
COMPOSE_FILE="${DOCKTOR_COMPOSE_FILE:-$(pwd)/examples/docker-compose.yaml}"

echo "Watching replicas + CPU. Ctrl-C to exit."
while true; do
  echo "----- $(date) -----"
  docker compose -f "$COMPOSE_FILE" ps
  echo
  docker stats --no-stream | (echo "CONTAINER CPU% MEM%"; cat) | awk 'NR<=2 || /web/'
  echo
  sleep 2
done