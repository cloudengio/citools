#!/usr/bin/env bash
set -euo pipefail

# Preconfigure /etc/hosts with the pebble test hostnames. Baked in at build time
# because self-hosted runner jobs can't use sudo to modify /etc/hosts themselves.

if grep -q "pebble.example.com" /etc/hosts; then
  echo "==> pebble.example.com already exists in /etc/hosts"
else
  echo "==> Adding pebble.example.com to /etc/hosts"
  echo "127.0.0.1 pebble.example.com pebble-test.example.com" | sudo tee -a /etc/hosts
  cat /etc/hosts
fi
