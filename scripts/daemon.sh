#!/usr/bin/env bash
set -euo pipefail

# Launch Docktor as a true daemon using cagent with LLM-based decision making
# This is the production mode for AI-native autoscaling

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT/examples/docker-compose.yaml"
ENV_FILE="$ROOT/.env.cagent"
LOG_FILE="${DOCKTOR_LOG_FILE:-/tmp/docktor-daemon.log}"
PID_FILE="/tmp/docktor-daemon.pid"

# Parse flags
MANUAL_MODE=false
for arg in "$@"; do
  case $arg in
    --manual)
      MANUAL_MODE=true
      shift
      ;;
  esac
done

export DOCKTOR_COMPOSE_FILE="$COMPOSE_FILE"

# Build docktor binary
echo "Building docktor binary..."
(cd "$ROOT" && go build -o docktor ./cmd/docktor)

# Use the proper agent file with absolute paths (cagent changes to agent dir)
AGENT_FILE="$ROOT/agents/docktor.dmr.yaml"

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

    if [[ "$MANUAL_MODE" == "true" ]]; then
      echo "Starting AI agent in MANUAL mode (requires user approval for actions)..."
      echo "The agent will wait for your input in the TUI."
      echo

      # Launch cagent in manual/interactive mode
      echo "Start monitoring and autoscaling the 'web' service. Check metrics and scale when needed." | \
      nohup cagent run "$AGENT_FILE" \
        --agent docktor \
        > "$LOG_FILE" 2>&1 &
    else
      echo "Starting AI agent in AUTONOMOUS mode (auto-approves all actions)..."
      echo "The agent will run continuously in the background."
      echo

      # Launch cagent in autonomous mode with:
      # --tui=false: Disable Terminal UI (headless mode)
      # --yolo: Auto-approve all tool calls (autonomous mode)
      # Continuous loop: Send prompt every 10 seconds to keep agent running
      # Keep prompts short to avoid context overflow
      (
        while true; do
          echo "Check and scale"
          sleep 10
        done
      ) | nohup cagent run "$AGENT_FILE" \
        --agent docktor \
        --tui=false \
        --yolo \
        > "$LOG_FILE" 2>&1 &
    fi

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
Usage: $0 {start|stop|restart|status|logs} [--manual]

Commands:
  start    Start the AI autoscaling daemon
  stop     Stop the daemon
  restart  Restart the daemon
  status   Show daemon status and recent activity
  logs     Follow daemon logs in real-time

Flags:
  --manual  Run in manual mode (requires user approval for each action)
            Default: autonomous mode (auto-approves all actions)

Environment:
  DOCKTOR_LOG_FILE  Path to log file (default: /tmp/docktor-daemon.log)

Examples:
  $0 start                    # Start in autonomous mode (default)
  $0 start --manual           # Start in manual mode
  $0 logs                     # Watch logs
  bash scripts/load-cpu.sh    # Generate load
  $0 stop                     # Stop daemon

Modes:
  Autonomous (default): Agent monitors and scales automatically every 10s
  Manual (--manual):    Agent waits for user approval before each action
EOF
    exit 1
    ;;
esac
