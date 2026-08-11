#!/usr/bin/env bash
set -euo pipefail

# Install mkcert and bake a locally-trusted CA into the system + NSS trust stores
# so CI jobs can issue trusted certificates without per-job setup. Mirrors the
# Linux steps of the mkcert composite action:
#   - libnss3-tools provides certutil, required by mkcert to update the NSS store
#   - go install github.com/cloudengio/mkcert@latest
#   - mkcert -install

export DEBIAN_FRONTEND=noninteractive
export PATH="/usr/local/bin:${PATH}"

echo "==> Installing mkcert requirements (libnss3-tools)"
sudo apt-get update -y
sudo apt-get install -y --no-install-recommends libnss3-tools

echo "==> Installing mkcert via go install"
go install github.com/cloudengio/mkcert@latest

# go install writes to GOBIN, or $(go env GOPATH)/bin when GOBIN is unset.
GOBIN="$(go env GOBIN)"
[[ -n "$GOBIN" ]] || GOBIN="$(go env GOPATH)/bin"
sudo ln -sf "${GOBIN}/mkcert" /usr/local/bin/mkcert

# On Linux mkcert prints an informational "Found certutil at ..." line to stdout
# before the CAROOT path, so take the last line to get the directory itself.
CAROOT="$(mkcert -CAROOT | tail -n1)"
echo "==> mkcert CAROOT: ${CAROOT}"

echo "==> Generating and installing local CA (mkcert -install)"
mkcert -install

echo "--- Installed CA files ---"
ls -l "${CAROOT}"

echo "==> mkcert installed and root CA trusted (${CAROOT})"
