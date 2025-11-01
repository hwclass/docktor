# Docktor Examples

This directory contains complete examples demonstrating different Docktor use cases.

## 📁 Directory Structure

```
examples/
├── single-service/           # Simple CPU-based autoscaling
│   ├── docker-compose.yaml   # Single nginx service
│   ├── docktor.yaml          # CPU threshold configuration
│   ├── load-*.sh             # Load testing scripts
│   └── README.md             # Detailed guide
│
└── multi-service/
    └── nats-queue/           # Queue-aware autoscaling with NATS
        ├── docker-compose.yaml
        ├── docktor.yaml      # Multi-metric rules (CPU + queue)
        ├── producer/         # Go app: generates message load
        ├── consumer/         # Go app: processes messages (scales based on backlog)
        └── README.md
```

## 🎯 Which Example Should I Use?

### Start with `single-service/` if:
- You're new to Docktor
- You want CPU-based autoscaling
- You have a single service to scale
- You want to understand the basics

**Time to run**: 5 minutes
**Complexity**: ⭐ Beginner

[→ Go to single-service example](single-service/)

---

### Try `multi-service/nats-queue/` if:
- You understand the basics
- You have message queue workloads (NATS, RabbitMQ, Kafka)
- You want to scale based on queue metrics (backlog, lag, rates)
- You need to monitor multiple services

**Time to run**: 10 minutes
**Complexity**: ⭐⭐⭐ Advanced

[→ Go to multi-service/nats-queue example](multi-service/nats-queue/)

---

## 🚀 Quick Start

### Single-Service Example
```bash
cd examples/single-service
docker compose up -d
cd ../..
./docktor daemon start --config examples/single-service/docktor.yaml

# Generate load
bash examples/single-service/load-incremental.sh
```

### Multi-Service Queue Example
```bash
cd examples/multi-service/nats-queue
docker compose up -d
cd ../../..
./docktor daemon start --config examples/multi-service/nats-queue/docktor.yaml

# Producer automatically generates load
# Watch it scale based on queue backlog
./docktor daemon logs
```

## 📊 Feature Comparison

| Feature | Single-Service | Multi-Service/NATS |
|---------|---------------|-------------------|
| Services Monitored | 1 | Multiple (2+) |
| Metrics | CPU only | CPU + Queue metrics |
| Scaling Logic | Simple thresholds | Multi-metric rules (OR/AND) |
| Queue Integration | ❌ | ✅ NATS JetStream |
| Real Load Generator | ❌ (manual scripts) | ✅ (producer app) |
| Production-Ready | Basic | Advanced |

## 🔧 Common Commands

```bash
# Start daemon with specific config
./docktor daemon start --config examples/<path>/docktor.yaml

# View scaling decisions
./docktor explain --tail 20

# Validate configuration
./docktor config validate

# Watch logs
./docktor daemon logs

# Stop daemon
./docktor daemon stop
```

## 📚 Learn More

- [Main README](../README.md) - Full Docktor documentation
- [AUTOSCALE_GUIDE.md](../AUTOSCALE_GUIDE.md) - Testing guide
- [AGENTS.md](../AGENTS.md) - Agent architecture details

## 🎓 Learning Path

1. **Start**: Run `single-service/` to understand CPU-based scaling
2. **Explore**: Modify `docktor.yaml` thresholds and observe behavior
3. **Advance**: Try `multi-service/nats-queue/` for queue-aware scaling
4. **Customize**: Create your own multi-service config for your stack

---

**Need help?** Check the README in each example directory for detailed instructions.
