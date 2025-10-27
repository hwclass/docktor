#!/usr/bin/env bash
# Wrapper to launch Docktor MCP server from agents/ directory
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
exec "$ROOT/docktor" mcp
