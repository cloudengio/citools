#!/usr/bin/env bash
set -euo pipefail

eval "$(/opt/homebrew/bin/brew shellenv)"

echo "==> System bash: $(/bin/bash --version | head -1)"

echo "==> Installing latest bash via Homebrew"
brew install bash

BREW_BASH=/opt/homebrew/bin/bash

echo "==> Installed bash: $($BREW_BASH --version | head -1)"

# Add Homebrew bash to the list of acceptable shells if not already present.
if ! grep -qxF "$BREW_BASH" /etc/shells; then
  echo "$BREW_BASH" | sudo tee -a /etc/shells
fi
