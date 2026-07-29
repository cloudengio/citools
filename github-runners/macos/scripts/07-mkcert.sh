#!/usr/bin/env bash
set -euo pipefail

# Install mkcert and bake a locally-trusted CA into the System keychain so CI
# jobs can issue certificates trusted by the runner without per-job setup.
# Mirrors the steps the workflow previously ran at job time. Runs as admin;
# privileged operations use sudo (passwordless on the cirruslabs base image).

export PATH="/usr/local/bin:${PATH}"

echo "==> Installing mkcert via go install"
go install github.com/cloudengio/mkcert@latest

# go install writes to GOBIN, or $(go env GOPATH)/bin when GOBIN is unset.
GOBIN="$(go env GOBIN)"
[[ -n "$GOBIN" ]] || GOBIN="$(go env GOPATH)/bin"
MKCERT_BIN="${GOBIN}/mkcert"

echo "==> Linking mkcert onto PATH at /usr/local/bin/mkcert"
sudo ln -sf "$MKCERT_BIN" /usr/local/bin/mkcert

CAROOT="$(mkcert -CAROOT)"
echo "==> mkcert CAROOT: ${CAROOT}"

echo "==> Generating and installing local CA (mkcert -install)"
mkcert -install

echo "==> Adding mkcert root CA to the System keychain as a trusted root"
sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain "${CAROOT}/rootCA.pem"

echo "--- Verifying mkcert CA in System keychain ---"
security find-certificate -a -c "mkcert" -p /Library/Keychains/System.keychain

echo "--- Checking trust settings for the mkcert CA ---"
security verify-cert -c "${CAROOT}/rootCA.pem" -L -R trustRoot

echo "==> mkcert installed and root CA trusted (${CAROOT})"
