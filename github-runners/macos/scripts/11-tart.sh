#!/usr/bin/env bash
set -euo pipefail

# Install the tart CLI via Homebrew so the image has it available (e.g. for
# pulling/cloning images or driving nested tooling).
#
# NOTE: tart uses Apple's Virtualization.framework, which does NOT support nested
# virtualization on Apple Silicon. The binary installs fine here, but `tart run`
# to actually boot a VM will fail inside this guest. Image operations that don't
# start a VM (tart pull / clone / list / images) do work.

# Homebrew is installed by 01-homebrew.sh; make it available in this session.
eval "$(/opt/homebrew/bin/brew shellenv)"

if command -v tart >/dev/null 2>&1; then
  echo "==> tart already installed: $(tart --version)"
else
  echo "==> Installing tart via Homebrew"
  # Newer Homebrew enforces tap trust. tart pulls in the softnet formula from the
  # same cirruslabs/cli tap, which isn't auto-trusted, so trust the tap first or
  # `brew install` aborts refusing to load softnet.
  brew tap cirruslabs/cli
  brew trust cirruslabs/cli
  brew install cirruslabs/cli/tart
fi

echo "==> tart version: $(tart --version)"
echo "==> tart installed at: $(command -v tart)"
