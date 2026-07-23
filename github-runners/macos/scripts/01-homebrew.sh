#!/usr/bin/env bash
set -euo pipefail

BREW=/opt/homebrew/bin/brew

if [ -f "$BREW" ]; then
  echo "==> Homebrew already installed"
else
  echo "==> Installing Homebrew"
  NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
fi

# Add to shell profile so subsequent provisioner scripts and login shells find it.
# Use sudo as some paths may be protected
echo 'eval "$(/opt/homebrew/bin/brew shellenv)"' >> /Users/admin/.zprofile
echo 'eval "$(/opt/homebrew/bin/brew shellenv)"' >> /Users/admin/.bash_profile

eval "$($BREW shellenv)"

echo "==> Updating Homebrew"
# brew update can fail if git is not happy or CLT is missing
# Let's be a bit more robust here.
if ! brew update --verbose; then
    echo "WARNING: brew update failed. Trying to continue anyway."
fi

echo "==> Upgrading Homebrew packages"
if ! brew upgrade --verbose; then
    echo "WARNING: brew upgrade failed. Trying to continue anyway."
fi

echo "==> Homebrew version: $(brew --version)"
