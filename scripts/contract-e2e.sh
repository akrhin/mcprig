#!/usr/bin/env bash
# Contract gate: our manager against a live reference or our own product.
#   E2E_BASE=http://127.0.0.1:9091 scripts/contract-e2e.sh   # TS reference
#   E2E_BASE=http://127.0.0.1:9091 scripts/contract-e2e.sh   # mcprig panel
# E2E_STRICT=0 downgrades the reference's known defects to log lines.
set -euo pipefail
cd "$(dirname "$0")/.."

base="${E2E_BASE:-http://127.0.0.1:9091}"
echo "contract e2e against $base"
E2E_BASE="$base" E2E_STRICT="${E2E_STRICT:-1}" go test ./e2e -run 'TestContract' -count=1 -v "$@"
