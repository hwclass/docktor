# Single-Service CPU-Based Autoscaling

This example demonstrates basic CPU-based autoscaling for a single Docker Compose service.

## Overview

- **Service**: `web` (Nginx)
- **Scaling metric**: CPU usage
- **Scale up**: When CPU > 75%
- **Scale down**: When CPU < 20%
- **Replicas**: 2-10

## Quick Start

```bash
# From repo root
cd examples/single-service

# Start the service
docker compose up -d

# Start Docktor daemon
cd ../..
./docktor daemon start --config examples/single-service/docktor.yaml

# Generate CPU load (in another terminal)
bash examples/single-service/load-incremental.sh

# Watch scaling in action
./docktor daemon logs

# View scaling decisions
./docktor explain --tail 20

# Stop
./docktor daemon stop
cd examples/single-service
docker compose down
```

## Load Testing Scripts

### `load-incremental.sh`
Gradual load increase - recommended for demos:
- Starts with 5 requests/sec
- Increases by 5 req/sec every 10 seconds
- Maxes out at 50 req/sec
- Good for observing scaling behavior

### `load-quick.sh`
Instant high load - quick test:
- Immediately generates 50 req/sec
- Runs for 90 seconds
- Fast way to trigger scale-up

### `watch.sh`
Monitor running containers:
```bash
bash examples/single-service/watch.sh
```

## Configuration

See [docktor.yaml](docktor.yaml) for the complete configuration.

Key settings:
```yaml
service: web
compose_file: docker-compose.yaml

scaling:
  cpu_high: 75.0
  cpu_low: 20.0
  min_replicas: 2
  max_replicas: 10
  scale_up_by: 2
  scale_down_by: 1
  check_interval: 10
  metrics_window: 10
```

## Next Steps

- Try the [multi-service NATS queue example](../multi-service/nats-queue/) for queue-aware scaling
- Customize thresholds in `docktor.yaml`
- Test with your own Docker Compose services
