#!/usr/bin/env bash
set -euo pipefail

# Base packages for a CI runner: toolchain, VCS, archive tools, and the helpers
# later provisioning scripts rely on (curl, jq, python3, ca-certificates).
# Replaces the macOS Command Line Tools + Homebrew steps.

export DEBIAN_FRONTEND=noninteractive

echo "==> Updating apt package lists"
sudo apt-get update -y

echo "==> Installing base packages"
sudo apt-get install -y --no-install-recommends \
  build-essential \
  ca-certificates \
  curl \
  git \
  gnupg \
  jq \
  python3 \
  tar \
  unzip \
  zstd

echo "==> Base packages installed"
