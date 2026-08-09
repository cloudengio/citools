#!/usr/bin/env bash
set -euo pipefail

RUNNER_HOME=/home/admin/actions-runner
OS=linux

# Map dpkg arch (amd64/arm64) to the actions runner arch suffix (x64/arm64).
case "$(dpkg --print-architecture)" in
  amd64) ARCH=x64 ;;
  arm64) ARCH=arm64 ;;
  *) echo "ERROR: unsupported architecture $(dpkg --print-architecture)" >&2; exit 1 ;;
esac

echo "==> Fetching latest GitHub Actions runner version"
RUNNER_VERSION=$(curl -fsSL "https://api.github.com/repos/actions/runner/releases/latest" | \
  python3 -c "import sys, json; print(json.load(sys.stdin)['tag_name'].lstrip('v'))")
echo "    version: ${RUNNER_VERSION}"

mkdir -p "$RUNNER_HOME"
cd "$RUNNER_HOME"

TARBALL="actions-runner-${OS}-${ARCH}-${RUNNER_VERSION}.tar.gz"
URL="https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/${TARBALL}"

echo "==> Downloading runner from ${URL}"
curl -fsSL "$URL" -o "$TARBALL"
tar xzf "$TARBALL"
rm "$TARBALL"

echo "==> Installing runner dependencies"
sudo ./bin/installdependencies.sh

echo "==> GitHub Actions runner ${RUNNER_VERSION} installed at ${RUNNER_HOME}"
