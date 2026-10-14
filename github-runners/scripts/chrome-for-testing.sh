#!/usr/bin/env bash
set -euo pipefail

# Preinstall Chrome for Testing (stable) into the runner tool cache so self-hosted
# jobs don't download it at job time. chrome-for-testing derives its install
# location from RUNNER_TOOL_CACHE / RUNNER_TEMP, so we install into the SAME paths
# the runner uses at job time (default: <runner-home>/_work/_tool and .../_temp).

export PATH="/usr/local/bin:${PATH}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

# On Linux, Chrome for Testing only publishes linux64 (x64) builds.
# On an arm64 guest (such as tart on Apple Silicon), this step is a no-op.
if [[ "$OS" == "linux" ]]; then
  if [[ "$ARCH" != "x86_64" ]]; then
    echo "==> Skipping Chrome for Testing: no linux/${ARCH} build exists (x64 only)"
    exit 0
  fi

  export DEBIAN_FRONTEND=noninteractive
  echo "==> Installing Chrome runtime dependencies"
  sudo apt-get update -y
  deps=(
    ca-certificates fonts-liberation xdg-utils
    libnss3 libgbm1 libasound2 libatk1.0-0 libatk-bridge2.0-0 libatspi2.0-0
    libcups2 libdrm2 libxkbcommon0 libxcomposite1 libxdamage1 libxfixes3
    libxrandr2 libgtk-3-0 libpango-1.0-0 libpangocairo-1.0-0 libvulkan1
  )
  for p in "${deps[@]}"; do
    sudo apt-get install -y --no-install-recommends "$p" \
      || echo "    (package $p unavailable on this release, skipping)"
  done
fi

RUNNER_HOME="${HOME}/actions-runner"
export RUNNER_TOOL_CACHE="${RUNNER_TOOL_CACHE:-${RUNNER_HOME}/_work/_tool}"
export RUNNER_TEMP="${RUNNER_TEMP:-${RUNNER_HOME}/_work/_temp}"

echo "==> Tool cache: ${RUNNER_TOOL_CACHE}"
mkdir -p "${RUNNER_TOOL_CACHE}" "${RUNNER_TEMP}"

echo "==> Installing chrome-for-testing tool via go install"
go install cloudeng.io/citools/chrome-for-testing@latest

GOBIN="$(go env GOBIN)"
[[ -n "$GOBIN" ]] || GOBIN="$(go env GOPATH)/bin"
sudo ln -sf "${GOBIN}/chrome-for-testing" /usr/local/bin/chrome-for-testing

echo "==> Installing and initializing Chrome for Testing (stable)"
chrome-for-testing install --channel=stable --application=chrome --initialize

echo "==> Chrome for Testing preinstalled under ${RUNNER_TOOL_CACHE}/setup-chrome"
