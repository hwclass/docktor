# Docktor Autoscaling Guide

Docktor provides **two autoscaling modes**:

## 🤖 AI-Powered Mode (Recommended for Demo)
Uses **cagent + Llama 3.2 + MCP** for intelligent, dynamic scaling decisions.

**Benefits:**
- AI-native decision making
- Dynamic replica calculations (not hardcoded)
- Full MCP explainability
- Completely local (no API keys)

## ⚡ Direct Mode (Fast & Reliable)
Pure bash script calling MCP tools directly.

**Benefits:**
- Fast (no LLM inference)
- Deterministic behavior
- Simple debugging

---

# Testing AI-Powered Autoscaling

## Prerequisites

1. **Docker Model Runner must be running** (comes with Docker Desktop)
2. **Llama 3.2 model downloaded** (happens automatically on first run)

## Step-by-Step Testing Guide

### Step 1: Start the Docker Stack
```bash
# Start with 2 web replicas
docker compose -f examples/docker-compose.yaml up -d --scale web=2
```

### Step 2: Start the AI Autoscaler Daemon
```bash
# Start the AI-powered autoscaler
bash scripts/daemon.sh start
```

You should see:
```
✓ Daemon started successfully
  PID: 12345
  Logs: tail -f /tmp/docktor-daemon.log
```

### Step 3: Monitor the Agent (in a new terminal)
```bash
# Watch the agent make decisions in real-time
tail -f /tmp/docktor-daemon.log

# Or watch just the important parts
tail -f /tmp/docktor-daemon.log | grep -E "Calling|response|CPU"
```

### Step 4: Watch Container Scaling (in another terminal)
```bash
# Watch containers being added/removed
watch -n 2 'docker compose -f examples/docker-compose.yaml ps'
```

### Step 5: Generate CPU Load
```bash
# Start CPU stress test (runs for 40 seconds)
bash scripts/load-cpu.sh
```

This will:
- Generate ~80% CPU load on all web containers
- Trigger the agent to detect high CPU
- Agent will calculate how many replicas needed
- Agent will scale up (typically from 2 → 4 or 5)

### Step 6: Watch Automatic Scale-Down
After the load test completes:
- Agent detects low CPU (~0%)
- Agent calculates optimal replica count
- Agent scales back down (typically to 1 or 2)

### Step 7: Stop the Daemon
```bash
bash scripts/daemon.sh stop
```

## What You'll See in the Logs

**When Idle (Low CPU):**
```
Calling get_metrics(container_regex: "web", window_sec: 30)
→ metrics: {"examples-web-1": 0.5, "examples-web-2": 0.3}

Calling detect_anomalies(...)
→ recommendation: "scale_down", reason: "avg_cpu 0.4% <= 20%"

Calling apply_scale(service: "web", target_replicas: 1, reason: "CPU low, scaling down")
→ SUCCESS: scaled web to 1
```

**When Under Load (High CPU):**
```
Calling get_metrics(container_regex: "web", window_sec: 30)
→ metrics: {"examples-web-1": 87.2, "examples-web-2": 91.5}

Calling detect_anomalies(...)
→ recommendation: "scale_up", reason: "avg_cpu 89.3% >= 80%"

Calling apply_scale(service: "web", target_replicas: 4, reason: "CPU high, scaling up")
→ SUCCESS: scaled web to 4
```

## Understanding the Agent's Decisions

The agent follows this logic:
- **Reads current state**: Counts replicas from metrics
- **Calculates average CPU**: Sums all values / count
- **Decides action**:
  - If avg > 80%: `target = current + 2` (scale up)
  - If avg < 20%: `target = max(1, current - 1)` (scale down)
  - If 20-80%: No change needed
- **Executes**: Calls `apply_scale` with calculated target

This means the agent **adapts to any starting state** - no hardcoded "2 or 5"!

## Useful Commands

```bash
# Check daemon status
bash scripts/daemon.sh status

# View logs in real-time
bash scripts/daemon.sh logs

# Restart daemon (applies config changes)
bash scripts/daemon.sh restart

# Check current replica count
docker compose -f examples/docker-compose.yaml ps | grep web

# Check real-time CPU usage
docker stats --no-stream | grep web

# View MCP debug logs
tail -f /tmp/docktor-mcp-debug.log

# See just the tool calls
grep "tools/call" /tmp/docktor-mcp-debug.log

# See scaling actions
grep "apply_scale" /tmp/docktor-daemon.log
```

## Troubleshooting

**Daemon exits after one iteration:**
- This is expected behavior with cagent's current architecture
- The agent completes one full cycle successfully but doesn't loop indefinitely
- Restart with `bash scripts/daemon.sh start` to run another iteration
- For continuous operation, use Direct Mode: `bash scripts/direct.sh`

**Model not found error:**
```bash
# Pull Llama 3.2 manually
docker model pull ai/llama3.2

# Check available models
docker model ls
```

**No scaling happening:**
- Check if containers are actually under load: `docker stats`
- Increase load duration in `scripts/load-cpu.sh`
- Check MCP logs for errors: `tail /tmp/docktor-mcp-debug.log`

---

# Alternative: Direct Mode (No LLM)

For **production** or **continuous operation**, use Direct Mode:

```bash
# Run autoscaling loop directly (no LLM, faster, more reliable)
bash scripts/direct.sh
```

This uses a bash loop that calls MCP tools directly without involving the LLM for decision-making. Decisions are coded in bash but still use MCP for explainability.

## Key Files

- `scripts/daemon.sh` - AI daemon launcher (uses cagent + Llama 3.2)
- `agents/docktor-daemon.yaml` - Agent configuration
- `cmd/docktor/main.go` - MCP server with 4 tools
- `scripts/load-cpu.sh` - CPU stress test
- `scripts/mcp-debug.sh` - MCP logging wrapper

## Architecture

```
┌─────────────┐      ┌──────────────┐      ┌─────────────┐
│   cagent    │─────▶│ Llama 3.2 3B │      │   Docker    │
│  (daemon)   │      │   (local)    │      │   Compose   │
└──────┬──────┘      └──────────────┘      └──────▲──────┘
       │                                           │
       │ MCP (JSON-RPC)                           │
       │                                           │
       ▼                                           │
┌─────────────────────────────────────────────────┴──────┐
│              Docktor MCP Server                        │
│  - get_metrics                                         │
│  - detect_anomalies                                    │
│  - propose_scale                                       │
│  - apply_scale                                         │
└────────────────────────────────────────────────────────┘
```

All decisions are logged via MCP for full observability!
