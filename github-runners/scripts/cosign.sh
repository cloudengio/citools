#!/usr/bin/env bash
set -euo pipefail

# Install the cosign CLI (https://docs.sigstore.dev), which jobs on this runner
# need in order to sign and verify OCI artifacts: the orchestrator's own tests
# sign an image in a throwaway registry and verify it, and its cosignverify
# package shells out to this binary at run time.
#
# macOS takes it from Homebrew, which is what the hosts that sign the VM images
# use, see github-runners/cosign.mk. Linux has no cosign package, so the
# release binary is fetched and checked against the checksum the release
# publishes beside it.
#
# COSIGN_VERSION pins a release tag, eg. v3.1.4, on Linux; the default is the
# latest release, which is what Homebrew installs on macOS. Homebrew has no
# say in which version it carries, so the two can differ by a patch release
# for as long as it takes a release to reach homebrew-core.

export PATH="/usr/local/bin:${PATH}"
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
COSIGN_VERSION="${COSIGN_VERSION:-}"

# cosign_version prints the installed version, which is buried in the banner
# that "cosign version" leads with.
cosign_version() {
  cosign version 2>&1 | awk '/GitVersion:/ { print $2 }'
}

if [[ "$OS" == "darwin" ]]; then
  # Homebrew is installed by macos/scripts/01-homebrew.sh; make it available in
  # this session. cosign is in homebrew-core, so no tap has to be trusted.
  eval "$(/opt/homebrew/bin/brew shellenv)"

  [[ -z "$COSIGN_VERSION" ]] ||
    echo "WARNING: COSIGN_VERSION=${COSIGN_VERSION} is ignored on macOS: Homebrew installs the version it carries"

  if command -v cosign >/dev/null 2>&1; then
    echo "==> cosign already installed: $(cosign_version)"
  else
    echo "==> Installing cosign via Homebrew"
    brew install cosign
  fi
else
  case "$(uname -m)" in
  x86_64) ARCH="amd64" ;;
  arm64 | aarch64) ARCH="arm64" ;;
  *)
    echo "ERROR: unsupported architecture $(uname -m)" >&2
    exit 1
    ;;
  esac

  # The "latest" download path redirects to the newest release's assets, so no
  # API call is made and nothing has to be rate limited.
  releases="https://github.com/sigstore/cosign/releases"
  if [[ -n "$COSIGN_VERSION" ]]; then
    assets="${releases}/download/${COSIGN_VERSION}"
  else
    assets="${releases}/latest/download"
  fi

  tmpdir=$(mktemp -d /tmp/cosign-XXXXXX)
  trap 'rm -rf "${tmpdir}"' EXIT HUP INT TERM

  echo "==> Downloading cosign-linux-${ARCH} from ${assets}"
  curl -fsSL "${assets}/cosign-linux-${ARCH}" -o "${tmpdir}/cosign"
  curl -fsSL "${assets}/cosign_checksums.txt" -o "${tmpdir}/checksums.txt"

  # A tool whose whole purpose is to say what an artifact is gets the same
  # treatment: what was downloaded is checked against the release's own
  # checksums before it is installed.
  want=$(awk -v file="cosign-linux-${ARCH}" '$2 == file { print $1 }' "${tmpdir}/checksums.txt")
  if [[ -z "$want" ]]; then
    echo "ERROR: the release publishes no checksum for cosign-linux-${ARCH}" >&2
    exit 1
  fi
  got=$(sha256sum "${tmpdir}/cosign" | awk '{ print $1 }')
  if [[ "$want" != "$got" ]]; then
    echo "ERROR: cosign-linux-${ARCH} is not what the release says it is:" >&2
    echo "  expected ${want}" >&2
    echo "  got      ${got}" >&2
    exit 1
  fi
  echo "==> Checksum verified, installing to /usr/local/bin/cosign"
  sudo install -m 0755 "${tmpdir}/cosign" /usr/local/bin/cosign
fi

echo "==> cosign version: $(cosign_version)"
echo "==> cosign installed at: $(command -v cosign)"
