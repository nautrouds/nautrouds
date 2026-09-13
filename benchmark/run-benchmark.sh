#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if ! command -v docker >/dev/null 2>&1; then
	echo "docker not found; run this script in an environment with Docker." >&2
	exit 1
fi

if ! command -v go >/dev/null 2>&1; then
	echo "go not found; benchmark/orchestrator needs a local Go toolchain to run." >&2
	exit 1
fi

cd "$SCRIPT_DIR"
exec go run ./orchestrator "$@"
