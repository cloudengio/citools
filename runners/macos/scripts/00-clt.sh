#!/usr/bin/env bash
set -euo pipefail

# Install the latest Xcode Command Line Tools (macOS SDK + clang, no mobile simulators).
# In some VM environments, softwareupdate -i fails because of missing recovery partitions.
# xcode-select --install triggers a background install.

if [ -d "/Library/Developer/CommandLineTools" ]; then
    echo "==> Command Line Tools already installed"
    exit 0
fi

echo "==> Triggering Command Line Tools install via xcode-select"
# This creates a sentinel file that triggers the install on next boot/login,
# but we want it NOW.

# The most reliable way in headless VMs:
touch /tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress

PROD=$(softwareupdate -l | grep "\*.*Command Line Tools" | tail -n 1 | awk -F"*" '{print $2}' | sed -e 's/^ *//' | tr -d '\n')
if [[ -z "$PROD" ]]; then
    echo "ERROR: Could not find CLT package."
    exit 1
fi

echo "==> Installing $PROD"
softwareupdate -i "$PROD" --verbose || true

# If that still fails, we try a different approach:
if [ ! -d "/Library/Developer/CommandLineTools" ]; then
    echo "WARNING: softwareupdate failed. Trying to find a way to install CLT."
    # Sometimes it needs to be the EXACT string from softwareupdate -l without any leading/trailing junk
    # Re-extracting more carefully:
    PROD=$(softwareupdate -l | grep "Label: Command Line Tools" | sort -V | tail -1 | sed 's/.*Label: //')
    echo "==> Retrying with: $PROD"
    softwareupdate -i "$PROD" --verbose || true
fi

# Final check
if [ ! -d "/Library/Developer/CommandLineTools" ]; then
    echo "ERROR: Failed to install Command Line Tools."
    # List what we DID find
    softwareupdate -l
    exit 1
fi

echo "==> Command Line Tools installed at $(xcode-select -p)"
