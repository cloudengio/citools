#!/usr/bin/env bash
set -euo pipefail

RUNNER_HOME=/Users/admin/actions-runner
ARCH=arm64
OS=osx

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
sudo ./bin/installdependencies.sh || true

echo "==> GitHub Actions runner ${RUNNER_VERSION} installed at ${RUNNER_HOME}"
