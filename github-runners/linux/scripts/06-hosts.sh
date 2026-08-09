#!/usr/bin/env bash
set -euo pipefail

# Preconfigure /etc/hosts with the pebble test hostnames. Baked in at build time
# because self-hosted runner jobs can't use sudo to modify /etc/hosts themselves.
#
# Both IPv4 and IPv6 loopback entries are added (one address per line, matching
# how the base image defines plain "localhost") so the pebble hostnames resolve
# dual-stack; some tests depend on IPv6 (::1) localhost resolution.

# add_hosts_entry <address> <hostnames...>
# Appends the entry only if that exact address+hostname line isn't already present.
add_hosts_entry() {
  local entry="$*"
  if grep -qF "${entry}" /etc/hosts; then
    echo "==> already present in /etc/hosts: ${entry}"
  else
    echo "==> adding to /etc/hosts: ${entry}"
    echo "${entry}" | sudo tee -a /etc/hosts >/dev/null
  fi
}

# Ensure ::1 maps localhost (stock Ubuntu already does this). Guard on intent
# rather than an exact string so we don't duplicate the base image's
# "::1 localhost ip6-localhost ip6-loopback" line.
if grep -Eq '^[[:space:]]*::1[[:space:]]+\blocalhost\b' /etc/hosts; then
  echo "==> ::1 already maps localhost in /etc/hosts"
else
  echo "==> adding to /etc/hosts: ::1 localhost"
  echo "::1 localhost" | sudo tee -a /etc/hosts >/dev/null
fi

add_hosts_entry "127.0.0.1 pebble.example.com pebble-test.example.com"
add_hosts_entry "::1       pebble.example.com pebble-test.example.com"

echo "--- /etc/hosts ---"
cat /etc/hosts
