#!/usr/bin/env bash
set -euo pipefail

RUNNER_HOME=/home/admin/actions-runner

# Fix ownership — everything should belong to admin.
sudo chown -R admin:admin "$RUNNER_HOME"

echo "==> GitHub Actions runner code installed at ${RUNNER_HOME}"
echo "==> Configuration and startup will be handled by the orchestrator via SSH."
