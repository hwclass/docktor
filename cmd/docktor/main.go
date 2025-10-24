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
  docktor ai up [--debug] [--no-install] [--skip-compose] [--headless]

Commands:
  ai up     Launch AI autoscaling agent
            --debug: Enable verbose logging
            --headless: Run autoscaling loop without TUI
            --no-install: Skip automatic cagent installation
            --skip-compose: Skip docker compose up/down (agent monitors existing containers)

Examples:
  # Full lifecycle: start containers + run agent + cleanup on exit
  docktor ai up

  # Monitor existing containers (no lifecycle management)
  docker compose -f examples/docker-compose.yaml up -d --scale web=2
  docktor ai up --skip-compose

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

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	switch os.Args[1] {
	case "ai":
		if len(os.Args) < 3 || os.Args[2] != "up" {
			usage()
			return
		}
		runAIUp(os.Args[3:])
	case "mcp":
		runMCP()
	default:
		usage()
	}
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
