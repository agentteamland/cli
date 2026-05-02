#!/usr/bin/env bash
# Build the atl CLI inside Docker, without installing Go on the host.
# Cross-compiles for the host's OS/arch by default.
#
#   scripts/build.sh                        # host-native build (OUTPUT: bin/atl)
#   scripts/build.sh 0.1.0                  # with explicit version tag
#   TARGET=linux-amd64 scripts/build.sh     # override target (for release builds)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="${REPO_ROOT}/bin"
mkdir -p "${OUT_DIR}"

VOLUME_NAME="atl-go-cache"
docker volume create "${VOLUME_NAME}" >/dev/null

VERSION="${1:-0.1.0-dev}"

# Host auto-detect.
HOST_OS="$(uname -s | tr '[:upper:]' '[:lower:]')"    # linux | darwin
HOST_ARCH_RAW="$(uname -m)"
case "${HOST_ARCH_RAW}" in
  x86_64|amd64) HOST_ARCH=amd64 ;;
  arm64|aarch64) HOST_ARCH=arm64 ;;
  *) HOST_ARCH="${HOST_ARCH_RAW}" ;;
esac

TARGET="${TARGET:-${HOST_OS}-${HOST_ARCH}}"
GOOS="${TARGET%-*}"
GOARCH="${TARGET#*-}"

OUT_NAME="atl"
if [ "${GOOS}" = "windows" ]; then
  OUT_NAME="atl.exe"
fi

BUILD_COMMIT="$(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || echo "unknown")"
BUILD_DATE="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

echo "Building atl ${VERSION} for ${GOOS}/${GOARCH} → bin/${OUT_NAME}"
echo "  commit=${BUILD_COMMIT} date=${BUILD_DATE}"

docker run --rm \
  -v "${REPO_ROOT}:/app" \
  -v "${VOLUME_NAME}:/go" \
  -w /app \
  -e GOFLAGS=-buildvcs=false \
  -e GOOS="${GOOS}" \
  -e GOARCH="${GOARCH}" \
  -e CGO_ENABLED=0 \
  golang:1.22-alpine \
  sh -c "go mod tidy && go build \
    -ldflags='-s -w \
      -X github.com/agentteamland/cli/internal/config.Version=${VERSION} \
      -X github.com/agentteamland/cli/internal/config.Commit=${BUILD_COMMIT} \
      -X github.com/agentteamland/cli/internal/config.Date=${BUILD_DATE}' \
    -o bin/${OUT_NAME} ./cmd/atl"

echo ""
echo "Built: ${OUT_DIR}/${OUT_NAME} (${GOOS}/${GOARCH}, version ${VERSION})"
ls -lh "${OUT_DIR}/${OUT_NAME}"
