#!/usr/bin/env bash
# DEPRECATED: This script is kept for backward compatibility
# Please use: ./docktor daemon <start|stop|status|logs> [options]

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Show deprecation warning
echo "⚠️  WARNING: scripts/daemon.sh is deprecated" >&2
echo "   Please use: ./docktor daemon <start|stop|status|logs> [options]" >&2
echo "   Example: ./docktor daemon start --manual" >&2
echo "" >&2

# Forward to the new command
"$ROOT/docktor" daemon "$@"
