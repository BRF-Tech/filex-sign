#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export GOTOOLCHAIN=auto
gofmt -l . && go vet ./... && go test ./... "$@"
