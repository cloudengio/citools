#!/usr/bin/env bash
set -euo pipefail

# Install mkcert and bake a locally-trusted CA into the trust store so CI
# jobs can issue certificates trusted by the runner without per-job setup.

export PATH="/usr/local/bin:${PATH}"
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"

if [[ "$OS" == "linux" ]]; then
  export DEBIAN_FRONTEND=noninteractive
  echo "==> Installing mkcert requirements (libnss3-tools)"
  sudo apt-get update -y
  sudo apt-get install -y --no-install-recommends libnss3-tools
fi

echo "==> Installing mkcert via go install"
go install github.com/cloudengio/mkcert@latest

# go install writes to GOBIN, or $(go env GOPATH)/bin when GOBIN is unset.
GOBIN="$(go env GOBIN)"
[[ -n "$GOBIN" ]] || GOBIN="$(go env GOPATH)/bin"
sudo ln -sf "${GOBIN}/mkcert" /usr/local/bin/mkcert

# Take the last line in case mkcert prints informational output before CAROOT.
CAROOT="$(mkcert -CAROOT | tail -n1)"
echo "==> mkcert CAROOT: ${CAROOT}"

echo "==> Generating and installing local CA (mkcert -install)"
mkcert -install

if [[ "$OS" == "darwin" ]]; then
  echo "==> Adding mkcert root CA to the System keychain as a trusted root"
  sudo security add-trusted-cert -d -r trustRoot \
    -k /Library/Keychains/System.keychain "${CAROOT}/rootCA.pem"

  echo "--- Verifying mkcert CA in System keychain ---"
  security find-certificate -a -c "mkcert" -p /Library/Keychains/System.keychain

  echo "--- Checking trust settings for the mkcert CA ---"
  security verify-cert -c "${CAROOT}/rootCA.pem" -L -R trustRoot
else
  echo "--- Installed CA files ---"
  ls -l "${CAROOT}"
fi

echo "==> mkcert installed and root CA trusted (${CAROOT})"
