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

# The tap that publishes tart. It moved from cirruslabs/cli to openai/tools
# when tart moved to github.com/openai/tart: the old formula now redirects to
# this one, so installing from the old tap fails on the new tap's trust.
TART_TAP="${TART_TAP:-openai/tools}"

if command -v tart >/dev/null 2>&1; then
  echo "==> tart already installed: $(tart --version)"
else
  echo "==> Installing tart from ${TART_TAP} via Homebrew"
  brew tap "${TART_TAP}"
  # Homebrew will not load a formula from a tap that is not official unless the
  # tap is trusted. The whole tap is trusted rather than the formula alone,
  # since tart depends on softnet from the same tap and that has to load too.
  if brew trust --help >/dev/null 2>&1; then
    brew trust "${TART_TAP}"
  fi
  brew install "${TART_TAP}/tart"
fi

echo "==> tart version: $(tart --version)"
echo "==> tart installed at: $(command -v tart)"
