#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
# The compiler go.mod pins (`toolchain goX.Y.Z`), as in scripts/build.sh.
GOTOOLCHAIN="$(sed -n 's/^toolchain //p' go.mod)"
export GOTOOLCHAIN="${GOTOOLCHAIN:?go.mod has no toolchain line}"
gofmt -l . && go vet ./... && go test ./... "$@"
