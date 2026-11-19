#!/usr/bin/env bash
# vendor-llama.sh — Download, verify, cache, and stage pinned llama.cpp runtime
#
# Pinned release: ggml-org/llama.cpp b12428 arm64 macOS release
# SHA256: 7d72477a2a375cc1d93e308b34e26fb9a608c2f93750517d2bafc59af578d3e2
#
# Usage:
#   scripts/vendor-llama.sh [DEST_DIR]
#
# If DEST_DIR is omitted, defaults to build/llama

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TAG="b12428"
ASSET_NAME="llama-${TAG}-bin-macos-arm64.tar.gz"
EXPECTED_SHA256="7d72477a2a375cc1d93e308b34e26fb9a608c2f93750517d2bafc59af578d3e2"
URL="https://github.com/ggml-org/llama.cpp/releases/download/${TAG}/${ASSET_NAME}"

CACHE_DIR="${ROOT}/build/cache"
CACHE_FILE="${CACHE_DIR}/${ASSET_NAME}"
DEST_DIR="${1:-${ROOT}/build/llama}"

mkdir -p "$CACHE_DIR"

# 1. Download and verify cached archive
if [ -f "$CACHE_FILE" ]; then
  ACTUAL_SHA256="$(shasum -a 256 "$CACHE_FILE" | awk '{print $1}')"
  if [ "$ACTUAL_SHA256" != "$EXPECTED_SHA256" ]; then
    echo "warning: cached ${ASSET_NAME} sha256 mismatch ($ACTUAL_SHA256 != $EXPECTED_SHA256); re-downloading" >&2
    rm -f "$CACHE_FILE"
  fi
fi

if [ ! -f "$CACHE_FILE" ]; then
  echo "==> Downloading pinned llama.cpp runtime (${ASSET_NAME})..."
  curl -fSL "$URL" -o "${CACHE_FILE}.tmp"
  ACTUAL_SHA256="$(shasum -a 256 "${CACHE_FILE}.tmp" | awk '{print $1}')"
  if [ "$ACTUAL_SHA256" != "$EXPECTED_SHA256" ]; then
    echo "error: downloaded ${ASSET_NAME} sha256 mismatch:" >&2
    echo "  expected: $EXPECTED_SHA256" >&2
    echo "  actual:   $ACTUAL_SHA256" >&2
    rm -f "${CACHE_FILE}.tmp"
    exit 1
  fi
  mv "${CACHE_FILE}.tmp" "$CACHE_FILE"
  echo "==> Verified ${ASSET_NAME} sha256: ${ACTUAL_SHA256}"
fi

# 2. Extract and stage into DEST_DIR
mkdir -p "$DEST_DIR"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

tar -xzf "$CACHE_FILE" -C "$TMP_DIR"

EXTRACTED_DIR="${TMP_DIR}/llama-${TAG}"
if [ ! -d "$EXTRACTED_DIR" ]; then
  echo "error: expected directory ${EXTRACTED_DIR} not found in archive" >&2
  exit 1
fi

echo "==> Staging llama runtime into ${DEST_DIR}..."
# Copy binaries, license, and shared dynamic libraries (preserving symlinks)
cp -a "$EXTRACTED_DIR/llama-server" "$DEST_DIR/"
cp -a "$EXTRACTED_DIR/llama-cli" "$DEST_DIR/"
cp -a "$EXTRACTED_DIR/LICENSE" "$DEST_DIR/"
cp -a "$EXTRACTED_DIR/"*.dylib "$DEST_DIR/"

chmod +x "$DEST_DIR/llama-server" "$DEST_DIR/llama-cli"

echo "==> Successfully staged llama.cpp ${TAG} runtime to ${DEST_DIR}"
