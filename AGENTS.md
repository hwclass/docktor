# Agent Instructions for Docktor

This file follows the [agents.md](https://agents.md/) specification.

## Project Overview

Docktor is an AI-native autoscaling system for Docker Compose services. It uses local LLMs to make intelligent scaling decisions based on real-time metrics.

## Core Agent: SRE Autoscaler

**Role**: Autonomous Site Reliability Engineer
**Responsibility**: Monitor and scale Docker Compose services to maintain optimal performance

### Configuration

Agent behavior is configured via `docktor.yaml`. Key parameters:

- **service**: Name of the Docker Compose service to monitor
- **scaling.cpu_high**: CPU threshold (%) to trigger scale-up
- **scaling.cpu_low**: CPU threshold (%) to trigger scale-down
- **scaling.min_replicas**: Minimum replicas (high availability)
- **scaling.max_replicas**: Maximum replicas (cost/capacity limit)
- **scaling.check_interval**: Seconds between scaling checks

### Available Tools

1. **get_metrics(container_regex, window_sec)**
   - Collects average CPU% over time window
   - Returns: `{container_name: cpu_percent}`

2. **detect_anomalies(metrics, rules)**
   - Analyzes metrics against thresholds
   - Returns: `recommendation` (scale_up/scale_down/hold) + `reason`

3. **propose_scale(service, target_replicas)**
   - Previews the scaling command
   - Returns: validation + docker compose command

4. **apply_scale(service, target_replicas, reason)**
   - Executes the scaling action
   - Returns: success/failure status

### Decision Loop

```
Every check_interval seconds:
1. get_metrics() → Collect current CPU metrics
2. detect_anomalies() → Compare against thresholds
3. If scale_up AND replicas < max:
     apply_scale(current + scale_up_by, reason="cpu_high")
4. If scale_down AND replicas > min:
     apply_scale(current - scale_down_by, reason="cpu_low")
5. If hold:
     Log status, no action
6. Wait for next interval
```

### Guidelines

- **Autonomous**: Run continuously without user approval
- **Conservative**: Only scale when metrics clearly justify it
- **Observability**: Log every decision with reasoning
- **Constraints**: Never violate min/max replica bounds
- **Stability**: Prioritize stability over aggressive scaling

### Example Configuration

```yaml
# docktor.yaml
service: web
scaling:
  cpu_high: 75.0
  cpu_low: 20.0
  min_replicas: 2
  max_replicas: 10
  scale_up_by: 2
  scale_down_by: 1
  check_interval: 10
```

### Example Decision

```
Metrics: web-1: 82%, web-2: 85% → avg: 83.5%
Threshold: cpu_high = 75%
Current: 2 replicas, Max: 10
Decision: Scale up by 2 → Target: 4 replicas
Action: apply_scale(service="web", target_replicas=4, reason="cpu_high: avg 83.5% >= 75%")
```

## For AI Agents

When working with Docktor:

1. **Read `docktor.yaml`** first to understand the configuration
2. **Use MCP tools** - never bypass them or write scaling code directly
3. **Follow constraints** - respect min/max replica limits
4. **Log decisions** - output structured JSON status updates
5. **Be conservative** - favor stability over aggressive optimization

## For Developers

- Configuration lives in `docktor.yaml` (not hardcoded)
- Agent logic is in `agents/docktor.dmr.yaml` or `agents/docktor.cloud.yaml`
- MCP server implementation: `cmd/docktor/main.go`
- Start daemon: `./docktor daemon start --config docktor.yaml`
