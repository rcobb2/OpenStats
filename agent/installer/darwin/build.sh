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
#   /usr/local/openlabstats/configs/agent.yaml  (regenerated from configs/agent-macos.yaml on every build)
#   /Library/LaunchDaemons/com.openlabstats.agent.plist

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AGENT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
INSTALLER_DIR="$SCRIPT_DIR"
PAYLOAD_DIR="$INSTALLER_DIR/payload"
SCRIPTS_DIR="$INSTALLER_DIR/scripts"
BUILD_DIR="$INSTALLER_DIR/build"
VERSION="$(grep -E 'AgentVersion\s*=\s*"' "$AGENT_DIR/internal/enrollment/client.go" | sed 's/.*"\(.*\)".*/\1/')"
if [[ -z "$VERSION" ]]; then
    echo "ERROR: could not read AgentVersion from client.go" >&2
    exit 1
fi
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

# Always regenerate the payload's default agent.yaml from the source of
# truth (agent/configs/agent-macos.yaml). Previously this only copied it in
# "if not already present," which let the checked-in payload copy drift
# from agent-macos.yaml and ship a stale reportURL in new packages.
# postinstall is responsible for preserving site-specific fields
# (building/room) across upgrades — this script's job is just to make sure
# the shipped default is always current.
CONFIG_DST="$PAYLOAD_DIR/usr/local/openlabstats/configs/agent.yaml"
CONFIG_SRC="$AGENT_DIR/configs/agent-macos.yaml"
if [[ -f "$CONFIG_SRC" ]]; then
    mkdir -p "$(dirname "$CONFIG_DST")"
    cp "$CONFIG_SRC" "$CONFIG_DST"
else
    echo "ERROR: default config $CONFIG_SRC not found." >&2
    exit 1
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
