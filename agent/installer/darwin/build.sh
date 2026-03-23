#!/usr/bin/env bash
# build.sh — Build the OpenLabStats agent macOS .pkg installer.
#
# Usage:
#   ./build.sh [arm64|amd64|universal]
#
# Requires:
#   - Go with CGO_ENABLED=1 and an appropriate macOS SDK
#   - Xcode Command Line Tools (for pkgbuild / productbuild)
#   - Run from the agent/ directory (or set AGENT_DIR)
#
# The resulting package installs:
#   /usr/local/openlabstats/openlabstats-agent
#   /usr/local/openlabstats/configs/agent.yaml  (if not already present)
#   /Library/LaunchDaemons/com.openlabstats.agent.plist

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AGENT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
INSTALLER_DIR="$SCRIPT_DIR"
PAYLOAD_DIR="$INSTALLER_DIR/payload"
SCRIPTS_DIR="$INSTALLER_DIR/scripts"
BUILD_DIR="$INSTALLER_DIR/build"
VERSION="0.1.3"
ARCH="${1:-arm64}"

echo "Building openlabstats-agent v$VERSION for $ARCH ..."

mkdir -p "$BUILD_DIR"
mkdir -p "$PAYLOAD_DIR/usr/local/openlabstats"

BIN_PATH="$PAYLOAD_DIR/usr/local/openlabstats/openlabstats-agent"

if [[ "$ARCH" == "universal" ]]; then
    # Build both architectures and lipo them together.
    cd "$AGENT_DIR"
    CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build \
        -o "$BUILD_DIR/openlabstats-agent-arm64" ./cmd/agent/
    CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build \
        -o "$BUILD_DIR/openlabstats-agent-amd64" ./cmd/agent/
    lipo -create -output "$BIN_PATH" \
        "$BUILD_DIR/openlabstats-agent-arm64" \
        "$BUILD_DIR/openlabstats-agent-amd64"
    echo "  Built universal binary."
else
    cd "$AGENT_DIR"
    CGO_ENABLED=1 GOOS=darwin GOARCH="$ARCH" go build \
        -o "$BIN_PATH" ./cmd/agent/
    echo "  Built $ARCH binary."
fi

chmod 755 "$BIN_PATH"

# Copy a default agent.yaml only if none exists in the payload.
CONFIG_DST="$PAYLOAD_DIR/usr/local/openlabstats/configs/agent.yaml"
CONFIG_SRC="$AGENT_DIR/configs/agent.yaml"
if [[ ! -f "$CONFIG_DST" && -f "$CONFIG_SRC" ]]; then
    mkdir -p "$(dirname "$CONFIG_DST")"
    cp "$CONFIG_SRC" "$CONFIG_DST"
fi

# Make scripts executable.
chmod +x "$SCRIPTS_DIR/preinstall"
chmod +x "$SCRIPTS_DIR/postinstall"

PKG_PATH="$BUILD_DIR/openlabstats-agent-$VERSION-$ARCH.pkg"

pkgbuild \
    --root "$PAYLOAD_DIR" \
    --scripts "$SCRIPTS_DIR" \
    --identifier "com.openlabstats.agent" \
    --version "$VERSION" \
    --install-location "/" \
    "$PKG_PATH"

echo "Package written to: $PKG_PATH"
