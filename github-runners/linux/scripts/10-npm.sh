#!/usr/bin/env bash
set -euo pipefail

# Install Node.js (which bundles npm) from NodeSource's apt repository. The
# distro's own nodejs package lags well behind, so NodeSource gives a current LTS
# with matching npm. NodeSource ships both amd64 and arm64 packages, so this
# works on the arm64 guest tart runs on Apple Silicon as well as on x64.

export DEBIAN_FRONTEND=noninteractive

NODE_MAJOR="${NODE_MAJOR:-22}" # LTS line; override via env if needed.

echo "==> Configuring NodeSource apt repository (Node.js ${NODE_MAJOR}.x)"
sudo apt-get update -y
sudo apt-get install -y --no-install-recommends ca-certificates curl gnupg

curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | sudo -E bash -

echo "==> Installing Node.js and npm"
sudo apt-get install -y nodejs

echo "==> node version: $(node --version)"
echo "==> npm version:  $(npm --version)"
echo "==> npm installed"
