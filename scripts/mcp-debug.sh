#!/usr/bin/env bash
# MCP wrapper that logs everything to a separate file for debugging

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG="/tmp/docktor-mcp-debug.log"

echo "=== MCP Server Started $(date) ===" >> "$LOG" 2>&1

# Run docktor mcp and tee all stderr to the debug log
"$ROOT/docktor" mcp 2>> "$LOG"
