# NATS Queue-Aware Autoscaling Example

This example demonstrates **queue-aware autoscaling** with Docktor using NATS JetStream. The consumer service scales based on message backlog and processing rates rather than CPU usage.

## Architecture

```
┌─────────────┐     ┌──────────┐     ┌────────────┐
│  Producer   │────▶│   NATS   │────▶│  Consumer  │
│ 100 msgs/s  │     │JetStream │     │ (scalable) │
│ (burst 500) │     │          │     │  1-10      │
└─────────────┘     └──────────┘     │  replicas  │
                          │           └────────────┘
                          ▼
                    ┌──────────┐
                    │ Docktor  │
                    │ Monitors │
                    │ & Scales │
                    └──────────┘
```

## What Gets Scaled

**Consumer Service:** Scales from 1 to 10 replicas based on:
- **Queue backlog** (messages pending in NATS consumer)
- **Message rates** (incoming vs. processing rate)

**Scaling Rules:**
- **Scale UP** if backlog > 500 messages **OR** rate_in > 200 msgs/sec
- **Scale DOWN** if backlog ≤ 100 messages **AND** rate_in < 150 msgs/sec

## Prerequisites

- Docker Desktop with Model Runner enabled
- Docktor binary built: `go build -o ../../docktor ../../cmd/docktor`
- Docker Compose v2+

## Quick Start

### 1. Start the Stack

```bash
cd examples/nats-multi
docker compose up -d --build
```

This starts:
- **NATS Server** (JetStream enabled)
- **Producer** (publishes messages at 100 msgs/sec, bursts to 500 msgs/sec every 60s)
- **Consumer** (processes messages, initially 1 replica)
- **Web** (nginx, for demonstration)

### 2. Verify Services Are Running

```bash
docker compose ps
```

Expected output:
```
NAME                    STATUS          PORTS
nats-multi-nats-1       Up (healthy)    0.0.0.0:4222->4222/tcp
nats-multi-producer-1   Up
nats-multi-consumer-1   Up
nats-multi-web-1        Up              80/tcp
nats-multi-web-2        Up              80/tcp
```

### 3. Check NATS Stream and Consumer

```bash
# Install NATS CLI if not already installed
# brew install nats-io/nats-tools/nats

# Check stream status
docker exec nats-multi-nats-1 /nats stream info EVENTS

# Check consumer status
docker exec nats-multi-nats-1 /nats consumer info EVENTS WEB_WORKERS
```

You should see messages being published and consumed.

### 4. Start Docktor Daemon

From the repository root:

```bash
# Make sure you're in the repo root
cd ../..

# Start Docktor daemon (it will auto-discover docktor.yaml in examples/nats-multi/)
./docktor daemon start --config examples/nats-multi/docktor.yaml
```

Expected output:
```
=== Starting Docktor Daemon ===
Mode: AUTONOMOUS
Config: examples/nats-multi/docktor.yaml
Services: consumer

LLM Config:
  Provider: dmr
  Model: ai/llama3.2

Service: consumer
  Min/Max Replicas: 1/10
  Check Interval: 10s
  Queue: nats://nats:4222 (EVENTS/WEB_WORKERS)

✓ Daemon started successfully
```

### 5. Monitor Scaling Activity

**Watch Docktor logs:**
```bash
./docktor daemon logs
```

**Watch consumer replicas:**
```bash
watch -n 2 'docker compose -f examples/nats-multi/docker-compose.yaml ps consumer'
```

**Watch NATS consumer metrics:**
```bash
watch -n 2 'docker exec nats-multi-nats-1 /nats consumer info EVENTS WEB_WORKERS'
```

## Testing Scenarios

### Scenario 1: Scale Up (Artificial Backlog)

Create a backlog by temporarily stopping consumers:

```bash
# Stop all consumers
docker compose scale consumer=0

# Wait 30 seconds for backlog to build up
sleep 30

# Check backlog (should be > 500)
docker exec nats-multi-nats-1 /nats consumer info EVENTS WEB_WORKERS

# Resume consumers (Docktor will scale automatically)
docker compose scale consumer=1
```

**Expected Behavior:**
- Backlog grows to 2000+ messages
- Docktor detects: `queue.backlog > 500`
- Decision: Scale UP
- Consumers scale from 1 → 3 → 5 (progressive)
- Backlog drains as more consumers process messages

### Scenario 2: Scale Down (Idle Period)

Let the system idle after backlog is cleared:

```bash
# Check current backlog
docker exec nats-multi-nats-1 /nats consumer info EVENTS WEB_WORKERS

# Wait for backlog to drain (< 100 messages)
# Docktor will automatically scale down
```

**Expected Behavior:**
- Backlog drains to < 100 messages
- Rate_in drops below 150 msgs/sec
- Docktor detects: ALL scale_down conditions met
- Decision: Scale DOWN
- Consumers scale 5 → 4 → 3 → 2 → 1 (gradual)

### Scenario 3: Burst Handling

The producer automatically creates bursts every 60 seconds:

```bash
# Watch producer logs
docker logs -f nats-multi-producer-1

# You'll see:
# "🔥 Starting burst mode for 10 seconds"
# Rate increases from 100 → 500 msgs/sec
```

**Expected Behavior:**
- During burst: rate_in > 200 msgs/sec
- Docktor detects high rate
- Scales UP preemptively
- After burst: rate normalizes, scales down

## Understanding the Logs

**Docktor Decision Log:**
```json
{
  "ts": "2025-01-29T14:30:00Z",
  "service": "consumer",
  "current_replicas": 1,
  "decision": "scale_up",
  "target_replicas": 3,
  "reason": "queue.backlog 1500.0 > 500",
  "observations": {
    "queue.backlog": 1500,
    "queue.lag": 0,
    "queue.rate_in": 300.5,
    "queue.rate_out": 95.2
  },
  "metadata": {
    "provider": "dmr",
    "model": "ai/llama3.2"
  }
}
```

**Key Fields:**
- `queue.backlog`: Messages pending in consumer queue
- `queue.rate_in`: Messages/sec being published
- `queue.rate_out`: Messages/sec being consumed
- `reason`: Which rule triggered the decision

## Cleanup

```bash
# Stop Docktor daemon
./docktor daemon stop

# Stop Docker Compose stack
cd examples/nats-multi
docker compose down -v  # -v removes volumes (clears NATS data)
```

## Troubleshooting

### "Failed to connect to NATS"
```bash
# Check if NATS is running
docker compose ps nats

# Check NATS logs
docker logs nats-multi-nats-1

# Restart NATS
docker compose restart nats
```

### "Stream/Consumer not found"
The producer and consumer automatically create streams/consumers on startup. If you see this error:

```bash
# Recreate stream manually
docker exec -it nats-multi-nats-1 /nats stream add EVENTS \
  --subjects="events.web" \
  --storage=file \
  --max-age=24h

# Recreate consumer
docker exec -it nats-multi-nats-1 /nats consumer add EVENTS WEB_WORKERS \
  --pull \
  --ack=explicit \
  --deliver=all
```

### "Docktor not scaling"
```bash
# Check Docktor logs
./docktor daemon logs

# Verify queue metrics are being collected
# Look for lines like: "get_queue_metrics response"

# Check if rules are configured correctly
cat examples/nats-multi/docktor.yaml
```

### "Consumer not processing messages"
```bash
# Check consumer logs
docker logs nats-multi-consumer-1

# Check if consumers can reach NATS
docker exec nats-multi-consumer-1 wget -qO- http://nats:8222/varz
```

## Advanced Configuration

### Adjust Scaling Thresholds

Edit `docktor.yaml`:

```yaml
services:
  - name: consumer
    rules:
      scale_up_when:
        - metric: queue.backlog
          op: ">"
          value: 1000        # More aggressive (scale up at 1000 msgs)
      scale_down_when:
        - metric: queue.backlog
          op: "<="
          value: 50          # More conservative (keep replicas longer)
```

### Adjust Producer Rate

```bash
docker compose up -d \
  -e RATE=200 \
  -e BURST_RATE=1000 \
  producer
```

### Adjust Consumer Processing Speed

```bash
docker compose up -d \
  -e PROCESS_TIME_MS=100 \  # Slower processing (more backlog)
  consumer
```

## What's Next

After running this example, you can:

1. **Add More Services:** Add a second service that scales based on different queues
2. **Mix Metrics:** Combine queue + CPU metrics for hybrid scaling
3. **Production Setup:** Use this pattern for real workloads (Kafka, RabbitMQ, SQS)

## Related Examples

- [../docker-compose.yaml](../docker-compose.yaml) - CPU-based autoscaling (original)
- [../../README.md](../../README.md) - Full Docktor documentation

---

**Questions?** Check the main [Docktor README](../../README.md) or open an issue.
