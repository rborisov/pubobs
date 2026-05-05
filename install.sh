#!/usr/bin/env bash
set -euo pipefail

REPO_URL="https://lab.rclmx.ru/gogs/rborisov/pubobs.git"
INSTALL_DIR="/opt/pubobs"
PORT=8000

echo "=== PubObs Installer ==="
echo ""

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  BINARY="installer-linux-amd64" ;;
  aarch64) BINARY="installer-linux-arm64" ;;
  arm64)   BINARY="installer-linux-arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

# Install git if missing
if ! command -v git &>/dev/null; then
  echo "Installing git..."
  apt-get update -qq && apt-get install -y -qq git
fi

# Clone or update repo
if [ -d "$INSTALL_DIR/.git" ]; then
  echo "Repo already exists at $INSTALL_DIR, updating..."
  git -C "$INSTALL_DIR" pull --ff-only
else
  echo "Cloning PubObs to $INSTALL_DIR..."
  git clone --branch main "$REPO_URL" "$INSTALL_DIR"
fi

INSTALLER="$INSTALL_DIR/installer/bin/$BINARY"
chmod +x "$INSTALLER"

# Detect public IP
PUBLIC_IP=$(curl -s --max-time 5 https://api.ipify.org 2>/dev/null || hostname -I | awk '{print $1}')

echo ""
echo "+---------------------------------------------------------+"
echo "|  PubObs Installer is ready.                            |"
echo "|                                                        |"
echo "|  Open in your browser:                                 |"
printf "|  http://%-46s |\n" "${PUBLIC_IP}:${PORT}"
echo "|                                                        |"
printf "|  Note: ensure port %-38s |\n" "$PORT is reachable from your machine"
echo "+---------------------------------------------------------+"
echo ""
echo "Installer log output:"
echo "--------------------"

# Run installer (foreground)
"$INSTALLER" --port "$PORT"
