#!/bin/sh
set -e

REPO="sestinj/att"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Detect OS and architecture
OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
  Linux)  OS="linux" ;;
  Darwin) OS="darwin" ;;
  *) echo "Unsupported OS: $OS" >&2; exit 1 ;;
esac

case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

# Get latest version
VERSION="$(curl -sSf "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)"
if [ -z "$VERSION" ]; then
  echo "Failed to fetch latest version" >&2
  exit 1
fi

FILENAME="att_${VERSION#v}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${FILENAME}"

echo "Installing att ${VERSION} (${OS}/${ARCH})..."

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

curl -sSfL "$URL" -o "$TMP/$FILENAME"
tar xzf "$TMP/$FILENAME" -C "$TMP" att

if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP/att" "$INSTALL_DIR/att"
else
  echo "Installing to $INSTALL_DIR (requires sudo)..."
  sudo mv "$TMP/att" "$INSTALL_DIR/att"
fi

echo "att ${VERSION} installed to ${INSTALL_DIR}/att"
