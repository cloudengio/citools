#!/usr/bin/env bash
set -euo pipefail

# Install Node.js (which bundles npm) from Homebrew, on the same LTS line as
# the Linux image (linux/scripts/10-npm.sh), so that both runners run the same
# major version. The versioned formula is keg-only, so it is linked into
# /opt/homebrew/bin, where everything else installed by Homebrew is found,
# rather than being left on a PATH of its own.

eval "$(/opt/homebrew/bin/brew shellenv)"

NODE_MAJOR="${NODE_MAJOR:-22}" # LTS line; override via env if needed.

echo "==> Installing Node.js ${NODE_MAJOR}.x and npm via Homebrew"
brew install "node@${NODE_MAJOR}"
brew link --force --overwrite "node@${NODE_MAJOR}"

echo "==> node version: $(node --version)"
echo "==> npm version:  $(npm --version)"
echo "==> npm installed"
