#!/usr/bin/env bash
set -euo pipefail

BREW=/opt/homebrew/bin/brew
eval "$($BREW shellenv)"

echo "==> Installing tart"
brew install cirruslabs/cli/tart

echo "==> Pulling tart images, they must have the tart agent installed"
tart pull ghcr.io/cirruslabs/ubuntu:latest
tart pull ghcr.io/cirruslabs/macos-tahoe-base:latest

echo "==> Tart images available:"
tart list
