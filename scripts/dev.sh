#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

git pull

cd ..
make build

podman compose --env-file .env -f deploy/docker-compose.yml up -d --build
echo "done — remember: source .env in your shell before running backtest"