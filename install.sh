#!/usr/bin/env bash
set -euo pipefail

# mimicode installer (macOS & Linux)
#
# Builds mimicode from source and installs it onto your PATH so you can run
# `mimicode` from anywhere.
#
# Quick install:
#   curl -fsSL https://raw.githubusercontent.com/trymimicode/mimicode-go/main/install.sh | bash
#
# Override the install location (default: /usr/local/bin):
#   INSTALL_DIR=$HOME/bin curl -fsSL .../install.sh | bash

REPO="trymimicode/mimicode-go"
MODULE="github.com/trymimicode/mimicode-go"
BINARY_NAME="mimicode"
CMD_PATH="./cmd/mimicode"

# /usr/local/bin is on PATH by default on virtually every macOS/Linux system,
# so the binary is reachable everywhere with no shell-profile editing.
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

echo "🚀 Installing mimicode..."

# ── Detect OS / arch (used only for the optional prebuilt fast path) ─────────
OS="$(uname -s)"
ARCH="$(uname -m)"
case "$OS" in
    Linux)  OS="linux" ;;
    Darwin) OS="darwin" ;;
    *)      echo "❌ Unsupported OS: $OS"; exit 1 ;;
esac
case "$ARCH" in
    x86_64)        ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *)             echo "❌ Unsupported architecture: $ARCH"; exit 1 ;;
esac

# ── Helper: place a built binary into INSTALL_DIR, using sudo if required ─────
install_binary() {
    local src="$1"
    chmod +x "$src"
    if mkdir -p "$INSTALL_DIR" 2>/dev/null && [ -w "$INSTALL_DIR" ]; then
        mv -f "$src" "$INSTALL_DIR/$BINARY_NAME"
    elif command -v sudo >/dev/null 2>&1; then
        echo "  $INSTALL_DIR needs elevated permissions; using sudo..."
        sudo mkdir -p "$INSTALL_DIR"
        sudo mv -f "$src" "$INSTALL_DIR/$BINARY_NAME"
    else
        echo "❌ Cannot write to $INSTALL_DIR and sudo is unavailable."
        echo "   Re-run with a writable location, e.g.:"
        echo "     INSTALL_DIR=\$HOME/bin bash install.sh"
        exit 1
    fi
    echo "✓ $BINARY_NAME installed to $INSTALL_DIR/$BINARY_NAME"
}

# ── Build mimicode from source ───────────────────────────────────────────────
build_from_source() {
    if ! command -v go >/dev/null 2>&1; then
        return 1
    fi
    echo "✓ Go detected, building from source..."

    local build_dir
    # If we're already inside the mimicode repo, build right here so the
    # user's local changes are what get installed.
    if [ -f go.mod ] && grep -q "^module $MODULE" go.mod 2>/dev/null; then
        build_dir="$(pwd)"
        echo "  Building from local checkout: $build_dir"
    else
        build_dir="$(mktemp -d)"
        trap 'rm -rf "$build_dir"' EXIT
        echo "  Cloning repository..."
        git clone --depth 1 "https://github.com/$REPO.git" "$build_dir"
    fi

    # Version metadata matches the Makefile so `mimicode --version` is accurate.
    local version commit build_date
    version="$(git -C "$build_dir" describe --tags --always --dirty 2>/dev/null || echo dev)"
    commit="$(git -C "$build_dir" rev-parse --short HEAD 2>/dev/null || echo unknown)"
    build_date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

    echo "  Building binary ($version)..."
    local out="$build_dir/$BINARY_NAME"
    ( cd "$build_dir" && go build \
        -ldflags="-s -w -X main.version=$version -X main.commit=$commit -X main.buildDate=$build_date" \
        -o "$out" "$CMD_PATH" )

    install_binary "$out"
    return 0
}

# ── Optional fast path: download a prebuilt release binary ───────────────────
download_prebuilt() {
    command -v curl >/dev/null 2>&1 || return 1
    local file="$BINARY_NAME-$OS-$ARCH"
    local url="https://github.com/$REPO/releases/latest/download/$file"
    local tmp; tmp="$(mktemp)"
    echo "  Checking for a prebuilt binary..."
    local code
    code="$(curl -fL -w '%{http_code}' -o "$tmp" "$url" 2>/dev/null || echo 000)"
    if [ "$code" = "200" ]; then
        echo "✓ Downloaded prebuilt binary"
        install_binary "$tmp"
        return 0
    fi
    rm -f "$tmp"
    return 1
}

# Prefer building from source (this is what "create the binary" means); fall
# back to a prebuilt download if Go isn't available.
if build_from_source; then
    :
elif download_prebuilt; then
    :
else
    echo "❌ Go is not installed and no prebuilt binary is available."
    echo "   Install Go 1.26+ from https://go.dev/dl/ and re-run, or grab a"
    echo "   binary manually from https://github.com/$REPO/releases"
    exit 1
fi

# ── Verify ───────────────────────────────────────────────────────────────────
if ! "$INSTALL_DIR/$BINARY_NAME" --version >/dev/null 2>&1; then
    echo "⚠️  Installed but '$BINARY_NAME --version' failed — check the output above."
fi

# ── Dependency / environment checks ──────────────────────────────────────────
if ! command -v rg >/dev/null 2>&1; then
    echo ""
    echo "⚠️  ripgrep (rg) is required but not installed."
    case "$OS" in
        darwin) echo "   Install: brew install ripgrep" ;;
        linux)  echo "   Install: sudo apt install ripgrep   # Debian/Ubuntu"
                echo "            sudo dnf install ripgrep   # Fedora" ;;
    esac
fi

if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo ""
    echo "⚠️  $INSTALL_DIR is not in your PATH."
    echo "   Add this to your shell profile (~/.bashrc, ~/.zshrc):"
    echo "     export PATH=\"$INSTALL_DIR:\$PATH\""
fi

if [ -z "${ANTHROPIC_API_KEY:-}" ]; then
    echo ""
    echo "⚠️  ANTHROPIC_API_KEY not set."
    echo "   Get a key at https://console.anthropic.com/settings/keys then add:"
    echo "     export ANTHROPIC_API_KEY=\"your-key-here\""
fi

echo ""
echo "✅ Installation complete!"
echo ""
echo "Usage:"
echo "  $BINARY_NAME \"add tests to calc.go\""
echo "  $BINARY_NAME --tui"
echo "  $BINARY_NAME -s myfeature \"continue working\""
echo ""
echo "Docs: https://github.com/$REPO"
