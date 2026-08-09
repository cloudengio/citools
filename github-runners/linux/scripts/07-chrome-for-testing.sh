#!/usr/bin/env bash
set -euo pipefail

# Preinstall Chrome for Testing (stable) into the runner tool cache so self-hosted
# jobs don't download it at job time. Mirrors the macOS 09-chrome-for-testing.sh.
#
# IMPORTANT: Chrome for Testing has no linux/arm64 build (Google only publishes
# linux64/x64). On an arm64 guest — which is what tart runs on Apple Silicon —
# this step is a no-op. A Linux Chrome runner must be x64.

export DEBIAN_FRONTEND=noninteractive
export PATH="/usr/local/bin:${PATH}"

ARCH=$(dpkg --print-architecture)
if [[ "$ARCH" != "amd64" ]]; then
  echo "==> Skipping Chrome for Testing: no linux/${ARCH} build exists (x64 only)"
  exit 0
fi

RUNNER_HOME=/home/admin/actions-runner
export RUNNER_TOOL_CACHE="${RUNNER_TOOL_CACHE:-${RUNNER_HOME}/_work/_tool}"
export RUNNER_TEMP="${RUNNER_TEMP:-${RUNNER_HOME}/_work/_temp}"

echo "==> Installing Chrome runtime dependencies"
sudo apt-get update -y
# Package names vary across Ubuntu releases (e.g. the 24.04 t64 transition), so
# install tolerantly and report any that are unavailable for this release.
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
