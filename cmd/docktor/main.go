package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hwclass/docktor/pkg/queue"
	_ "github.com/hwclass/docktor/pkg/queue" // Import queue plugins for auto-registration
	"gopkg.in/yaml.v3"
)

const (
	modelRunnerV1URL     = "http://localhost:12434/v1/models"
	modelRunnerEngineURL = "http://localhost:12434/engines/llama.cpp/v1/models"
	modelRunnerBaseURL   = "http://localhost:12434/engines/llama.cpp/v1"
)

func printBanner() {
	cyan := "\033[36m"
	blue := "\033[34m"
	green := "\033[32m"
	reset := "\033[0m"

	fmt.Println(cyan + `
    _______________      ___________
   |_|_|_|_|_|_|_|_|    |     /\    |
   |_|_|_|_|_|_|_|_|    |    /  \   |
     |_|_|_|_|_|_|      |   /    \  |
       |_|_|_|          |  /      \ |
    ___________________/|_/________\|
   /                                  \
  /   ` + blue + `##` + cyan + `    ##                         \
 |     ` + blue + `##` + cyan + `  ##    ` + green + `____` + cyan + `              |
 |       ` + blue + `####` + cyan + `     ` + green + `|  o |` + cyan + `             |
 |        ` + blue + `##` + cyan + `      ` + green + `|____|` + cyan + `             |
  \       ` + blue + `##` + cyan + `                         /
   \_________________________________/

` + blue + `        ___                _    _
       |   \ ___  __ _ _| |_ | |_ ___  _ _
       | |) / _ \/ _| / /  _|| '_/ _ \| '_|
       |___/\___/\__|_\_\\__||_| \___/|_|
` + reset + `
   🐳 AI-Native Autoscaling for Docker Compose
   ` + cyan + `https://github.com/hwclass/docktor` + reset + `
`)
}

func usage() {
	fmt.Println(`docktor CLI
Usage:
  docktor daemon <start|stop|status|logs> [options]
  docktor config <list-models|set-model|validate> [options]
  docktor explain [--tail N] [--service NAME]
  docktor ai up [--debug] [--no-install] [--skip-compose] [--headless]

Commands:
  daemon    Autonomous autoscaling daemon
    start   Start daemon (autonomous by default)
            --config: Path to docktor.yaml config file
            --manual: Require approval for each action
            --compose-file: Path to compose file (overrides config)
            --service: Service name to monitor (overrides config)
            --interval: Check interval in seconds (overrides config, e.g., 30)
    stop    Stop running daemon
    status  Check daemon status
    logs    Follow daemon logs

  config    Configure LLM model selection
    list-models       List available models from Docker Model Runner
    set-model <ID>    Set the active model
            --provider=dmr|openai: LLM provider (default: keeps current)
            --base-url=<URL>: API base URL (default: keeps current)
    validate          Validate configuration and connectivity

  explain   Show scaling decision history
            --tail N: Show last N decisions (default: 10)
            --service NAME: Filter by service name

  ai up     Launch AI autoscaling agent (legacy interactive mode)
            --debug: Enable verbose logging
            --headless: Run autoscaling loop without TUI
            --no-install: Skip automatic cagent installation
            --skip-compose: Skip docker compose up/down (agent monitors existing containers)

Examples:
  # Configure LLM model
  docktor config list-models
  docktor config set-model granite-4.0-1b
  docktor config set-model gpt-4o-mini --provider=openai --base-url=https://api.openai.com/v1

  # Start autonomous daemon (default - auto-approves all actions)
  docktor daemon start

  # Start manual daemon (requires user approval)
  docktor daemon start --manual

  # Check every 30 seconds instead of default 10
  docktor daemon start --interval 30

  # Monitor custom compose file and service with custom interval
  docktor daemon start --compose-file ./prod.yaml --service api --interval 60

  # Check daemon status and logs
  docktor daemon status
  docktor daemon logs

Internal:
  docktor mcp
            MCP stdio server (called internally by cagent, not for direct use)`)
}

type opts struct {
	debug       bool
	noInstall   bool
	skipCompose bool
	headless    bool
}

type daemonOpts struct {
	manual        bool
	composeFile   string
	service       string
	configFile    string
	checkInterval int
}

// Config represents docktor.yaml configuration
type Config struct {
	Version     string          `yaml:"version"`
	Service     string          `yaml:"service,omitempty"`      // Legacy: single service name (backward compatible)
	ComposeFile string          `yaml:"compose_file"`
	Scaling     ScalingConfig   `yaml:"scaling,omitempty"`      // Legacy: single service scaling config
	LLM         LLMConfig       `yaml:"llm"`
	Services    []ServiceConfig `yaml:"services,omitempty"`     // New: multi-service configuration
}

// ScalingConfig holds scaling thresholds and parameters
type ScalingConfig struct {
	CPUHigh       float64 `yaml:"cpu_high"`
	CPULow        float64 `yaml:"cpu_low"`
	MinReplicas   int     `yaml:"min_replicas"`
	MaxReplicas   int     `yaml:"max_replicas"`
	ScaleUpBy     int     `yaml:"scale_up_by"`
	ScaleDownBy   int     `yaml:"scale_down_by"`
	CheckInterval int     `yaml:"check_interval"`
	MetricsWindow int     `yaml:"metrics_window"`
}

// LLMConfig holds LLM provider settings
type LLMConfig struct {
	Provider string `yaml:"provider"` // "dmr" or "openai"
	BaseURL  string `yaml:"base_url"`
	Model    string `yaml:"model"`
}

// Condition represents a single rule condition for scaling
type Condition struct {
	Metric string  `yaml:"metric"` // e.g., "cpu.avg_pct", "queue.backlog"
	Op     string  `yaml:"op"`     // ">", ">=", "<", "<=", "==", "!="
	Value  float64 `yaml:"value"`
}

// Rules defines when to scale up or down
type Rules struct {
	ScaleUpWhen   []Condition `yaml:"scale_up_when"`   // Scale up if ANY condition matches (OR)
	ScaleDownWhen []Condition `yaml:"scale_down_when"` // Scale down if ALL conditions match (AND)
}

// QueueConfig holds queue/messaging system configuration
type QueueConfig struct {
	Kind       string   `yaml:"kind"`       // "nats", "kafka", "rabbitmq", "sqs"
	URL        string   `yaml:"url"`        // Connection URL
	JetStream  bool     `yaml:"jetstream"`  // NATS: use JetStream
	Stream     string   `yaml:"stream"`     // NATS: stream name
	Consumer   string   `yaml:"consumer"`   // NATS: consumer name
	Subject    string   `yaml:"subject"`    // NATS: subject filter
	Metrics    []string `yaml:"metrics"`    // Metrics to collect: backlog, lag, rate_in, rate_out
}

// ServiceConfig holds per-service monitoring and scaling configuration
type ServiceConfig struct {
	Name          string       `yaml:"name"`
	MinReplicas   int          `yaml:"min_replicas"`
	MaxReplicas   int          `yaml:"max_replicas"`
	MetricsWindow int          `yaml:"metrics_window"` // seconds
	CheckInterval int          `yaml:"check_interval"` // seconds
	Rules         Rules        `yaml:"rules"`
	Queue         *QueueConfig `yaml:"queue,omitempty"` // Optional queue configuration
}

// DefaultConfig returns config with sensible defaults
func DefaultConfig() Config {
	return Config{
		Version:     "1",
		Service:     "web",
		ComposeFile: "examples/docker-compose.yaml",
		Scaling: ScalingConfig{
			CPUHigh:       75.0,
			CPULow:        20.0,
			MinReplicas:   2,
			MaxReplicas:   10,
			ScaleUpBy:     2,
			ScaleDownBy:   1,
			CheckInterval: 10,
			MetricsWindow: 10,
		},
		LLM: LLMConfig{
			Provider: "dmr",
			BaseURL:  "http://localhost:12434/engines/llama.cpp/v1",
			Model:    "ai/llama3.2",
		},
	}
}

// LoadConfig loads configuration from YAML file
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()

	// Auto-discover config file if not specified
	if path == "" {
		// Check for docktor.yaml in current directory
		if _, err := os.Stat("docktor.yaml"); err == nil {
			path = "docktor.yaml"
		} else if _, err := os.Stat("docktor.yml"); err == nil {
			path = "docktor.yml"
		} else {
			// No config file found, use defaults
			return cfg, nil
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("failed to read config: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("failed to parse config: %w", err)
	}

	// Resolve relative paths in config relative to config file location
	configDir := filepath.Dir(path)
	if !filepath.IsAbs(cfg.ComposeFile) {
		cfg.ComposeFile = filepath.Join(configDir, cfg.ComposeFile)
	}

	// Validate
	if cfg.Scaling.MinReplicas < 1 {
		return cfg, fmt.Errorf("min_replicas must be >= 1")
	}
	if cfg.Scaling.MaxReplicas < cfg.Scaling.MinReplicas {
		return cfg, fmt.Errorf("max_replicas must be >= min_replicas")
	}
	if cfg.Scaling.CPUHigh <= cfg.Scaling.CPULow {
		return cfg, fmt.Errorf("cpu_high must be > cpu_low")
	}

	// Normalize: convert legacy single-service format to multi-service format
	cfg.Normalize()

	return cfg, nil
}

// Normalize converts legacy single-service config to multi-service format for backward compatibility
func (c *Config) Normalize() {
	// If services array is empty but we have legacy Service/Scaling, convert it
	if len(c.Services) == 0 && c.Service != "" {
		// Convert legacy format to multi-service format
		c.Services = []ServiceConfig{
			{
				Name:          c.Service,
				MinReplicas:   c.Scaling.MinReplicas,
				MaxReplicas:   c.Scaling.MaxReplicas,
				MetricsWindow: c.Scaling.MetricsWindow,
				CheckInterval: c.Scaling.CheckInterval,
				Rules: Rules{
					ScaleUpWhen: []Condition{
						{Metric: "cpu.avg_pct", Op: ">", Value: c.Scaling.CPUHigh},
					},
					ScaleDownWhen: []Condition{
						{Metric: "cpu.avg_pct", Op: "<", Value: c.Scaling.CPULow},
					},
				},
				Queue: nil, // No queue in legacy format
			},
		}
	}
}

func parseFlags(args []string) opts {
	o := opts{}
	for _, a := range args {
		switch a {
		case "--debug":
			o.debug = true
		case "--no-install":
			o.noInstall = true
		case "--skip-compose":
			o.skipCompose = true
		case "--headless":
			o.headless = true
		}
	}
	return o
}

func parseDaemonFlags(args []string) daemonOpts {
	const (
		defaultComposeFile = "examples/docker-compose.yaml"
		defaultService     = "web"
	)

	opts := daemonOpts{
		composeFile: defaultComposeFile,
		service:     defaultService,
	}

	for idx := 0; idx < len(args); idx++ {
		arg := args[idx]
		switch arg {
		case "--manual":
			opts.manual = true
		case "--config":
			if idx+1 < len(args) {
				opts.configFile = args[idx+1]
				idx++
			}
		case "--compose-file":
			if idx+1 < len(args) {
				opts.composeFile = args[idx+1]
				idx++
			}
		case "--service":
			if idx+1 < len(args) {
				opts.service = args[idx+1]
				idx++
			}
		case "--interval":
			if idx+1 < len(args) {
				if interval, err := strconv.Atoi(args[idx+1]); err == nil && interval > 0 {
					opts.checkInterval = interval
				}
				idx++
			}
		}
	}
	return opts
}

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	switch os.Args[1] {
	case "daemon":
		if len(os.Args) < 3 {
			usage()
			return
		}
		runDaemon(os.Args[2], os.Args[3:])
	case "ai":
		if len(os.Args) < 3 || os.Args[2] != "up" {
			usage()
			return
		}
		runAIUp(os.Args[3:])
	case "config":
		if len(os.Args) < 3 {
			usage()
			return
		}
		runConfig(os.Args[2], os.Args[3:])
	case "explain":
		runExplain(os.Args[2:])
	case "mcp":
		runMCP()
	default:
		usage()
	}
}

func runDaemon(action string, args []string) {
	const (
		pidFile = "/tmp/docktor-daemon.pid"
		logFile = "/tmp/docktor-daemon.log"
	)

	switch action {
	case "start":
		daemonStart(args, pidFile, logFile)
	case "stop":
		daemonStop(pidFile)
	case "status":
		daemonStatus(pidFile, logFile)
	case "logs":
		daemonLogs(logFile)
	default:
		fmt.Fprintf(os.Stderr, "Unknown daemon action: %s\n", action)
		usage()
		os.Exit(1)
	}
}

func runConfig(action string, args []string) {
	switch action {
	case "list-models":
		configListModels()
	case "set-model":
		if len(args) < 1 {
			fmt.Fprintf(os.Stderr, "Error: model ID required\n")
			fmt.Fprintf(os.Stderr, "Usage: docktor config set-model <MODEL_ID> [--provider=dmr|openai] [--base-url=<URL>]\n")
			os.Exit(1)
		}
		configSetModel(args)
	case "validate":
		configValidate()
	default:
		fmt.Fprintf(os.Stderr, "Unknown config action: %s\n", action)
		fmt.Fprintf(os.Stderr, "Available actions: list-models, set-model, validate\n")
		os.Exit(1)
	}
}

func runExplain(args []string) {
	tail := 10
	serviceFilter := ""

	// Parse flags
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--tail=") {
			tail, _ = strconv.Atoi(strings.TrimPrefix(args[i], "--tail="))
		} else if args[i] == "--tail" && i+1 < len(args) {
			tail, _ = strconv.Atoi(args[i+1])
			i++
		} else if strings.HasPrefix(args[i], "--service=") {
			serviceFilter = strings.TrimPrefix(args[i], "--service=")
		} else if args[i] == "--service" && i+1 < len(args) {
			serviceFilter = args[i+1]
			i++
		}
	}

	// Read JSONL file
	f, err := os.Open("/tmp/docktor-decisions.jsonl")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Cannot open decision log: %v\n", err)
		fmt.Fprintf(os.Stderr, "The daemon may not have run yet or no decisions have been logged.\n")
		os.Exit(1)
	}
	defer f.Close()

	// Parse all lines
	type Decision struct {
		Timestamp       string             `json:"timestamp"`
		Service         string             `json:"service"`
		Action          string             `json:"action"`
		CurrentReplicas int                `json:"current_replicas"`
		TargetReplicas  int                `json:"target_replicas"`
		Reason          string             `json:"reason"`
		Observations    map[string]float64 `json:"observations"`
	}

	var decisions []Decision
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var d Decision
		if err := json.Unmarshal(scanner.Bytes(), &d); err == nil {
			// Filter by service if specified
			if serviceFilter == "" || d.Service == serviceFilter {
				decisions = append(decisions, d)
			}
		}
	}

	if len(decisions) == 0 {
		fmt.Println("No scaling decisions found.")
		return
	}

	// Take last N decisions
	start := 0
	if len(decisions) > tail {
		start = len(decisions) - tail
	}
	decisions = decisions[start:]

	// Print table header
	fmt.Printf("%-12s %-10s %-10s %-8s %-50s\n", "TIME", "SERVICE", "ACTION", "FROM→TO", "REASON")
	fmt.Println(strings.Repeat("-", 100))

	// Print decisions
	for _, d := range decisions {
		// Parse timestamp
		ts, _ := time.Parse(time.RFC3339, d.Timestamp)
		timeStr := ts.Format("15:04:05")

		// Format replica change
		replicaChange := fmt.Sprintf("%d→%d", d.CurrentReplicas, d.TargetReplicas)

		// Truncate reason if too long
		reason := d.Reason
		if len(reason) > 50 {
			reason = reason[:47] + "..."
		}

		fmt.Printf("%-12s %-10s %-10s %-8s %-50s\n", timeStr, d.Service, d.Action, replicaChange, reason)
	}

	fmt.Printf("\nShowing %d of %d total decisions", len(decisions), len(decisions)+start)
	if serviceFilter != "" {
		fmt.Printf(" (filtered by service: %s)", serviceFilter)
	}
	fmt.Println()
}

func runAIUp(args []string) {
	o := parseFlags(args)

	printBanner()

	repoRoot, err := os.Getwd()
	must(err)

	composeFile := filepath.Join(repoRoot, "examples", "docker-compose.yaml")
	envFile := filepath.Join(repoRoot, ".env.cagent")
	agentDMR := filepath.Join(repoRoot, "agents", "docktor.dmr.yaml")
	agentCloud := filepath.Join(repoRoot, "agents", "docktor.cloud.yaml")

	fmt.Println("▶ Using integrated MCP in Docktor binary")

	if !hasBinary("cagent") {
		if o.noInstall {
			fmt.Fprintln(os.Stderr, "cagent not found and --no-install set; please install cagent")
			os.Exit(1)
		}
		installCagent()
	}

	if !o.skipCompose {
		must(run("docker", "compose", "-f", composeFile, "down", "-v", "--remove-orphans"))
		must(run("docker", "compose", "-f", composeFile, "up", "-d", "--scale", "web=2"))
	}

	useDMR := probeURL(modelRunnerEngineURL) || probeURL(modelRunnerV1URL)

	env := os.Environ()
	env = append(env, "DOCKTOR_COMPOSE_FILE="+composeFile)

	var agentFile string
	if !fileExists(envFile) {
		if useDMR {
			content := fmt.Sprintf("OPENAI_BASE_URL=%s\nOPENAI_API_KEY=dummy\nOPENAI_MODEL=dmr/ai/llama3.2\n", modelRunnerBaseURL)
			_ = os.WriteFile(envFile, []byte(content), 0644)
			fmt.Println("▶ Using Docker Model Runner with Llama 3.2")
		} else {
			fmt.Fprintln(os.Stderr, "ERROR: No Docker Model Runner detected and no .env.cagent for Gateway/OpenAI.")
			fmt.Fprintln(os.Stderr, "Create .env.cagent with:")
			fmt.Fprintln(os.Stderr, "  OPENAI_BASE_URL=https://api.openai.com/v1 (or your gateway)")
			fmt.Fprintln(os.Stderr, "  OPENAI_API_KEY=sk-...")
			fmt.Fprintln(os.Stderr, "  OPENAI_MODEL=gpt-4")
			cleanupCompose(composeFile, !o.skipCompose)
			os.Exit(1)
		}
	}

	if useDMR {
		agentFile = agentDMR
	} else {
		agentFile = agentCloud
	}

	argsRun := []string{"run", agentFile, "--agent", "docktor", "--env-from-file", envFile}
	if o.debug {
		argsRun = append(argsRun, "--debug")
	}

	cmd := exec.Command("cagent", argsRun...)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if o.headless {
		msg := `You are an autoscaling SRE agent. Start monitoring and autoscaling the 'web' service NOW.

LOOP FOREVER (every 10 seconds):
1. Call get_metrics with container_regex="web" and window_sec=30
2. Call detect_anomalies with the metrics using cpu_high_pct=80 and cpu_low_pct=25
3. Based on the recommendation:
   - If "scale_up": call propose_scale for service="web" target_replicas=5, then call apply_scale with reason="cpu_high"
   - If "scale_down": call propose_scale for service="web" target_replicas=2, then call apply_scale with reason="cpu_low"
   - If "hold": print "No scaling needed" and the reason
4. After each iteration, print a summary message explaining:
   - Current average CPU
   - Action taken (scaled up/down/held)
   - Current replica count
   - Reason for decision

Print clear, human-readable status updates after each loop iteration.
Continue running this loop until interrupted.
Start now!
`
		cmd.Stdin = strings.NewReader(msg)
		fmt.Println(">>> Running cagent (headless autoscaling mode)")
		fmt.Println(">>> Watch MCP tool calls in the output below...")
	} else {
		cmd.Stdin = os.Stdin
		fmt.Println(">>> Running cagent with Docktor MCP ...")
	}

	err = cmd.Run()

	cleanupCompose(composeFile, !o.skipCompose)

	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(0) // Ctrl-C exit as success
		}
		os.Exit(1)
	}
}

var inMCP bool

type rpcReq struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}
type rpcRes struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}
type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeRes(id json.RawMessage, result interface{}) {
	res := rpcRes{Jsonrpc: "2.0", ID: id, Result: result}
	resJSON, _ := json.Marshal(res)
	log.Printf("→ Response: %s", string(resJSON))
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(res)
}
func writeErr(id json.RawMessage, code int, msg string) {
	res := rpcRes{Jsonrpc: "2.0", ID: id, Error: &rpcErr{Code: code, Message: msg}}
	resJSON, _ := json.Marshal(res)
	log.Printf("→ Error Response: %s", string(resJSON))
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(res)
}

type GetMetricsParams struct {
	ContainerRegex string `json:"container_regex"`
	WindowSec      int    `json:"window_sec"`
}
type DetectParams struct {
	Metrics map[string]float64 `json:"metrics"`
	Rules   struct {
		CPUHighPct float64 `json:"cpu_high_pct"`
		CPULowPct  float64 `json:"cpu_low_pct"`
	} `json:"rules"`
}
type ProposeParams struct {
	Service        string `json:"service"`
	TargetReplicas int    `json:"target_replicas"`
}
type ApplyParams struct {
	Service        string `json:"service"`
	TargetReplicas int    `json:"target_replicas"`
	Reason         string `json:"reason"`
}

func mcpInitialize(id json.RawMessage, params json.RawMessage) {
	var in struct {
		ProtocolVersion string          `json:"protocolVersion"`
		ClientInfo      json.RawMessage `json:"clientInfo"`
	}
	_ = json.Unmarshal(params, &in)
	pv := strings.TrimSpace(in.ProtocolVersion)
	if pv == "" {
		pv = "2024-06-01"
	}
	writeRes(id, map[string]interface{}{
		"protocolVersion": pv,
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    "docktor-mcp",
			"version": "0.1.0",
		},
	})
}

func mcpToolsList(id json.RawMessage) {
	type tool struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		InputSchema map[string]interface{} `json:"inputSchema"`
	}
	tools := []tool{
		{
			Name:        "get_metrics",
			Description: "Return avg CPU% over window for containers matching regex",
			InputSchema: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"container_regex": map[string]interface{}{"type": "string"},
					"window_sec":      map[string]interface{}{"type": "integer"},
				},
				"required": []string{"container_regex"},
			},
		},
		{
			Name:        "get_current_replicas",
			Description: "Get the current number of running replicas for a service from Docker Compose",
			InputSchema: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"service": map[string]interface{}{"type": "string"},
				},
				"required": []string{"service"},
			},
		},
		{
			Name:        "calculate_target_replicas",
			Description: "Calculate target replicas based on scaling recommendation and current count. Handles all arithmetic logic per config.",
			InputSchema: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"recommendation":    map[string]interface{}{"type": "string", "enum": []string{"scale_up", "scale_down", "hold"}},
					"current_replicas":  map[string]interface{}{"type": "integer"},
				},
				"required": []string{"recommendation", "current_replicas"},
			},
		},
		{
			Name:        "detect_anomalies",
			Description: "Recommend scale_up/scale_down based on CPU thresholds",
			InputSchema: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"metrics": map[string]interface{}{"type": "object"},
					"rules": map[string]interface{}{
						"type":                 "object",
						"additionalProperties": false,
						"properties": map[string]interface{}{
							"cpu_high_pct": map[string]interface{}{"type": "number"},
							"cpu_low_pct":  map[string]interface{}{"type": "number"},
						},
						"required": []string{"cpu_high_pct", "cpu_low_pct"},
					},
				},
				"required": []string{"metrics", "rules"},
			},
		},
		{
			Name:        "propose_scale",
			Description: "Echo the docker compose scaling command for validation",
			InputSchema: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"service":         map[string]interface{}{"type": "string"},
					"target_replicas": map[string]interface{}{"type": "integer"},
				},
				"required": []string{"service", "target_replicas"},
			},
		},
		{
			Name:        "apply_scale",
			Description: "Run docker compose --scale",
			InputSchema: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"service":         map[string]interface{}{"type": "string"},
					"target_replicas": map[string]interface{}{"type": "integer"},
					"reason":          map[string]interface{}{"type": "string"},
				},
				"required": []string{"service", "target_replicas"},
			},
		},
		{
			Name:        "get_queue_metrics",
			Description: "Collect queue metrics from NATS JetStream (backlog, lag, rates)",
			InputSchema: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"queue_config": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"kind":       map[string]interface{}{"type": "string"},
							"url":        map[string]interface{}{"type": "string"},
							"jetstream":  map[string]interface{}{"type": "boolean"},
							"stream":     map[string]interface{}{"type": "string"},
							"consumer":   map[string]interface{}{"type": "string"},
							"subject":    map[string]interface{}{"type": "string"},
						},
						"required": []string{"kind", "url"},
					},
					"window_sec": map[string]interface{}{"type": "integer"},
				},
				"required": []string{"queue_config", "window_sec"},
			},
		},
		{
			Name:        "decide_scale_multi",
			Description: "Evaluate multi-metric scaling rules and decide action (scale_up/scale_down/hold)",
			InputSchema: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"service_name":      map[string]interface{}{"type": "string"},
					"current_replicas":  map[string]interface{}{"type": "integer"},
					"min_replicas":      map[string]interface{}{"type": "integer"},
					"max_replicas":      map[string]interface{}{"type": "integer"},
					"rules": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"scale_up_when":   map[string]interface{}{"type": "array"},
							"scale_down_when": map[string]interface{}{"type": "array"},
						},
					},
					"observations": map[string]interface{}{"type": "object"},
				},
				"required": []string{"service_name", "current_replicas", "min_replicas", "max_replicas", "rules", "observations"},
			},
		},
	}
	writeRes(id, map[string]interface{}{
		"tools":      tools,
		"nextCursor": nil,
	})
}

func mcpToolsCall(id json.RawMessage, params json.RawMessage) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		writeErr(id, -32602, "bad params")
		return
	}

	switch p.Name {
	case "get_metrics":
		var in GetMetricsParams
		_ = json.Unmarshal(p.Arguments, &in)
		if in.WindowSec <= 0 {
			in.WindowSec = 60
		}
		log.Printf("[MCP] get_metrics(container_regex=%q, window_sec=%d)", in.ContainerRegex, in.WindowSec)
		res, err := toolGetMetrics(in.ContainerRegex, in.WindowSec)
		if err != nil {
			log.Printf("[MCP] get_metrics ERROR: %v", err)
			writeErr(id, 1, err.Error())
			return
		}
		log.Printf("[MCP] get_metrics RESULT: %v", res)
		writeRes(id, map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": toJSON(map[string]interface{}{"metrics": res})},
			},
			"isError": false,
		})
	case "get_current_replicas":
		var in struct {
			Service string `json:"service"`
		}
		_ = json.Unmarshal(p.Arguments, &in)
		log.Printf("[MCP] get_current_replicas(service=%s)", in.Service)
		count, err := toolGetCurrentReplicas(in.Service)
		if err != nil {
			log.Printf("[MCP] get_current_replicas ERROR: %v", err)
			writeErr(id, 1, err.Error())
			return
		}
		log.Printf("[MCP] get_current_replicas RESULT: %d", count)
		writeRes(id, map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": toJSON(map[string]interface{}{"current_replicas": count})},
			},
			"isError": false,
		})
	case "calculate_target_replicas":
		var in struct {
			Recommendation   string `json:"recommendation"`
			CurrentReplicas  int    `json:"current_replicas"`
		}
		_ = json.Unmarshal(p.Arguments, &in)
		log.Printf("[MCP] calculate_target_replicas(recommendation=%s, current_replicas=%d)", in.Recommendation, in.CurrentReplicas)
		result, err := toolCalculateTargetReplicas(in.Recommendation, in.CurrentReplicas)
		if err != nil {
			log.Printf("[MCP] calculate_target_replicas ERROR: %v", err)
			writeErr(id, 1, err.Error())
			return
		}
		log.Printf("[MCP] calculate_target_replicas RESULT: %v", result)
		writeRes(id, map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": toJSON(result)},
			},
			"isError": false,
		})
	case "detect_anomalies":
		var in DetectParams
		_ = json.Unmarshal(p.Arguments, &in)
		log.Printf("[MCP] detect_anomalies(metrics=%v, cpu_high=%.1f, cpu_low=%.1f)", in.Metrics, in.Rules.CPUHighPct, in.Rules.CPULowPct)
		action, reason := toolDetect(in.Metrics, in.Rules.CPUHighPct, in.Rules.CPULowPct)
		log.Printf("[MCP] detect_anomalies RESULT: action=%s, reason=%s", action, reason)
		writeRes(id, map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": toJSON(map[string]interface{}{"recommendation": action, "reason": reason})},
			},
			"isError": false,
		})
	case "propose_scale":
		var in ProposeParams
		_ = json.Unmarshal(p.Arguments, &in)
		cmd := fmt.Sprintf("docker compose -f %q up -d --scale %s=%d",
			os.Getenv("DOCKTOR_COMPOSE_FILE"), in.Service, in.TargetReplicas)
		log.Printf("[MCP] propose_scale(service=%s, target_replicas=%d) → %s", in.Service, in.TargetReplicas, cmd)
		writeRes(id, map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": toJSON(map[string]interface{}{"valid": true, "message": "proposal valid", "command": cmd})},
			},
			"isError": false,
		})
	case "apply_scale":
		var in ApplyParams
		_ = json.Unmarshal(p.Arguments, &in)
		compose := os.Getenv("DOCKTOR_COMPOSE_FILE")
		if compose == "" {
			log.Printf("[MCP] apply_scale ERROR: DOCKTOR_COMPOSE_FILE not set")
			writeErr(id, 2, "DOCKTOR_COMPOSE_FILE not set")
			return
		}
		log.Printf("[MCP] apply_scale(service=%s, target_replicas=%d, reason=%s) EXECUTING...", in.Service, in.TargetReplicas, in.Reason)
		err := run("docker", "compose", "-f", compose, "up", "-d", "--scale", fmt.Sprintf("%s=%d", in.Service, in.TargetReplicas))
		if err != nil {
			log.Printf("[MCP] apply_scale FAILED: %v", err)
			writeRes(id, map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": toJSON(map[string]interface{}{"valid": false, "message": "failed to scale: " + err.Error()})},
				},
				"isError": false,
			})
			return
		}
		log.Printf("[MCP] apply_scale SUCCESS: scaled %s to %d (reason: %s)", in.Service, in.TargetReplicas, in.Reason)
		writeRes(id, map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": toJSON(map[string]interface{}{"valid": true, "message": fmt.Sprintf("scaled %s to %d. reason: %s", in.Service, in.TargetReplicas, in.Reason)})},
			},
			"isError": false,
		})
	case "get_queue_metrics":
		var in struct {
			QueueConfig QueueConfig `json:"queue_config"`
			WindowSec   int         `json:"window_sec"`
		}
		_ = json.Unmarshal(p.Arguments, &in)
		if in.WindowSec <= 0 {
			in.WindowSec = 5
		}
		log.Printf("[MCP] get_queue_metrics(kind=%s, stream=%s, consumer=%s, window_sec=%d)",
			in.QueueConfig.Kind, in.QueueConfig.Stream, in.QueueConfig.Consumer, in.WindowSec)
		res, err := toolGetQueueMetrics(in.QueueConfig, in.WindowSec)
		if err != nil {
			log.Printf("[MCP] get_queue_metrics ERROR: %v", err)
			writeErr(id, 1, err.Error())
			return
		}
		log.Printf("[MCP] get_queue_metrics RESULT: %v", res)
		writeRes(id, map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": toJSON(res)},
			},
			"isError": false,
		})
	case "decide_scale_multi":
		var in struct {
			ServiceName     string             `json:"service_name"`
			CurrentReplicas int                `json:"current_replicas"`
			MinReplicas     int                `json:"min_replicas"`
			MaxReplicas     int                `json:"max_replicas"`
			Rules           Rules              `json:"rules"`
			Observations    map[string]float64 `json:"observations"`
		}
		_ = json.Unmarshal(p.Arguments, &in)
		log.Printf("[MCP] decide_scale_multi(service=%s, current=%d, min=%d, max=%d, observations=%v)",
			in.ServiceName, in.CurrentReplicas, in.MinReplicas, in.MaxReplicas, in.Observations)
		res, err := toolDecideScaleMulti(in.ServiceName, in.CurrentReplicas, in.MinReplicas, in.MaxReplicas, in.Rules, in.Observations)
		if err != nil {
			log.Printf("[MCP] decide_scale_multi ERROR: %v", err)
			writeErr(id, 1, err.Error())
			return
		}
		log.Printf("[MCP] decide_scale_multi RESULT: %v", res)
		writeRes(id, map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": toJSON(res)},
			},
			"isError": false,
		})
	default:
		writeErr(id, -32601, "unknown tool")
	}
}

func toJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func toolGetMetrics(containerRegex string, windowSec int) (map[string]float64, error) {
	re, err := regexp.Compile(containerRegex)
	if err != nil {
		return nil, fmt.Errorf("bad regex: %w", err)
	}

	type acc struct {
		sum float64
		n   int
	}
	agg := map[string]*acc{}

	stop := time.Now().Add(time.Duration(windowSec) * time.Second)
	for time.Now().Before(stop) {
		out, err := exec.Command("bash", "-lc",
			`docker stats --no-stream --format '{{.Name}} {{.CPUPerc}}'`).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("docker stats: %w", err)
		}

		sc := bufio.NewScanner(strings.NewReader(string(out)))
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) != 2 {
				continue
			}
			name := fields[0]
			if !re.MatchString(name) {
				continue
			}
			pctStr := strings.TrimSuffix(fields[1], "%")
			val, err := strconv.ParseFloat(pctStr, 64)
			if err != nil {
				continue
			}
			if _, ok := agg[name]; !ok {
				agg[name] = &acc{}
			}
			agg[name].sum += val
			agg[name].n++
		}
		time.Sleep(1 * time.Second)
	}

	avg := map[string]float64{}
	for k, v := range agg {
		if v.n > 0 {
			avg[k] = v.sum / float64(v.n)
		}
	}
	return avg, nil
}

func toolGetCurrentReplicas(service string) (int, error) {
	composeFile := os.Getenv("DOCKTOR_COMPOSE_FILE")
	if composeFile == "" {
		return 0, fmt.Errorf("DOCKTOR_COMPOSE_FILE not set")
	}

	// Use docker compose ps to count running containers for the service
	out, err := exec.Command("docker", "compose", "-f", composeFile, "ps", service, "--format", "{{.Name}}").CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("docker compose ps: %w", err)
	}

	// Count non-empty lines
	count := 0
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			count++
		}
	}

	return count, nil
}

func toolCalculateTargetReplicas(recommendation string, currentReplicas int) (map[string]interface{}, error) {
	// Get config values from environment
	minReplicas, _ := strconv.Atoi(os.Getenv("DOCKTOR_MIN_REPLICAS"))
	maxReplicas, _ := strconv.Atoi(os.Getenv("DOCKTOR_MAX_REPLICAS"))
	scaleUpBy, _ := strconv.Atoi(os.Getenv("DOCKTOR_SCALE_UP_BY"))
	scaleDownBy, _ := strconv.Atoi(os.Getenv("DOCKTOR_SCALE_DOWN_BY"))

	var targetReplicas int
	var action string
	var shouldScale bool

	switch recommendation {
	case "scale_up":
		if currentReplicas >= maxReplicas {
			action = "hold"
			shouldScale = false
			targetReplicas = currentReplicas
		} else {
			targetReplicas = currentReplicas + scaleUpBy
			if targetReplicas > maxReplicas {
				targetReplicas = maxReplicas
			}
			action = "scale_up"
			shouldScale = true
		}
	case "scale_down":
		if currentReplicas <= minReplicas {
			action = "hold"
			shouldScale = false
			targetReplicas = currentReplicas
		} else {
			targetReplicas = currentReplicas - scaleDownBy
			if targetReplicas < minReplicas {
				targetReplicas = minReplicas
			}
			action = "scale_down"
			shouldScale = true
		}
	case "hold":
		action = "hold"
		shouldScale = false
		targetReplicas = currentReplicas
	default:
		return nil, fmt.Errorf("unknown recommendation: %s", recommendation)
	}

	return map[string]interface{}{
		"action":          action,
		"should_scale":    shouldScale,
		"target_replicas": targetReplicas,
		"current_replicas": currentReplicas,
	}, nil
}

func toolDetect(metrics map[string]float64, hi, lo float64) (string, string) {
	if len(metrics) == 0 {
		return "hold", "no metrics"
	}
	var sum float64
	for _, v := range metrics {
		sum += v
	}
	avg := sum / float64(len(metrics))

	switch {
	case avg >= hi:
		return "scale_up", fmt.Sprintf("avg_cpu %.1f >= %.1f", avg, hi)
	case avg <= lo:
		return "scale_down", fmt.Sprintf("avg_cpu %.1f <= %.1f", avg, lo)
	default:
		return "hold", fmt.Sprintf("avg_cpu %.1f within [%.1f, %.1f]", avg, lo, hi)
	}
}

// toolGetQueueMetrics collects queue metrics using the queue plugin architecture
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
		return nil, fmt.Errorf("failed to create queue provider: %w", err)
	}
	defer provider.Close()

	// Connect
	if err := provider.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to queue: %w", err)
	}

	// Get metrics
	metrics, err := provider.GetMetrics(windowSec)
	if err != nil {
		return nil, fmt.Errorf("failed to get queue metrics: %w", err)
	}

	// Convert to map[string]float64 for MCP
	result := map[string]float64{
		"queue.backlog":  metrics.Backlog,
		"queue.lag":      metrics.Lag,
		"queue.rate_in":  metrics.RateIn,
		"queue.rate_out": metrics.RateOut,
	}

	// Add custom metrics with queue prefix
	for k, v := range metrics.Custom {
		result["queue."+k] = v
	}

	return result, nil
}

// toolDecideScaleMulti evaluates multi-metric rules and decides scaling action
func toolDecideScaleMulti(serviceName string, currentReplicas, minReplicas, maxReplicas int, rules Rules, observations map[string]float64) (map[string]interface{}, error) {
	// Helper to evaluate a single condition
	evaluateCondition := func(cond Condition) bool {
		value, exists := observations[cond.Metric]
		if !exists {
			return false // Metric not available
		}

		switch cond.Op {
		case ">":
			return value > cond.Value
		case ">=":
			return value >= cond.Value
		case "<":
			return value < cond.Value
		case "<=":
			return value <= cond.Value
		case "==":
			return value == cond.Value
		case "!=":
			return value != cond.Value
		default:
			return false
		}
	}

	// Evaluate scale_up_when (OR logic - any condition matches)
	scaleUpMatches := []string{}
	for _, cond := range rules.ScaleUpWhen {
		if evaluateCondition(cond) {
			val := observations[cond.Metric]
			scaleUpMatches = append(scaleUpMatches, fmt.Sprintf("%s %.1f %s %.1f", cond.Metric, val, cond.Op, cond.Value))
		}
	}

	// Evaluate scale_down_when (AND logic - all conditions must match)
	scaleDownMatches := []string{}
	allScaleDownMatch := len(rules.ScaleDownWhen) > 0
	for _, cond := range rules.ScaleDownWhen {
		if evaluateCondition(cond) {
			val := observations[cond.Metric]
			scaleDownMatches = append(scaleDownMatches, fmt.Sprintf("%s %.1f %s %.1f", cond.Metric, val, cond.Op, cond.Value))
		} else {
			allScaleDownMatch = false
		}
	}

	// Decide action
	var action string
	var targetReplicas int
	var reason string
	var matchedRules []string

	if len(scaleUpMatches) > 0 {
		// Scale up: ANY condition matched
		action = "scale_up"
		targetReplicas = currentReplicas + 2 // TODO: make configurable via scale_up_by
		if targetReplicas > maxReplicas {
			targetReplicas = maxReplicas
		}
		reason = strings.Join(scaleUpMatches, " OR ")
		matchedRules = scaleUpMatches
	} else if allScaleDownMatch {
		// Scale down: ALL conditions matched
		action = "scale_down"
		targetReplicas = currentReplicas - 1 // TODO: make configurable via scale_down_by
		if targetReplicas < minReplicas {
			targetReplicas = minReplicas
		}
		reason = strings.Join(scaleDownMatches, " AND ")
		matchedRules = scaleDownMatches
	} else {
		// Hold
		action = "hold"
		targetReplicas = currentReplicas
		reason = "no scaling conditions met"
		matchedRules = []string{}
	}

	// Enforce bounds and convert to hold if no change
	if targetReplicas == currentReplicas {
		action = "hold"
		if len(matchedRules) > 0 {
			reason = fmt.Sprintf("already at target replicas (%d)", currentReplicas)
		}
	}

	return map[string]interface{}{
		"action":          action,
		"target_replicas": targetReplicas,
		"current_replicas": currentReplicas,
		"reason":          reason,
		"policy":          "multi-metric evaluation",
		"matched_rules":   matchedRules,
	}, nil
}

func runMCP() {
	inMCP = true
	log.SetOutput(os.Stderr)

	dec := json.NewDecoder(os.Stdin)
	for {
		var req rpcReq
		if err := dec.Decode(&req); err != nil {
			log.Printf("ERROR decoding request: %v", err)
			return
		}

		reqJSON, _ := json.Marshal(req)
		log.Printf("← %s (full request: %s)", req.Method, string(reqJSON))

		switch req.Method {
		case "initialize":
			mcpInitialize(req.ID, req.Params)
		case "notifications/initialized":
			log.Printf("DEBUG: Client initialized notification received")
		case "tools/list":
			log.Printf("DEBUG: tools/list params=%s", string(req.Params))
			mcpToolsList(req.ID)
		case "tools/call":
			mcpToolsCall(req.ID, req.Params)
		default:
			log.Printf("WARN: Unknown method: %s", req.Method)
			if len(req.ID) > 0 {
				writeErr(req.ID, -32601, "unknown method: "+req.Method)
			}
		}
	}
}

// generateAgentConfig creates a runtime agent config with substituted config values
func generateAgentConfig(sourceFile, targetFile string, cfg Config) error {
	// Read source agent file
	data, err := os.ReadFile(sourceFile)
	if err != nil {
		return fmt.Errorf("failed to read agent file: %w", err)
	}

	// Parse YAML
	var agentYAML map[string]interface{}
	if err := yaml.Unmarshal(data, &agentYAML); err != nil {
		return fmt.Errorf("failed to parse agent YAML: %w", err)
	}

	// Navigate to docktor agent instruction
	agents, ok := agentYAML["agents"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid agent file: missing agents section")
	}

	docktor, ok := agents["docktor"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid agent file: missing docktor agent")
	}

	instruction, ok := docktor["instruction"].(string)
	if !ok {
		return fmt.Errorf("invalid agent file: missing instruction")
	}

	// Substitute config values into instruction
	instruction = strings.ReplaceAll(instruction, "$DOCKTOR_SERVICE", cfg.Service)
	instruction = strings.ReplaceAll(instruction, "$DOCKTOR_METRICS_WINDOW", fmt.Sprintf("%d", cfg.Scaling.MetricsWindow))
	instruction = strings.ReplaceAll(instruction, "$DOCKTOR_CPU_HIGH", fmt.Sprintf("%.0f", cfg.Scaling.CPUHigh))
	instruction = strings.ReplaceAll(instruction, "$DOCKTOR_CPU_LOW", fmt.Sprintf("%.0f", cfg.Scaling.CPULow))
	instruction = strings.ReplaceAll(instruction, "$DOCKTOR_MIN_REPLICAS", fmt.Sprintf("%d", cfg.Scaling.MinReplicas))
	instruction = strings.ReplaceAll(instruction, "$DOCKTOR_MAX_REPLICAS", fmt.Sprintf("%d", cfg.Scaling.MaxReplicas))
	instruction = strings.ReplaceAll(instruction, "$DOCKTOR_SCALE_UP_BY", fmt.Sprintf("%d", cfg.Scaling.ScaleUpBy))
	instruction = strings.ReplaceAll(instruction, "$DOCKTOR_SCALE_DOWN_BY", fmt.Sprintf("%d", cfg.Scaling.ScaleDownBy))
	instruction = strings.ReplaceAll(instruction, "$DOCKTOR_LLM_PROVIDER", cfg.LLM.Provider)
	instruction = strings.ReplaceAll(instruction, "$DOCKTOR_LLM_MODEL", cfg.LLM.Model)

	docktor["instruction"] = instruction

	// Write modified config
	output, err := yaml.Marshal(agentYAML)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	if err := os.WriteFile(targetFile, output, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// monitorService runs the scaling loop for a single service
func monitorService(svc ServiceConfig, logFh *os.File, composeFile string) {
	checkInterval := time.Duration(svc.CheckInterval) * time.Second
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	log.Printf("[%s] Monitor started (interval=%ds, replicas=%d-%d)\n",
		svc.Name, svc.CheckInterval, svc.MinReplicas, svc.MaxReplicas)

	iteration := 0
	for range ticker.C {
		iteration++
		runScalingIteration(svc, iteration, logFh, composeFile)
	}
}

// runScalingIteration performs one scaling check for a service
func runScalingIteration(svc ServiceConfig, iteration int, logFh *os.File, composeFile string) {
	timestamp := time.Now()
	fmt.Fprintf(logFh, "\n=== [%s] Iteration %d (%s) ===\n", svc.Name, iteration, timestamp.Format("15:04:05"))
	logFh.Sync()

	// 1. Get current replica count
	currentReplicas, err := toolGetCurrentReplicas(svc.Name)
	if err != nil {
		fmt.Fprintf(logFh, "[%s] ERROR: Failed to get current replicas: %v\n", svc.Name, err)
		return
	}
	fmt.Fprintf(logFh, "[%s] Current replicas: %d\n", svc.Name, currentReplicas)

	// 2. Get CPU metrics
	cpuMetrics, err := toolGetMetrics(svc.Name, svc.MetricsWindow)
	if err != nil {
		fmt.Fprintf(logFh, "[%s] ERROR: Failed to get CPU metrics: %v\n", svc.Name, err)
		return
	}

	// 3. Merge all observations (start with CPU metrics)
	observations := make(map[string]float64)
	for k, v := range cpuMetrics {
		observations[k] = v
	}

	// 4. Get queue metrics if configured
	if svc.Queue != nil {
		queueMetrics, err := toolGetQueueMetrics(*svc.Queue, svc.MetricsWindow)
		if err != nil {
			fmt.Fprintf(logFh, "[%s] WARNING: Failed to get queue metrics: %v\n", svc.Name, err)
		} else {
			for k, v := range queueMetrics {
				observations[k] = v
			}
		}
	}

	fmt.Fprintf(logFh, "[%s] Observations: %v\n", svc.Name, observations)

	// 5. Decide scaling action
	decision, err := toolDecideScaleMulti(svc.Name, currentReplicas, svc.MinReplicas, svc.MaxReplicas, svc.Rules, observations)
	if err != nil {
		fmt.Fprintf(logFh, "[%s] ERROR: Failed to decide scaling: %v\n", svc.Name, err)
		return
	}

	action := decision["action"].(string)
	targetReplicas := int(decision["target_replicas"].(float64))
	reason := decision["reason"].(string)

	fmt.Fprintf(logFh, "[%s] Decision: %s (current=%d, target=%d, reason=%s)\n",
		svc.Name, action, currentReplicas, targetReplicas, reason)

	// 6. Execute scaling if needed
	if action != "hold" {
		fmt.Fprintf(logFh, "[%s] Executing: docker compose -f %s up -d --scale %s=%d\n",
			svc.Name, composeFile, svc.Name, targetReplicas)

		err := run("docker", "compose", "-f", composeFile, "up", "-d", "--scale", fmt.Sprintf("%s=%d", svc.Name, targetReplicas))
		if err != nil {
			fmt.Fprintf(logFh, "[%s] ERROR: Scaling failed: %v\n", svc.Name, err)
		} else {
			fmt.Fprintf(logFh, "[%s] ✓ Scaled successfully to %d replicas\n", svc.Name, targetReplicas)
		}
	}

	// 7. Log decision to JSONL file
	logDecisionJSONL(svc.Name, timestamp, action, currentReplicas, targetReplicas, reason, observations, decision)

	logFh.Sync()
}

// logDecisionJSONL appends a decision record to /tmp/docktor-decisions.jsonl
func logDecisionJSONL(service string, timestamp time.Time, action string, currentReplicas, targetReplicas int, reason string, observations map[string]float64, decision map[string]interface{}) {
	entry := map[string]interface{}{
		"timestamp":        timestamp.Format(time.RFC3339),
		"service":          service,
		"action":           action,
		"current_replicas": currentReplicas,
		"target_replicas":  targetReplicas,
		"reason":           reason,
		"observations":     observations,
		"matched_rules":    decision["matched_rules"],
	}

	f, err := os.OpenFile("/tmp/docktor-decisions.jsonl", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("ERROR: Failed to open decisions log: %v", err)
		return
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(entry); err != nil {
		log.Printf("ERROR: Failed to write decision log: %v", err)
	}
}

func daemonStart(args []string, pidFile, logFile string) {
	opts := parseDaemonFlags(args)

	// Check if daemon is already running
	if pidData, err := os.ReadFile(pidFile); err == nil {
		pid := strings.TrimSpace(string(pidData))
		if checkProcess(pid) {
			fmt.Fprintf(os.Stderr, "Daemon already running (PID %s)\n", pid)
			os.Exit(1)
		}
	}

	repoRoot, err := os.Getwd()
	must(err)

	// Load configuration
	cfg, err := LoadConfig(opts.configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Normalize config to multi-service format
	cfg.Normalize()

	// Track which config was loaded for logging
	configSource := "built-in defaults"
	if opts.configFile != "" {
		configSource = opts.configFile
	} else if _, err := os.Stat("docktor.yaml"); err == nil {
		configSource = "docktor.yaml (auto-discovered)"
	} else if _, err := os.Stat("docktor.yml"); err == nil {
		configSource = "docktor.yml (auto-discovered)"
	}

	// Command-line flags override config
	if opts.composeFile != "examples/docker-compose.yaml" {
		cfg.ComposeFile = opts.composeFile
	}
	if opts.service != "web" {
		// Override first service name for backward compatibility
		if len(cfg.Services) > 0 {
			cfg.Services[0].Name = opts.service
		}
	}
	if opts.checkInterval > 0 {
		// Override check interval for all services
		for i := range cfg.Services {
			cfg.Services[i].CheckInterval = opts.checkInterval
		}
	}

	// Resolve compose file path (support both relative and absolute paths)
	composeFile := cfg.ComposeFile
	if !filepath.IsAbs(composeFile) {
		composeFile = filepath.Join(repoRoot, composeFile)
	}

	if !fileExists(composeFile) {
		fmt.Fprintf(os.Stderr, "Error: Compose file not found: %s\n", composeFile)
		os.Exit(1)
	}

	envFile := filepath.Join(repoRoot, ".env.cagent")
	agentDMR := filepath.Join(repoRoot, "agents", "docktor.dmr.yaml")
	agentCloud := filepath.Join(repoRoot, "agents", "docktor.cloud.yaml")

	printBanner()

	// Check for cagent
	if !hasBinary("cagent") {
		fmt.Fprintln(os.Stderr, "Error: cagent not found. Install with: brew install cagent")
		os.Exit(1)
	}

	// Start compose stack with configured min_replicas for all services
	fmt.Printf("Starting Docker Compose stack (%s)...\n", composeFile)
	scaleArgs := []string{"compose", "-f", composeFile, "up", "-d"}
	for _, svc := range cfg.Services {
		scaleArgs = append(scaleArgs, "--scale", fmt.Sprintf("%s=%d", svc.Name, svc.MinReplicas))
	}
	must(run("docker", scaleArgs...))

	// Configure LLM based on config
	var agentFile string
	apiKey := ""

	switch cfg.LLM.Provider {
	case "dmr":
		// Docker Model Runner - use DMR agent with dummy API key
		agentFile = agentDMR
		apiKey = "dummy"

		// Verify DMR is reachable
		if !probeURL(cfg.LLM.BaseURL + "/models") {
			fmt.Fprintf(os.Stderr, "\n❌ Error: Cannot connect to Docker Model Runner at %s\n\n", cfg.LLM.BaseURL)
			fmt.Fprintln(os.Stderr, "Please ensure:")
			fmt.Fprintln(os.Stderr, "  1. Docker Desktop is running")
			fmt.Fprintln(os.Stderr, "  2. Model Runner is enabled (Settings → Features in development)")
			fmt.Fprintln(os.Stderr, "  3. At least one model is pulled\n")
			_ = run("docker", "compose", "-f", composeFile, "down")
			os.Exit(1)
		}

		fmt.Printf("▶ Using Docker Model Runner: %s\n", cfg.LLM.Model)

	case "openai":
		// OpenAI-compatible provider - use cloud agent
		agentFile = agentCloud

		// Check for API key
		apiKey = os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			fmt.Fprintln(os.Stderr, "\n❌ Error: OPENAI_API_KEY environment variable not set\n")
			fmt.Fprintln(os.Stderr, "For OpenAI provider, you must set:")
			fmt.Fprintln(os.Stderr, "  export OPENAI_API_KEY=sk-...")
			fmt.Fprintln(os.Stderr, "\nOr use Docker Model Runner:")
			fmt.Fprintln(os.Stderr, "  docktor config set-model <MODEL> --provider=dmr\n")
			_ = run("docker", "compose", "-f", composeFile, "down")
			os.Exit(1)
		}

		fmt.Printf("▶ Using OpenAI-compatible provider: %s\n", cfg.LLM.Model)

	default:
		fmt.Fprintf(os.Stderr, "Error: Unknown LLM provider '%s' (must be 'dmr' or 'openai')\n", cfg.LLM.Provider)
		os.Exit(1)
	}

	// Write .env.cagent with LLM config
	envContent := fmt.Sprintf("OPENAI_BASE_URL=%s\nOPENAI_API_KEY=%s\nOPENAI_MODEL=%s\n",
		cfg.LLM.BaseURL, apiKey, cfg.LLM.Model)
	if err := os.WriteFile(envFile, []byte(envContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing .env.cagent: %v\n", err)
		os.Exit(1)
	}

	// Generate runtime agent config with substituted values
	runtimeAgentFile := filepath.Join(repoRoot, ".docktor-agent-runtime.yaml")
	if err := generateAgentConfig(agentFile, runtimeAgentFile, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating agent config: %v\n", err)
		os.Exit(1)
	}
	agentFile = runtimeAgentFile

	fmt.Println("\n=== Starting Docktor Daemon ===")
	fmt.Printf("Mode: %s\n", map[bool]string{true: "MANUAL", false: "AUTONOMOUS"}[opts.manual])
	fmt.Printf("Config: %s\n", configSource)
	fmt.Printf("Compose: %s\n", composeFile)
	fmt.Printf("Agent: %s\n", filepath.Base(agentFile))
	fmt.Printf("Log: %s\n", logFile)
	fmt.Printf("\nLLM Config:\n")
	fmt.Printf("  Provider: %s\n", cfg.LLM.Provider)
	fmt.Printf("  Model: %s\n", cfg.LLM.Model)
	fmt.Printf("\nServices (%d):\n", len(cfg.Services))
	for _, svc := range cfg.Services {
		fmt.Printf("  • %s: replicas=%d-%d, interval=%ds",
			svc.Name, svc.MinReplicas, svc.MaxReplicas, svc.CheckInterval)
		if svc.Queue != nil {
			fmt.Printf(", queue=%s", svc.Queue.Kind)
		}
		fmt.Println()
	}
	fmt.Println()

	// Create log file
	logFh, err := os.Create(logFile)
	must(err)

	// Set global DOCKTOR_COMPOSE_FILE for MCP tools
	os.Setenv("DOCKTOR_COMPOSE_FILE", composeFile)

	// Write PID file
	must(os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644))

	fmt.Printf("✓ Daemon started successfully\n")
	fmt.Printf("  PID: %d\n", os.Getpid())
	fmt.Printf("  Logs: tail -f %s\n\n", logFile)

	// Start multi-service monitoring
	for _, svc := range cfg.Services {
		go monitorService(svc, logFh, composeFile)
	}

	fmt.Printf("Control:\n")
	fmt.Printf("  docktor daemon status  # Check status\n")
	fmt.Printf("  docktor daemon logs    # Follow logs\n")
	fmt.Printf("  docktor daemon stop    # Stop daemon\n\n")

	// Block forever - the service monitors run in background
	select {}
}

func daemonStop(pidFile string) {
	pidData, err := os.ReadFile(pidFile)
	if err != nil {
		fmt.Println("No daemon running (PID file not found)")
		return
	}

	pid := strings.TrimSpace(string(pidData))
	if !checkProcess(pid) {
		fmt.Println("Daemon not running (stale PID file)")
		os.Remove(pidFile)
		return
	}

	fmt.Printf("Stopping daemon (PID %s)...\n", pid)
	cmd := exec.Command("kill", pid)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to stop daemon: %v\n", err)
		os.Exit(1)
	}

	// Wait for process to exit
	for i := 0; i < 30; i++ {
		if !checkProcess(pid) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Force kill if still running
	if checkProcess(pid) {
		fmt.Println("Daemon didn't stop gracefully, force killing...")
		exec.Command("kill", "-9", pid).Run()
		time.Sleep(500 * time.Millisecond)
	}

	os.Remove(pidFile)
	fmt.Println("✓ Daemon stopped")
}

func daemonStatus(pidFile, logFile string) {
	pidData, err := os.ReadFile(pidFile)
	if err != nil {
		fmt.Println("Status: NOT RUNNING")
		return
	}

	pid := strings.TrimSpace(string(pidData))
	if !checkProcess(pid) {
		fmt.Println("Status: NOT RUNNING (stale PID file)")
		return
	}

	fmt.Printf("Status: RUNNING\n")
	fmt.Printf("  PID: %s\n", pid)
	fmt.Printf("  Log: %s\n", logFile)
	fmt.Println("\nRecent log entries:")
	exec.Command("tail", "-20", logFile).Run()
}

func daemonLogs(logFile string) {
	if !fileExists(logFile) {
		fmt.Fprintf(os.Stderr, "Log file not found: %s\n", logFile)
		os.Exit(1)
	}
	cmd := exec.Command("tail", "-f", logFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	must(cmd.Run())
}

func checkProcess(pid string) bool {
	cmd := exec.Command("kill", "-0", pid)
	return cmd.Run() == nil
}

func run(name string, args ...string) error {
	line := fmt.Sprintf("▶ %s %s\n", name, strings.Join(args, " "))
	if inMCP {
		os.Stderr.WriteString(line)
	} else {
		os.Stdout.WriteString(line)
	}
	cmd := exec.Command(name, args...)
	if inMCP {
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func must(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func cleanupCompose(composeFile string, do bool) {
	if do {
		_ = run("docker", "compose", "-f", composeFile, "down", "-v", "--remove-orphans")
	}
}

func probeURL(u string) bool {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	req, _ := http.NewRequest("GET", u, nil)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	return !fi.IsDir()
}

func installCagent() {
	fmt.Println("cagent not found. Attempting 'brew install cagent'...")
	_ = run("brew", "install", "cagent")
}

// configListModels lists available models from DMR
func configListModels() {
	cfg, _ := LoadConfig("")

	fmt.Println("🔍 Discovering models from Docker Model Runner...")
	fmt.Printf("   Base URL: %s\n\n", cfg.LLM.BaseURL)

	// Try to fetch models from DMR
	models, err := fetchDMRModels(cfg.LLM.BaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Unable to connect to Docker Model Runner\n\n")
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		fmt.Fprintf(os.Stderr, "Please ensure:\n")
		fmt.Fprintf(os.Stderr, "  1. Docker Desktop is running\n")
		fmt.Fprintf(os.Stderr, "  2. Model Runner is enabled in Docker Desktop settings\n")
		fmt.Fprintf(os.Stderr, "  3. At least one model is pulled/running\n\n")
		fmt.Fprintf(os.Stderr, "To enable Model Runner:\n")
		fmt.Fprintf(os.Stderr, "  → Open Docker Desktop\n")
		fmt.Fprintf(os.Stderr, "  → Go to Settings → Features in development\n")
		fmt.Fprintf(os.Stderr, "  → Enable 'Docker Model Runner'\n")
		os.Exit(1)
	}

	if len(models) == 0 {
		fmt.Println("⚠️  No models found")
		fmt.Println("\nTip: Pull a model first, e.g.:")
		fmt.Println("  docker model pull granite-4.0-1b")
		return
	}

	fmt.Printf("✓ Found %d model(s):\n\n", len(models))
	for _, model := range models {
		if model == cfg.LLM.Model {
			fmt.Printf("  • %s [currently selected]\n", model)
		} else {
			fmt.Printf("  • %s\n", model)
		}
	}

	fmt.Println("\nTo select a model:")
	fmt.Println("  docktor config set-model <MODEL_ID>")
}

// configSetModel updates the model in docktor.yaml
func configSetModel(args []string) {
	modelID := args[0]

	// Parse optional flags
	provider := ""
	baseURL := ""

	for _, arg := range args[1:] {
		if strings.HasPrefix(arg, "--provider=") {
			provider = strings.TrimPrefix(arg, "--provider=")
		} else if strings.HasPrefix(arg, "--base-url=") {
			baseURL = strings.TrimPrefix(arg, "--base-url=")
		}
	}

	// Load or create config
	configPath := "docktor.yaml"
	cfg, err := LoadConfig(configPath)
	if err != nil {
		cfg = DefaultConfig()
	}

	// Update LLM config
	if provider != "" {
		cfg.LLM.Provider = provider
	}
	if baseURL != "" {
		cfg.LLM.BaseURL = baseURL
	}
	cfg.LLM.Model = modelID

	// Validate provider
	if cfg.LLM.Provider != "dmr" && cfg.LLM.Provider != "openai" {
		fmt.Fprintf(os.Stderr, "Error: provider must be 'dmr' or 'openai', got '%s'\n", cfg.LLM.Provider)
		os.Exit(1)
	}

	// Save config
	if err := SaveConfig(configPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✓ Model configuration updated")
	fmt.Println()
	fmt.Printf("  Provider:  %s\n", cfg.LLM.Provider)
	fmt.Printf("  Base URL:  %s\n", cfg.LLM.BaseURL)
	fmt.Printf("  Model:     %s\n", cfg.LLM.Model)
	fmt.Println()
	fmt.Printf("Saved to: %s\n", configPath)
}

func configValidate() {
	// Load configuration
	cfg, err := LoadConfig("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Error loading config: %v\n", err)
		os.Exit(1)
	}

	cfg.Normalize()

	fmt.Println("Validating Docktor configuration...\n")

	allValid := true

	// 1. Check compose file exists
	composeFile := cfg.ComposeFile
	if !filepath.IsAbs(composeFile) {
		wd, _ := os.Getwd()
		composeFile = filepath.Join(wd, composeFile)
	}

	if fileExists(composeFile) {
		fmt.Printf("✓ Compose file exists: %s\n", composeFile)
	} else {
		fmt.Printf("✗ Compose file not found: %s\n", composeFile)
		allValid = false
	}

	// 2. Check each service
	for _, svc := range cfg.Services {
		fmt.Printf("\n[Service: %s]\n", svc.Name)

		// Check service exists in compose file (basic check - just grep for service name)
		if fileExists(composeFile) {
			content, _ := os.ReadFile(composeFile)
			if strings.Contains(string(content), svc.Name+":") {
				fmt.Printf("  ✓ Service '%s' found in compose file\n", svc.Name)
			} else {
				fmt.Printf("  ✗ Service '%s' not found in compose file\n", svc.Name)
				allValid = false
			}
		}

		// Check replica bounds
		if svc.MinReplicas > 0 && svc.MaxReplicas >= svc.MinReplicas {
			fmt.Printf("  ✓ Replica bounds valid: %d-%d\n", svc.MinReplicas, svc.MaxReplicas)
		} else {
			fmt.Printf("  ✗ Invalid replica bounds: min=%d, max=%d\n", svc.MinReplicas, svc.MaxReplicas)
			allValid = false
		}

		// Check queue configuration if present
		if svc.Queue != nil {
			fmt.Printf("  [Queue: %s]\n", svc.Queue.Kind)

			// Try to connect to queue
			queueCfg := queue.Config{
				Kind: svc.Queue.Kind,
				URL:  svc.Queue.URL,
				Attributes: map[string]string{
					"stream":    svc.Queue.Stream,
					"consumer":  svc.Queue.Consumer,
					"subject":   svc.Queue.Subject,
					"jetstream": fmt.Sprintf("%t", svc.Queue.JetStream),
				},
			}

			provider, err := queue.NewProvider(queueCfg)
			if err != nil {
				fmt.Printf("    ✗ Queue provider error: %v\n", err)
				allValid = false
				continue
			}

			if err := provider.Connect(); err != nil {
				fmt.Printf("    ✗ Cannot connect to queue: %v\n", err)
				allValid = false
			} else {
				fmt.Printf("    ✓ Queue reachable: %s\n", svc.Queue.URL)

				// Try to get metrics
				metrics, err := provider.GetMetrics(5)
				if err != nil {
					fmt.Printf("    ✗ Cannot get queue metrics: %v\n", err)
					allValid = false
				} else {
					fmt.Printf("    ✓ Stream '%s' accessible\n", svc.Queue.Stream)
					fmt.Printf("    ✓ Consumer '%s' accessible (backlog: %.0f)\n", svc.Queue.Consumer, metrics.Backlog)
				}
			}

			provider.Close()
		}

		// Check rules configuration
		if len(svc.Rules.ScaleUpWhen) > 0 {
			fmt.Printf("  ✓ Scale-up rules: %d conditions (OR logic)\n", len(svc.Rules.ScaleUpWhen))
		}
		if len(svc.Rules.ScaleDownWhen) > 0 {
			fmt.Printf("  ✓ Scale-down rules: %d conditions (AND logic)\n", len(svc.Rules.ScaleDownWhen))
		}
	}

	fmt.Println()
	if allValid {
		fmt.Println("✓ All checks passed!")
	} else {
		fmt.Println("✗ Some checks failed. Please review the errors above.")
		os.Exit(1)
	}
}

// fetchDMRModels fetches available models from Docker Model Runner
func fetchDMRModels(baseURL string) ([]string, error) {
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(baseURL + "/models")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("DMR returned status %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	models := make([]string, len(result.Data))
	for i, m := range result.Data {
		models[i] = m.ID
	}

	return models, nil
}

// SaveConfig saves configuration to YAML file
func SaveConfig(path string, cfg Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}
