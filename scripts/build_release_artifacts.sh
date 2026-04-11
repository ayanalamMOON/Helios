#!/usr/bin/env bash
set -euo pipefail

TAG="${1:-}"
GOOS="${2:-}"
GOARCH="${3:-}"

if [[ -z "$TAG" || -z "$GOOS" || -z "$GOARCH" ]]; then
  echo "Usage: $0 <tag> <goos> <goarch>" >&2
  exit 1
fi

EXT=""
if [[ "$GOOS" == "windows" ]]; then
  EXT=".exe"
fi

OUT_DIR="dist/helios-${TAG}-${GOOS}-${GOARCH}"
mkdir -p "$OUT_DIR"

TARGETS=(
  helios-atlasd
  helios-gateway
  helios-proxy
  helios-worker
  helios-cli
  helios-gencerts
  helios-migrate
)

for target in "${TARGETS[@]}"; do
  echo "Building ${target} for ${GOOS}/${GOARCH}"
  GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 \
    go build -trimpath -o "${OUT_DIR}/${target}${EXT}" "./cmd/${target}"
done

echo "Built release artifacts in: ${OUT_DIR}"
