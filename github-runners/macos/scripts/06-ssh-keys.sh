#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${SSH_PUBLIC_KEY:-}" ]]; then
    echo "==> No SSH_PUBLIC_KEY provided, skipping SSH key setup"
    exit 0
fi

USER_HOME="/Users/admin"
SSH_DIR="${USER_HOME}/.ssh"

echo "==> Setting up SSH public key for passwordless access"
mkdir -p "$SSH_DIR"
chmod 700 "$SSH_DIR"

echo "$SSH_PUBLIC_KEY" >> "${SSH_DIR}/authorized_keys"
chmod 600 "${SSH_DIR}/authorized_keys"
chown -R admin:staff "$SSH_DIR"

echo "==> SSH public key added to ${SSH_DIR}/authorized_keys"
