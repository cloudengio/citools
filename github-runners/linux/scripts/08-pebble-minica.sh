#!/usr/bin/env bash
set -euo pipefail

# Prebake pebble (v2.8.0 + latest) and minica (v1.1.0) into the image so
# self-hosted runner jobs can select a version without downloading. Each version
# is installed into its own directory keyed by the label the action requests
# (e.g. "v2.8.0", "latest"); the pebble action adds the requested directory to
# PATH and errors if the requested version was not baked here. Identical logic
# to the macOS 10-pebble-minica.sh.

export PATH="/usr/local/bin:${PATH}"

TOOLS_ROOT=/opt/gha-tools

echo "==> Preparing ${TOOLS_ROOT}"
sudo mkdir -p "${TOOLS_ROOT}"
sudo chown "$(id -un):$(id -gn)" "${TOOLS_ROOT}"

# install_tool <module> <version> <label> <binname>
# Installs <module>@<version> into ${TOOLS_ROOT}/<binname>/<label>/<binname>.
install_tool() {
  local module="$1" version="$2" label="$3" binname="$4"
  local dir="${TOOLS_ROOT}/${binname}/${label}"
  echo "==> Installing ${binname} ${label} (${module}@${version})"
  mkdir -p "${dir}"
  GOBIN="${dir}" go install "${module}@${version}"
}

install_tool github.com/letsencrypt/pebble/v2/cmd/pebble v2.8.0 v2.8.0 pebble
install_tool github.com/letsencrypt/pebble/v2/cmd/pebble latest latest pebble
install_tool github.com/jsha/minica                      v1.1.0 v1.1.0 minica

echo "==> Prebaked tools under ${TOOLS_ROOT}:"
ls -R "${TOOLS_ROOT}"
