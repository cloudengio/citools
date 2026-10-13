#!/usr/bin/env bash
set -euo pipefail

# Pre-bake pebble and minica binary versions under /opt/gha-tools so
# self-hosted runner jobs can select a version without downloading. Each version
# is installed into its own directory keyed by the label the action requests
# (e.g. "v2.8.0", "latest"); the pebble action adds the requested directory to
# PATH and errors if the requested version was not baked here.

export PATH="/usr/local/bin:${PATH}"

TOOLS_ROOT=/opt/gha-tools

echo "==> Preparing ${TOOLS_ROOT}"
sudo mkdir -p "${TOOLS_ROOT}"
sudo chown "$(id -un):$(id -gn)" "${TOOLS_ROOT}"

# install_tool <module> <version> <label> <binname>
# Installs <module>@<version> into ${TOOLS_ROOT}/<binname>/<label>/<binname>.
install_tool() {
  local module="$1"
  local version="$2"
  local label="$3"
  local binname="$4"
  local dest_dir="${TOOLS_ROOT}/${binname}/${label}"

  echo "==> Installing ${binname} ${label} (${module}@${version})"
  mkdir -p "${dest_dir}"
  GOBIN="${dest_dir}" go install "${module}@${version}"
  chmod +x "${dest_dir}/${binname}"
}

# Pebble versions to bake:
install_tool "github.com/letsencrypt/pebble/v2/cmd/pebble" "v2.8.0" "v2.8.0" "pebble"
install_tool "github.com/letsencrypt/pebble/v2/cmd/pebble" "latest" "latest" "pebble"

# Minica versions to bake:
install_tool "github.com/jsha/minica" "v1.1.0" "v1.1.0" "minica"

echo "==> Prebaked tools under ${TOOLS_ROOT}:"
ls -R "${TOOLS_ROOT}"
