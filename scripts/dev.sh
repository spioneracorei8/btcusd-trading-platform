#!/usr/bin/env bash
#
# Pull, rebuild the binaries, and restart the stack.
set -euo pipefail

cd "$(dirname "$0")/.."

git pull
make build
make up

echo
echo "done. The backtest CLI reads .env itself — no need to source it first."
