#!/usr/bin/env bash
set -euo pipefail

# Disable Spotlight indexing across all volumes to eliminate background
# indexing CPU and disk I/O overhead during runner jobs.
echo "==> Disabling Spotlight indexing"
sudo mdutil -a -i off
