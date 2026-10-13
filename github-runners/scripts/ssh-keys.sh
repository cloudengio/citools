#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${SSH_PUBLIC_KEY:-}" ]]; then
    echo "==> No SSH public key provided, skipping SSH key setup"
    exit 0
fi

USER_HOME="${HOME}"
SSH_DIR="${USER_HOME}/.ssh"

echo "==> Setting up SSH public key for passwordless access"
mkdir -p "$SSH_DIR"
chmod 700 "$SSH_DIR"

echo "$SSH_PUBLIC_KEY" >> "${SSH_DIR}/authorized_keys"
chmod 600 "${SSH_DIR}/authorized_keys"
sudo chown -R "$(id -un):$(id -gn)" "$SSH_DIR"

echo "==> SSH public key added to ${SSH_DIR}/authorized_keys"
