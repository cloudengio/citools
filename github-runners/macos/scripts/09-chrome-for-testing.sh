#!/usr/bin/env bash
set -euo pipefail

# Preinstall Chrome for Testing (stable) so self-hosted runner jobs don't have to
# download it at job time. chrome-for-testing derives its install location from
# RUNNER_TOOL_CACHE / RUNNER_TEMP, so we install into the SAME paths the runner
# uses at job time (default: <runner-home>/_work/_tool and .../_temp). The chrome
# action's self-hosted branch reads these same paths to export its outputs.

export PATH="/usr/local/bin:${PATH}"

RUNNER_HOME=/Users/admin/actions-runner
export RUNNER_TOOL_CACHE="${RUNNER_TOOL_CACHE:-${RUNNER_HOME}/_work/_tool}"
export RUNNER_TEMP="${RUNNER_TEMP:-${RUNNER_HOME}/_work/_temp}"

echo "==> Tool cache: ${RUNNER_TOOL_CACHE}"
mkdir -p "${RUNNER_TOOL_CACHE}" "${RUNNER_TEMP}"

echo "==> Installing chrome-for-testing tool via go install"
go install cloudeng.io/citools/chrome-for-testing@latest

# go install writes to GOBIN, or $(go env GOPATH)/bin when GOBIN is unset.
GOBIN="$(go env GOBIN)"
[[ -n "$GOBIN" ]] || GOBIN="$(go env GOPATH)/bin"
sudo ln -sf "${GOBIN}/chrome-for-testing" /usr/local/bin/chrome-for-testing

echo "==> Installing and initializing Chrome for Testing (stable)"
chrome-for-testing install --channel=stable --application=chrome --initialize

echo "==> Chrome for Testing preinstalled under ${RUNNER_TOOL_CACHE}/setup-chrome"
