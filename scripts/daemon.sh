#!/usr/bin/env bash
set -euo pipefail

# Launch Docktor as a true daemon using cagent with LLM-based decision making
# This is the production mode for AI-native autoscaling

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT/examples/docker-compose.yaml"
ENV_FILE="$ROOT/.env.cagent"
AGENT_FILE="$ROOT/agents/docktor-daemon.yaml"
LOG_FILE="${DOCKTOR_LOG_FILE:-/tmp/docktor-daemon.log}"
PID_FILE="/tmp/docktor-daemon.pid"

# No need for .env.cagent when using DMR provider!
# The agent config uses "model: dmr/ai/smollm2" which points directly to Docker Model Runner

export DOCKTOR_COMPOSE_FILE="$COMPOSE_FILE"

# Build docktor binary
echo "Building docktor binary..."
(cd "$ROOT" && go build -o docktor ./cmd/docktor)

# Create agent config with absolute paths (cagent changes to agent dir)
TEMP_AGENT="/tmp/docktor-daemon-abs.yaml"
cat > "$TEMP_AGENT" <<EOF
version: "2"
agents:
  docktor:
    model: dmr/ai/llama3.2
    description: "Autonomous SRE agent"
    instruction: |
      You are an autonomous SRE agent running in CONTINUOUS DAEMON MODE.

      Your job is to run an infinite monitoring loop. NEVER stop. NEVER exit.
      After each iteration, IMMEDIATELY start the next one.

      INFINITE LOOP - Execute these steps repeatedly without stopping:

      STEP 1: Call get_metrics(container_regex="web", window_sec=30)

      STEP 2: Analyze the metrics you received. Calculate:
        - Current replica count (number of containers in metrics)
        - Average CPU% across all containers

      STEP 3: Decide on scaling action based on average CPU:

        - If avg_cpu > 80%:
          target_replicas = current_replicas + 2
          Call apply_scale(service="web", target_replicas=target_replicas, reason="CPU high at X%, scaling up")

        - If avg_cpu < 20%:
          target_replicas = max(1, current_replicas - 1)
          Call apply_scale(service="web", target_replicas=target_replicas, reason="CPU low at X%, scaling down")

        - If 20% ≤ avg_cpu ≤ 80%:
          Print "CPU healthy at X%, no scaling needed"

      STEP 4: Print "Iteration N complete" then GOTO STEP 1

      REPEAT STEPS 1-4 FOREVER. Never stop. Never exit.

      CRITICAL RULES:
      - NEVER stop or exit
      - NO waiting between iterations (check continuously)
      - If you complete one iteration, IMMEDIATELY start the next
      - Keep running until killed

      Start the infinite loop NOW!
    toolsets:
      - type: mcp
        command: $ROOT/scripts/mcp-debug.sh
        args: []
EOF
AGENT_FILE="$TEMP_AGENT"

case "${1:-start}" in
  start)
    if [[ -f "$PID_FILE" ]] && kill -0 $(cat "$PID_FILE") 2>/dev/null; then
      echo "ERROR: Docktor daemon already running (PID $(cat "$PID_FILE"))"
      echo "Stop it first with: $0 stop"
      exit 1
    fi

    echo "=== Starting Docktor Daemon ==="
    echo "Agent: $AGENT_FILE"
    echo "Compose: $COMPOSE_FILE"
    echo "Log: $LOG_FILE"
    echo

    # Start the stack if not running
    echo "Ensuring Docker Compose stack is running..."
    docker compose -f "$COMPOSE_FILE" up -d --scale web=2

    echo "Starting autonomous AI agent (cagent + Docker Model Runner)..."
    echo "The agent will run continuously in the background."
    echo

    # Launch cagent in daemon mode with:
    # --tui=false: Disable Terminal UI (headless mode)
    # --yolo: Auto-approve all tool calls (autonomous mode)
    # model: dmr/ai/smollm2 uses Docker Model Runner (no API keys needed!)
    # Pipe initial message to trigger the agent immediately
    echo "Start the autonomous autoscaling loop now. Monitor the 'web' service continuously." | \
    nohup cagent run "$AGENT_FILE" \
      --agent docktor \
      --tui=false \
      --yolo \
      > "$LOG_FILE" 2>&1 &

    DAEMON_PID=$!
    echo $DAEMON_PID > "$PID_FILE"

    # Wait a bit to ensure it started
    sleep 3

    if kill -0 $DAEMON_PID 2>/dev/null; then
      echo "✓ Daemon started successfully"
      echo "  PID: $DAEMON_PID"
      echo "  Logs: tail -f $LOG_FILE"
      echo
      echo "Monitor with:"
      echo "  $0 status    # Check daemon status"
      echo "  $0 logs      # Follow logs"
      echo "  $0 stop      # Stop daemon"
    else
      echo "✗ Daemon failed to start. Check logs:"
      tail -20 "$LOG_FILE"
      rm -f "$PID_FILE"
      exit 1
    fi
    ;;

  stop)
    if [[ ! -f "$PID_FILE" ]]; then
      echo "No daemon PID file found"
      exit 1
    fi

    PID=$(cat "$PID_FILE")
    echo "Stopping Docktor daemon (PID $PID)..."

    if kill -0 $PID 2>/dev/null; then
      kill $PID
      sleep 2

      if kill -0 $PID 2>/dev/null; then
        echo "Daemon didn't stop gracefully, force killing..."
        kill -9 $PID
      fi

      echo "✓ Daemon stopped"
    else
      echo "Daemon not running"
    fi

    rm -f "$PID_FILE"
    ;;

  restart)
    $0 stop
    sleep 2
    $0 start
    ;;

  status)
    if [[ ! -f "$PID_FILE" ]]; then
      echo "Status: NOT RUNNING (no PID file)"
      exit 1
    fi

    PID=$(cat "$PID_FILE")
    if kill -0 $PID 2>/dev/null; then
      echo "Status: RUNNING"
      echo "  PID: $PID"
      echo "  Uptime: $(ps -p $PID -o etime= | tr -d ' ')"
      echo "  Log: $LOG_FILE"
      echo
      echo "Recent activity:"
      tail -10 "$LOG_FILE" | grep -E 'iteration|action|SCALED|MCP' || echo "  (no recent activity)"
    else
      echo "Status: DEAD (PID $PID not found)"
      rm -f "$PID_FILE"
      exit 1
    fi
    ;;

  logs)
    if [[ ! -f "$LOG_FILE" ]]; then
      echo "No log file found at $LOG_FILE"
      exit 1
    fi

    tail -f "$LOG_FILE"
    ;;

  *)
    cat <<EOF
Usage: $0 {start|stop|restart|status|logs}

Commands:
  start    Start the autonomous AI autoscaling daemon
  stop     Stop the daemon
  restart  Restart the daemon
  status   Show daemon status and recent activity
  logs     Follow daemon logs in real-time

Environment:
  DOCKTOR_LOG_FILE  Path to log file (default: /tmp/docktor-daemon.log)

Example:
  $0 start              # Start daemon
  $0 logs               # Watch logs
  bash scripts/load-cpu.sh  # Generate load
  $0 stop               # Stop daemon
EOF
    exit 1
    ;;
esac
