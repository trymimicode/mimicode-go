#!/usr/bin/env bash
set -e

# mimicode installer
# Usage: curl -fsSL https://raw.githubusercontent.com/trymimicode/mimicode-go/main/install.sh | bash

REPO="trymimicode/mimicode-go"
BINARY_NAME="mimicode"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

echo "🚀 Installing mimicode..."

# Detect OS and architecture
OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
    Linux)  OS="linux" ;;
    Darwin) OS="darwin" ;;
    *)      echo "❌ Unsupported OS: $OS"; exit 1 ;;
esac

case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *)       echo "❌ Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Try downloading pre-built binary from latest release
BINARY_FILE="$BINARY_NAME-$OS-$ARCH"
DOWNLOAD_URL="https://github.com/$REPO/releases/latest/download/$BINARY_FILE"

echo "  Trying to download pre-built binary..."
if command -v curl >/dev/null 2>&1; then
    HTTP_CODE=$(curl -L -w "%{http_code}" -o "/tmp/$BINARY_FILE" "$DOWNLOAD_URL" 2>/dev/null || echo "000")
    if [ "$HTTP_CODE" = "200" ]; then
        echo "✓ Downloaded pre-built binary"
        mkdir -p "$INSTALL_DIR"
        mv "/tmp/$BINARY_FILE" "$INSTALL_DIR/$BINARY_NAME"
        chmod +x "$INSTALL_DIR/$BINARY_NAME"
        echo "✓ $BINARY_NAME installed to $INSTALL_DIR/$BINARY_NAME"
        INSTALLED=1
    else
        echo "  No pre-built binary available (HTTP $HTTP_CODE)"
        rm -f "/tmp/$BINARY_FILE"
    fi
fi

# Fall back to building from source if download failed
if [ -z "$INSTALLED" ]; then
    if command -v go >/dev/null 2>&1; then
        echo "✓ Go detected, building from source..."
        
        # Create temp directory
        TMP_DIR=$(mktemp -d)
        trap "rm -rf $TMP_DIR" EXIT
        
        cd "$TMP_DIR"
        echo "  Cloning repository..."
        git clone --depth 1 "https://github.com/$REPO.git" .
        
        echo "  Building binary..."
        go build -o "$BINARY_NAME" -ldflags="-s -w" ./cmd/mimicode
        
        # Create install directory if it doesn't exist
        mkdir -p "$INSTALL_DIR"
        
        # Install binary
        mv "$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
        chmod +x "$INSTALL_DIR/$BINARY_NAME"
        
        echo "✓ $BINARY_NAME installed to $INSTALL_DIR/$BINARY_NAME"
    else
        echo "❌ Could not download pre-built binary and Go is not installed."
        echo "   Install Go 1.26+ from https://go.dev/dl/ or download manually:"
        echo "   https://github.com/$REPO/releases"
        exit 1
    fi
fi

# Verify installation
if ! "$INSTALL_DIR/$BINARY_NAME" --version >/dev/null 2>&1; then
    echo "⚠️  Installation completed but binary verification failed"
fi

# Check if ripgrep is installed
if ! command -v rg >/dev/null 2>&1; then
    echo ""
    echo "⚠️  ripgrep (rg) is required but not installed."
    echo "   Install it from: https://github.com/BurntSushi/ripgrep#installation"
    echo ""
    case "$OS" in
        darwin)
            echo "   Quick install: brew install ripgrep"
            ;;
        linux)
            echo "   Quick install: sudo apt install ripgrep  # Debian/Ubuntu"
            echo "                  sudo dnf install ripgrep  # Fedora"
            ;;
    esac
fi

# Check PATH
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo ""
    echo "⚠️  $INSTALL_DIR is not in your PATH."
    echo "   Add this to your shell profile (~/.bashrc, ~/.zshrc, etc.):"
    echo ""
    echo "   export PATH=\"\$HOME/.local/bin:\$PATH\""
    echo ""
fi

# Check API key
if [ -z "$ANTHROPIC_API_KEY" ]; then
    echo ""
    echo "⚠️  ANTHROPIC_API_KEY not set."
    echo "   Get your API key from: https://console.anthropic.com/settings/keys"
    echo "   Then add to your shell profile:"
    echo ""
    echo "   export ANTHROPIC_API_KEY=\"your-key-here\""
    echo ""
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
