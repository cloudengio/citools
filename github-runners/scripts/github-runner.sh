#!/usr/bin/env bash
set -euo pipefail

RUNNER_HOME="${HOME}/actions-runner"
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  darwin) GHA_OS="osx" ;;
  linux)  GHA_OS="linux" ;;
  *) echo "ERROR: unsupported OS $OS" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64)        GHA_ARCH="x64" ;;
  arm64|aarch64) GHA_ARCH="arm64" ;;
  *) echo "ERROR: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

echo "==> Fetching latest GitHub Actions runner version"
RUNNER_VERSION=$(curl -fsSL "https://api.github.com/repos/actions/runner/releases/latest" | \
  python3 -c "import sys, json; print(json.load(sys.stdin)['tag_name'].lstrip('v'))")
echo "    version: ${RUNNER_VERSION}"

mkdir -p "$RUNNER_HOME"
cd "$RUNNER_HOME"

TARBALL="actions-runner-${GHA_OS}-${GHA_ARCH}-${RUNNER_VERSION}.tar.gz"
URL="https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/${TARBALL}"

echo "==> Downloading runner from ${URL}"
curl -fsSL "$URL" -o "$TARBALL"
tar xzf "$TARBALL"
rm "$TARBALL"

echo "==> Installing runner dependencies"
sudo ./bin/installdependencies.sh || true

echo "==> GitHub Actions runner ${RUNNER_VERSION} installed at ${RUNNER_HOME}"
