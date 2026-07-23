#!/usr/bin/env bash
set -euo pipefail

# Go minor versions to install. The script resolves the latest patch release
# for each minor version at build time.
GO_VERSIONS=(1.24 1.25 1.26)

INSTALL_ROOT=/usr/local
ARCH=arm64
OS=darwin

export PATH="${INSTALL_ROOT}/bin:${PATH}"

# Resolve the latest stable patch release for a given minor version.
# Reads JSON from stdin (go.dev/dl/?mode=json&include=all).
latest_patch() {
  local minor="$1"
  GO_MINOR="$minor" python3 -c "
import sys, json, os
data = json.load(sys.stdin)
minor = os.environ['GO_MINOR']
prefix = 'go' + minor + '.'
# Stable releases whose version starts with goX.Y.
candidates = [
    r['version'] for r in data
    if r.get('stable') and r['version'].startswith(prefix)
]
if not candidates:
    # Handle the case where the minor itself is the release (no patch component).
    candidates = [
        r['version'] for r in data
        if r.get('stable') and r['version'] == 'go' + minor
    ]
if not candidates:
    sys.exit(1)
# Sort by numeric version components.
candidates.sort(key=lambda v: [int(x) for x in v[2:].split('.')])
print(candidates[-1])
"
}

echo "==> Fetching Go release list"
go_releases=$(curl -fsSL "https://go.dev/dl/?mode=json&include=all")

declare -a installed_versions

for minor in "${GO_VERSIONS[@]}"; do
  version=$(echo "$go_releases" | latest_patch "$minor")
  if [[ -z "$version" ]]; then
    echo "ERROR: could not resolve latest patch for Go ${minor}" >&2
    exit 1
  fi

  install_dir="${INSTALL_ROOT}/${version}"

  if [[ -d "$install_dir" ]]; then
    echo "==> ${version} already present at ${install_dir}, skipping"
  else
    echo "==> Installing ${version}"
    url="https://go.dev/dl/${version}.${OS}-${ARCH}.tar.gz"
    tmpfile=$(mktemp /tmp/go-XXXXXX.tar.gz)
    curl -fsSL "$url" -o "$tmpfile"
    sudo tar -C "$INSTALL_ROOT" -xzf "$tmpfile"
    sudo mv "${INSTALL_ROOT}/go" "$install_dir"
    rm -f "$tmpfile"
    echo "    installed at ${install_dir}"
  fi

  # Versioned symlinks: go1.24, gofmt1.24, etc.
  sudo ln -sf "${install_dir}/bin/go"    "${INSTALL_ROOT}/bin/go${minor}"
  sudo ln -sf "${install_dir}/bin/gofmt" "${INSTALL_ROOT}/bin/gofmt${minor}"

  installed_versions+=("$version")
done

# Make the latest minor version the default 'go' and 'gofmt'.
latest_minor="${GO_VERSIONS[${#GO_VERSIONS[@]}-1]}"
latest_version=$(echo "$go_releases" | latest_patch "$latest_minor")
latest_dir="${INSTALL_ROOT}/${latest_version}"

sudo ln -sf "${latest_dir}/bin/go"    "${INSTALL_ROOT}/bin/go"
sudo ln -sf "${latest_dir}/bin/gofmt" "${INSTALL_ROOT}/bin/gofmt"

echo "==> Default go -> ${latest_version}"
go version

echo "==> Installed Go versions:"
for v in "${installed_versions[@]}"; do
  echo "    ${v}: $("${INSTALL_ROOT}/${v}/bin/go" version)"
done
