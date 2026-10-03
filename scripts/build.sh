#!/usr/bin/env bash
# Builds plugin.wasm, prints its sha256 and size, and refuses a module
# over the size gate (filex's default FILEX_APP_PLUGIN_MAX_WASM_MB is 64;
# we keep well under it so installs stay quick).
#
#   bash scripts/build.sh            → dist/plugin.wasm + dist/plugin.wasm.sha256
#   bash scripts/build.sh --stamp    → also writes the sha256 into filex-app.json
#
# The build is REPRODUCIBLE: the same tree always gives the same sha256.
# That matters because filex-app.json carries the module's own hash and
# filex checks it at install, so a hash written here has to survive CI
# rebuilding the module at the tag. Two things would break it:
#
#   -buildvcs=false   Go otherwise stamps the git commit into the module,
#                     so every commit changes the hash. -trimpath does not
#                     cover this.
#   blanking wasm.sha256 before the build
#                     the manifest is embedded in the module, so a stamped
#                     manifest would change the very hash it records. The
#                     field is emptied for the build and put back after.
set -euo pipefail
cd "$(dirname "$0")/.."
export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"

# The tests run BEFORE the module is built, and a failure means no module.
# A wasm that was never checked is worse than no wasm: it installs, it
# looks right, and the first person to use it finds out.
# FILEX_SIGN_SKIP_TESTS=1 is for bisecting a build, nothing else.
if [ "${FILEX_SIGN_SKIP_TESTS:-0}" != "1" ]; then
  echo "== gofmt =="
  unformatted="$(gofmt -l . || true)"
  if [ -n "$unformatted" ]; then
    echo "not gofmt-clean:" >&2
    echo "$unformatted" >&2
    exit 1
  fi
  echo "== go vet =="
  go vet ./...
  echo "== go test =="
  go test ./...
fi

# 22 MB since 0.1.0's fonts (2026-09-21): the module grew from 17.3 to
# 20.6 MB
# rules and the script tables - which is what makes Arabic letters join and
# Devanagari conjuncts form. The FONTS add nothing: they are fetched on
# demand (fontkit/noto.go). An install compiles the module once; measure
# that time again before raising this further.
MAX_MB="${FILEX_SIGN_MAX_WASM_MB:-22}"
OUT=dist/plugin.wasm
MANIFEST=filex-app.json
BACKUP="$(mktemp)"
mkdir -p dist

cp "$MANIFEST" "$BACKUP"
restore() { cp "$BACKUP" "$MANIFEST"; rm -f "$BACKUP"; }
trap restore EXIT

python3 - "$MANIFEST" <<'PY'
import json, sys
p = sys.argv[1]
m = json.load(open(p, encoding="utf-8"))
m.setdefault("wasm", {})["sha256"] = ""
with open(p, "w", encoding="utf-8", newline="\n") as f:
    json.dump(m, f, ensure_ascii=False, indent=2)
    f.write("\n")
PY

# ⚠⚠ The guest SDK this links must be the one the host ships. While
# go.mod `replace`s it with a generated local copy, that copy can go stale
# without a sign: the module then compiles against a host contract the server
# does not have, and nothing is red until somebody uses the app. The guard
# lives in the copy itself and names the command that refreshes it; once
# go.mod points at the published module there is no copy and nothing to ask.
#
# ⚠ The copy is whichever directory go.mod's `replace` names; its name follows
# the filex release it was generated for. This used to ask ../filex-sdk-dev by
# name and skip the check in silence when that directory was not there, or
# when node was not on PATH - a guard that goes quiet exactly when it is
# needed. A replace with no check (or no node) beside it refuses.
SDK_COPY="$(sed -n 's#^replace github.com/brf-tech/filex/backend => \(\.[^ ]*\)[[:space:]]*$#\1#p' go.mod)"
if [ -n "$SDK_COPY" ]; then
  if [ ! -f "$SDK_COPY/check-sdk.mjs" ] || ! command -v node >/dev/null 2>&1; then
    echo "go.mod replaces the filex SDK with $SDK_COPY, and $SDK_COPY/check-sdk.mjs (or node) is not there to say it is current" >&2
    exit 1
  fi
  node "$SDK_COPY/check-sdk.mjs" || exit 1
fi

GOOS=wasip1 GOARCH=wasm go build -trimpath -buildvcs=false -ldflags="-s -w" -buildmode=c-shared -o "$OUT" ./cmd/plugin

if command -v sha256sum >/dev/null 2>&1; then
  SUM="$(sha256sum "$OUT" | cut -d' ' -f1)"
else
  SUM="$(shasum -a 256 "$OUT" | cut -d' ' -f1)"
fi
echo "$SUM  plugin.wasm" > "$OUT.sha256"

SIZE="$(wc -c < "$OUT")"
MB="$(awk -v b="$SIZE" 'BEGIN { printf "%.2f", b / 1048576 }')"
echo "plugin.wasm  ${SIZE} bytes (${MB} MB)  sha256 ${SUM}"

LIMIT=$(( MAX_MB * 1048576 ))
if [ "$SIZE" -gt "$LIMIT" ]; then
  echo "size gate: plugin.wasm is ${MB} MB, over the ${MAX_MB} MB limit" >&2
  exit 1
fi

restore
trap - EXIT

if [ "${1:-}" = "--stamp" ]; then
  python3 - "$MANIFEST" "$SUM" <<'PY'
import json, sys
p, sha = sys.argv[1], sys.argv[2]
m = json.load(open(p, encoding="utf-8"))
m.setdefault("wasm", {})["sha256"] = sha
with open(p, "w", encoding="utf-8", newline="\n") as f:
    json.dump(m, f, ensure_ascii=False, indent=2)
    f.write("\n")
PY
  cp "$MANIFEST" dist/filex-app.json
  echo "stamped wasm.sha256 into $MANIFEST"
fi
