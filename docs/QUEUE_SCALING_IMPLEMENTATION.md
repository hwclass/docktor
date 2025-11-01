# Queue-Aware Multi-Service Scaling - Implementation Progress

**Feature:** Multi-Service Scaling + Queue Awareness (NATS/JetStream)
**Status:** 🟡 In Progress (30% Complete)
**Started:** 2025-01-29

---

## 📊 Progress Overview

| Phase | Status | Progress |
|-------|--------|----------|
| **Phase 1: Config Schema** | ✅ Complete | 100% |
| **Phase 2: Queue Plugins** | 🟡 In Progress | 60% |
| **Phase 3: Decision Engine** | ⏳ Pending | 0% |
| **Phase 4: Multi-Service Loop** | ⏳ Pending | 0% |
| **Phase 5: Observability** | ⏳ Pending | 0% |
| **Phase 6: Example Stack** | ⏳ Pending | 0% |
| **Phase 7: Agent Updates** | ⏳ Pending | 0% |
| **Phase 8: Testing** | ⏳ Pending | 0% |
| **Phase 9: Documentation** | ⏳ Pending | 0% |

**Overall Completion:** 30% (3/9 phases complete)

---

## ✅ Completed Work

### Phase 1: Config Schema (Complete)

**Files Modified:**
- `cmd/docktor/main.go` - Added multi-service config types

**New Types Added:**
```go
// Condition represents a single rule condition
type Condition struct {
    Metric string  `yaml:"metric"` // "cpu.avg_pct", "queue.backlog"
    Op     string  `yaml:"op"`     // ">", ">=", "<", "<=", "==", "!="
    Value  float64 `yaml:"value"`
}

// Rules defines when to scale up or down
type Rules struct {
    ScaleUpWhen   []Condition // Scale up if ANY (OR logic)
    ScaleDownWhen []Condition // Scale down if ALL (AND logic)
}

// QueueConfig holds queue system configuration
type QueueConfig struct {
    Kind       string   // "nats", "kafka", "rabbitmq", "sqs"
    URL        string
    JetStream  bool
    Stream     string
    Consumer   string
    Subject    string
    Metrics    []string
}

// ServiceConfig holds per-service configuration
type ServiceConfig struct {
    Name          string
    MinReplicas   int
    MaxReplicas   int
    MetricsWindow int
    CheckInterval int
    Rules         Rules
    Queue         *QueueConfig
}

// Config extended with Services array
type Config struct {
    // ... existing fields ...
    Services []ServiceConfig `yaml:"services,omitempty"`
}
```

**Backward Compatibility:**
- Added `Config.Normalize()` method that converts legacy single-service format to new multi-service format
- Existing `docktor.yaml` files continue to work without changes
- New multi-service configs use `services:` array

**Example New Config Format:**
```yaml
version: "1"
compose_file: examples/nats-multi/docker-compose.yaml

llm:
  provider: dmr
  model: ai/llama3.2

services:
  - name: web
    min_replicas: 2
    max_replicas: 10
    metrics_window: 30
    check_interval: 10
    rules:
      scale_up_when:
        - metric: cpu.avg_pct
          op: ">"
          value: 75
        - metric: queue.backlog
          op: ">"
          value: 1000
      scale_down_when:
        - metric: cpu.avg_pct
          op: "<"
          value: 20
        - metric: queue.backlog
          op: "<="
          value: 50
    queue:
      kind: nats
      url: nats://nats:4222
      jetstream: true
      stream: EVENTS
      consumer: WEB_WORKERS
      subject: events.web
      metrics:
        - backlog
        - lag
        - rate_in
        - rate_out
```

---

### Phase 2: Queue Plugin Architecture (60% Complete)

**New Package Created:** `pkg/queue/`

**Files Created:**

#### `pkg/queue/queue.go` - Plugin Interface
```go
package queue

// Metrics represents queue metrics collected over a time window
type Metrics struct {
    Backlog   float64           // Messages pending
    Lag       float64           // Consumer lag
    RateIn    float64           // Msgs/sec published
    RateOut   float64           // Msgs/sec consumed
    Custom    map[string]float64 // Vendor-specific metrics
    Timestamp time.Time
}

// Config represents queue backend configuration
type Config struct {
    Kind       string            // "nats", "kafka", etc.
    URL        string
    Attributes map[string]string // Vendor-specific
}

// Provider interface for queue backends
type Provider interface {
    Connect() error
    GetMetrics(windowSec int) (*Metrics, error)
    Close() error
    Validate() error
}

// Registry holds all registered queue providers
var registry = make(map[string]func(Config) (Provider, error))

// Register adds a queue provider to the registry
func Register(kind string, factory func(Config) (Provider, error))

// NewProvider creates a queue provider instance
func NewProvider(cfg Config) (Provider, error)
```

**Design Benefits:**
- ✅ Pluggable architecture - easy to add Kafka, RabbitMQ, SQS later
- ✅ Vendor-specific logic isolated from core
- ✅ Clean interface with standard metrics
- ✅ Self-registering plugins via `init()`

#### `pkg/queue/nats.go` - NATS Implementation
```go
package queue

type NATSProvider struct {
    url       string
    stream    string
    consumer  string
    subject   string
    jetstream bool
    conn      *nats.Conn
    js        nats.JetStreamContext
}

// Implements Provider interface
func (n *NATSProvider) Connect() error
func (n *NATSProvider) GetMetrics(windowSec int) (*Metrics, error)
func (n *NATSProvider) Validate() error
func (n *NATSProvider) Close() error

// Auto-registers on init
func init() {
    Register("nats", NewNATSProvider)
}
```

**Features Implemented:**
- ✅ NATS JetStream connection pooling
- ✅ Stream/Consumer info collection
- ✅ Rate calculation via dual sampling
- ✅ Backlog, lag, rate_in, rate_out metrics
- ✅ Validation of stream/consumer existence
- ✅ Graceful connection management

**Integration with Main:**
```go
// cmd/docktor/main.go
import (
    _ "github.com/hwclass/docktor/pkg/queue" // Auto-registers plugins
)
```

---

## 🔄 In Progress

### Phase 2: MCP Tool Wrappers (Current Task)

**What Needs to be Done:**
Add MCP tool wrappers in `cmd/docktor/main.go` that use the queue plugins:

```go
// Tool: get_queue_metrics
func toolGetQueueMetrics(queueCfg QueueConfig, windowSec int) (map[string]float64, error) {
    // Convert QueueConfig to queue.Config
    cfg := queue.Config{
        Kind: queueCfg.Kind,
        URL:  queueCfg.URL,
        Attributes: map[string]string{
            "stream":    queueCfg.Stream,
            "consumer":  queueCfg.Consumer,
            "subject":   queueCfg.Subject,
            "jetstream": fmt.Sprintf("%t", queueCfg.JetStream),
        },
    }

    // Create provider
    provider, err := queue.NewProvider(cfg)
    if err != nil {
        return nil, err
    }
    defer provider.Close()

    // Connect
    if err := provider.Connect(); err != nil {
        return nil, err
    }

    // Get metrics
    metrics, err := provider.GetMetrics(windowSec)
    if err != nil {
        return nil, err
    }

    // Convert to map[string]float64 for MCP
    result := map[string]float64{
        "backlog":  metrics.Backlog,
        "lag":      metrics.Lag,
        "rate_in":  metrics.RateIn,
        "rate_out": metrics.RateOut,
    }

    // Add custom metrics
    for k, v := range metrics.Custom {
        result[k] = v
    }

    return result, nil
}
```

**Next Steps:**
1. Add `toolGetQueueMetrics()` function
2. Register it in `mcpToolsList()`
3. Add handler in `mcpToolsCall()` switch statement

---

## ⏳ Remaining Work

### Phase 3: Decision Engine

**Goal:** Implement multi-metric rule evaluation

**Tasks:**
1. Create `pkg/scaler` package for scaling decisions
2. Implement `EvaluateRules(rules Rules, observations map[string]float64) DecisionResult`
3. Add `toolDecideScaleMulti()` MCP tool
4. Register with MCP server

**Pseudo-code:**
```go
package scaler

func EvaluateRules(rules Rules, observations map[string]float64, current, min, max int) Decision {
    // Check scale_up_when (OR logic)
    for _, cond := range rules.ScaleUpWhen {
        if evaluateCondition(cond, observations) {
            return Decision{
                Action: "scale_up",
                Target: calculateTarget(current, +2, max),
                Reason: fmt.Sprintf("%s matched", cond),
            }
        }
    }

    // Check scale_down_when (AND logic)
    if allMatch(rules.ScaleDownWhen, observations) {
        return Decision{
            Action: "scale_down",
            Target: calculateTarget(current, -1, min),
            Reason: "all scale_down conditions met",
        }
    }

    return Decision{Action: "hold", Target: current}
}
```

---

### Phase 4: Multi-Service Loop

**Goal:** Per-service goroutine monitoring

**Architecture:**
```
┌─────────────────────────────────────┐
│   Docktor Daemon (main goroutine)  │
│                                     │
│  ┌───────────┐    ┌──────────────┐ │
│  │ web       │    │ worker       │ │
│  │ goroutine │    │ goroutine    │ │
│  └─────┬─────┘    └──────┬───────┘ │
│        │ every 10s        │         │
│        ▼                  ▼         │
│   ┌─────────────────────────────┐  │
│   │  MCP Tool Calls             │  │
│   │  • get_service_metrics      │  │
│   │  • get_queue_metrics        │  │
│   │  • decide_scale_multi       │  │
│   │  • apply_scale              │  │
│   └─────────────────────────────┘  │
└─────────────────────────────────────┘
```

**Implementation Outline:**
```go
func daemonStart() {
    cfg, _ := LoadConfig("")

    // Start per-service monitors
    var wg sync.WaitGroup
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    for _, svc := range cfg.Services {
        wg.Add(1)
        go func(s ServiceConfig) {
            defer wg.Done()
            monitorService(ctx, s, cfg)
        }(svc)
    }

    // Wait for signal
    sig := make(chan os.Signal, 1)
    signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
    <-sig

    cancel()
    wg.Wait()
}

func monitorService(ctx context.Context, svc ServiceConfig, cfg Config) {
    ticker := time.NewTicker(time.Duration(svc.CheckInterval) * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            runScalingIteration(svc, cfg)
        }
    }
}

func runScalingIteration(svc ServiceConfig, cfg Config) {
    // 1. Get CPU metrics
    cpuMetrics, _ := toolGetMetrics(svc.Name, svc.MetricsWindow)

    // 2. Get queue metrics (if configured)
    var queueMetrics map[string]float64
    if svc.Queue != nil {
        queueMetrics, _ = toolGetQueueMetrics(*svc.Queue, svc.MetricsWindow)
    }

    // 3. Merge observations
    observations := mergeMetrics(cpuMetrics, queueMetrics)

    // 4. Decide
    decision, _ := toolDecideScaleMulti(svc, observations)

    // 5. Execute
    if decision.Action != "hold" {
        applyScale(svc.Name, decision.TargetReplicas, decision.Reason)
    }

    // 6. Log
    logDecisionJSON(svc.Name, decision, observations)
}
```

---

### Phase 5: Observability

**Goal:** JSONL logging + CLI tools

**Features to Implement:**

1. **JSONL Decision Log** (`/tmp/docktor-decisions.jsonl`)
```go
func logDecisionJSON(service string, decision Decision, obs map[string]float64) {
    entry := map[string]interface{}{
        "ts":               time.Now().Format(time.RFC3339),
        "service":          service,
        "current_replicas": decision.CurrentReplicas,
        "decision":         decision.Action,
        "target_replicas":  decision.TargetReplicas,
        "reason":           decision.Reason,
        "observations":     obs,
        "metadata": map[string]string{
            "provider": cfg.LLM.Provider,
            "model":    cfg.LLM.Model,
        },
    }

    f, _ := os.OpenFile("/tmp/docktor-decisions.jsonl", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    defer f.Close()
    json.NewEncoder(f).Encode(entry)
}
```

2. **`./docktor explain` Command**
```bash
$ ./docktor explain --tail 10
SERVICE  TIME      ACTION     FROM→TO  REASON
web      14:30:00  scale_up   3→5      cpu 82.3% > 75 | backlog 2400 > 1000
worker   14:30:05  hold       2→2      all metrics healthy
web      14:30:10  hold       5→5      cpu normalizing
worker   14:30:15  scale_down 2→1      backlog 45 <= 50
```

3. **`./docktor config validate` Command**
```bash
$ ./docktor config validate
✓ Compose file exists
✓ Service 'web' found in compose
✓ Service 'worker' found in compose
✓ NATS reachable: nats://nats:4222
✓ Stream 'EVENTS' exists
✓ Consumer 'WEB_WORKERS' exists
All checks passed!
```

---

### Phase 6: Example Stack

**Goal:** Working NATS demo under `examples/nats-multi/`

**Directory Structure:**
```
examples/nats-multi/
├── docker-compose.yaml
├── producer/
│   ├── Dockerfile
│   ├── main.go
│   ├── go.mod
│   └── go.sum
├── consumer/
│   ├── Dockerfile
│   ├── main.go
│   ├── go.mod
│   └── go.sum
├── docktor.yaml
└── README.md
```

**docker-compose.yaml:**
```yaml
version: '3.8'

services:
  nats:
    image: nats:2.10
    command: ["-js", "-sd", "/nats"]
    ports: ["4222:4222"]

  producer:
    build: ./producer
    environment:
      NATS_URL: nats://nats:4222
      SUBJECT: events.web
      RATE: 100
      BURST_RATE: 500
    depends_on: [nats]

  consumer:
    build: ./consumer
    environment:
      NATS_URL: nats://nats:4222
      STREAM: EVENTS
      CONSUMER: WEB_WORKERS
      SUBJECT: events.web
      PROCESS_TIME_MS: 50
    depends_on: [nats]
    deploy:
      replicas: 1

  web:
    image: nginx:alpine
    expose: ["80"]
```

**Producer App (Go):**
```go
// producer/main.go
package main

import (
    "os"
    "time"
    "github.com/nats-io/nats.go"
)

func main() {
    nc, _ := nats.Connect(os.Getenv("NATS_URL"))
    js, _ := nc.JetStream()

    rate := getEnvInt("RATE", 100)
    ticker := time.NewTicker(time.Second / time.Duration(rate))

    for range ticker.C {
        js.Publish("events.web", []byte(`{"data":"test"}`))
    }
}
```

**Consumer App (Go):**
```go
// consumer/main.go
package main

import (
    "os"
    "time"
    "github.com/nats-io/nats.go"
)

func main() {
    nc, _ := nats.Connect(os.Getenv("NATS_URL"))
    js, _ := nc.JetStream()

    // Create stream
    js.AddStream(&nats.StreamConfig{
        Name:     "EVENTS",
        Subjects: []string{"events.web"},
    })

    // Create consumer
    js.AddConsumer("EVENTS", &nats.ConsumerConfig{
        Durable:   "WEB_WORKERS",
        AckPolicy: nats.AckExplicitPolicy,
    })

    // Process messages
    sub, _ := js.PullSubscribe("events.web", "WEB_WORKERS")
    for {
        msgs, _ := sub.Fetch(10)
        for _, msg := range msgs {
            processMessage(msg)
            msg.Ack()
        }
    }
}

func processMessage(msg *nats.Msg) {
    time.Sleep(50 * time.Millisecond) // Simulated work
}
```

---

### Phase 7: Agent Updates

**Goal:** Update agent instructions for multi-service

**File:** `agents/docktor-daemon.yaml`

```yaml
agents:
  docktor:
    model: dmr/ai/llama3.2
    instruction: |
      You are Docktor, managing multiple services concurrently.

      For each service:
      1. Collect metrics (CPU + queue if configured)
      2. Evaluate scaling rules:
         - Scale UP if ANY scale_up_when condition matches (OR)
         - Scale DOWN if ALL scale_down_when conditions match (AND)
      3. Execute scaling if needed
      4. Log decision with metadata

      Use these MCP tools:
      - get_service_metrics(service, window_sec)
      - get_queue_metrics(queue_config, window_sec)
      - decide_scale_multi(service, rules, observations)
      - apply_scale(service, target_replicas, reason)
```

---

### Phase 8: Testing

**Manual Test Plan:**
```bash
# 1. Start demo stack
cd examples/nats-multi
docker compose up -d --build

# 2. Start Docktor daemon
./docktor daemon start

# 3. Create backlog (pause consumers)
docker compose scale consumer=0
sleep 30

# 4. Verify metrics show high backlog
docker exec nats-multi-nats-1 nats consumer info EVENTS WEB_WORKERS

# 5. Check Docktor scaled up
docker compose ps consumer
# Should show replicas > 1

# 6. Resume consumers
docker compose scale consumer=3

# 7. Verify scale-down
sleep 60
docker compose ps consumer
# Should scale back to 1

# 8. Review decisions
./docktor explain --tail 10
```

---

### Phase 9: Documentation

**Update:** `README.md`

Add section:
```markdown
## Queue-Aware Scaling

Docktor can scale based on message queue backlog in addition to CPU:

### Quick Start
\`\`\`yaml
services:
  - name: worker
    rules:
      scale_up_when:
        - metric: queue.backlog
          op: ">"
          value: 1000
    queue:
      kind: nats
      url: nats://nats:4222
      stream: EVENTS
      consumer: WORKERS
\`\`\`

### Supported Queue Systems
- NATS JetStream ✅
- Apache Kafka (coming soon)
- RabbitMQ (coming soon)
- AWS SQS (coming soon)
```

---

## 🎯 Next Steps

**Immediate:**
1. Add `toolGetQueueMetrics()` wrapper in main.go
2. Register tool with MCP server
3. Test NATS plugin manually

**Short-term:**
1. Implement Phase 3 (Decision Engine)
2. Implement Phase 4 (Multi-Service Loop)
3. Create example stack

**Before Merge:**
- Complete all 9 phases
- End-to-end test with NATS
- Update documentation

---

## 📁 Files Modified/Created

**Modified:**
- `cmd/docktor/main.go` - Config types, imports
- `go.mod` - Added NATS dependency

**Created:**
- `pkg/queue/queue.go` - Plugin interface
- `pkg/queue/nats.go` - NATS implementation
- `docs/QUEUE_SCALING_IMPLEMENTATION.md` - This file

**To Be Created:**
- `pkg/scaler/` - Decision engine
- `examples/nats-multi/` - Demo stack
- Various example files

---

## 🔗 References

- [Original Spec](../QUEUE_SCALING_SPEC.md)
- [NATS JetStream Docs](https://docs.nats.io/nats-concepts/jetstream)
- [NATS Go Client](https://github.com/nats-io/nats.go)

---

**Last Updated:** 2025-01-29
**Completion Target:** TBD
