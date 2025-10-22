#!/usr/bin/env bash
set -euo pipefail

# Generate actual CPU load by running stress inside the web containers
# This will make the containers consume real CPU so autoscaling triggers

echo "Generating CPU load on web containers for 40 seconds..."
echo "This will trigger autoscaling from 2 → 5 replicas"
echo

# Get all web container IDs
WEB_CONTAINERS=$(docker ps --filter "name=examples-web" --format "{{.ID}}")

if [ -z "$WEB_CONTAINERS" ]; then
  echo "ERROR: No web containers found. Start them first with:"
  echo "  docker compose -f examples/docker-compose.yaml up -d --scale web=2"
  exit 1
fi

echo "Found web containers:"
echo "$WEB_CONTAINERS" | while read -r cid; do
  docker ps --filter "id=$cid" --format "  - {{.Names}} ({{.ID}})"
done
echo

# Start stress in each container (in background)
echo "Starting CPU stress (80% load per container)..."
echo "$WEB_CONTAINERS" | while read -r cid; do
  # Install and run stress-ng to consume CPU
  docker exec -d "$cid" sh -c 'apk add --no-cache stress-ng >/dev/null 2>&1 && stress-ng --cpu 1 --cpu-load 80 --timeout 40s' 2>/dev/null || \
  docker exec -d "$cid" sh -c 'yes > /dev/null' &  # Fallback: simple CPU burner
done

echo "✓ CPU stress started (will run for 40 seconds)"
echo "Watch in another terminal: bash scripts/watch.sh"
echo "Or check logs: tail -f /tmp/docktor-autoscale.log"
echo

# Wait for stress to complete
for i in {1..40}; do
  echo -n "."
  sleep 1
done

echo
echo "✓ CPU stress completed"
echo "Containers will cool down and autoscaler should scale back down to 2"
