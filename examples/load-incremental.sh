#!/usr/bin/env bash
set -euo pipefail

# Incremental CPU load simulator for showcasing Docktor autoscaling
# Gradually increases load from 20% → 90% → back down to 20%
# Perfect for live demos to show dynamic scaling behavior

echo "🚀 Docktor Incremental Load Simulator"
echo "======================================"
echo
echo "This will gradually increase CPU load to trigger autoscaling:"
echo "  Phase 1: 20% CPU (2 replicas - stable)"
echo "  Phase 2: 50% CPU (2 replicas - approaching threshold)"
echo "  Phase 3: 80% CPU (scale up to 4+ replicas)"
echo "  Phase 4: 90% CPU (scale up to max replicas)"
echo "  Phase 5: Cool down (scale back down)"
echo
echo "Total duration: ~2 minutes"
echo

# Get all web container IDs
WEB_CONTAINERS=$(docker ps --filter "name=examples-web" --format "{{.ID}}")

if [ -z "$WEB_CONTAINERS" ]; then
  echo "❌ ERROR: No web containers found. Start Docktor first with:"
  echo "   ./docktor daemon start"
  exit 1
fi

echo "✓ Found web containers:"
echo "$WEB_CONTAINERS" | while read -r cid; do
  docker ps --filter "id=$cid" --format "  - {{.Names}} ({{.ID}})"
done
echo

# Function to apply load at a specific percentage
apply_load() {
  local load_pct=$1
  local duration=$2
  local description=$3

  echo "📊 $description"
  echo "   Load: ${load_pct}% | Duration: ${duration}s"

  # Kill any existing stress processes
  echo "$WEB_CONTAINERS" | while read -r cid; do
    docker exec "$cid" pkill -9 stress-ng 2>/dev/null || true
    docker exec "$cid" pkill -9 yes 2>/dev/null || true
  done

  # Apply new load level
  if [ "$load_pct" -gt 0 ]; then
    echo "$WEB_CONTAINERS" | while read -r cid; do
      docker exec -d "$cid" sh -c "stress-ng --cpu 1 --cpu-load $load_pct --timeout ${duration}s >/dev/null 2>&1" 2>/dev/null || \
      docker exec -d "$cid" sh -c "yes > /dev/null &" 2>/dev/null
    done
  fi

  # Show progress
  for ((i=1; i<=duration; i++)); do
    echo -n "."
    sleep 1
  done
  echo " ✓"
  echo
}

# Phase 1: Low load (baseline)
apply_load 20 15 "Phase 1: Baseline - 20% CPU (should stay at 2 replicas)"

# Phase 2: Moderate load (approaching threshold)
apply_load 50 15 "Phase 2: Moderate - 50% CPU (still holding at 2 replicas)"

# Phase 3: High load (trigger scale up)
apply_load 80 20 "Phase 3: High Load - 80% CPU (should scale up to 4+ replicas)"

# Phase 4: Peak load (scale to max)
apply_load 90 20 "Phase 4: Peak Load - 90% CPU (should scale to max replicas)"

# Phase 5: Cool down
echo "❄️  Phase 5: Cool Down - 0% CPU"
echo "   Containers cooling down (should scale back down to min)"
echo "$WEB_CONTAINERS" | while read -r cid; do
  docker exec "$cid" pkill -9 stress-ng 2>/dev/null || true
  docker exec "$cid" pkill -9 yes 2>/dev/null || true
done

for i in {1..30}; do
  echo -n "."
  sleep 1
done
echo " ✓"
echo

echo "🎉 Load simulation complete!"
echo
echo "Check Docktor's scaling actions with:"
echo "  ./docktor daemon logs"
echo "  docker compose -f examples/docker-compose.yaml ps web"
