#!/usr/bin/env bash
set -euo pipefail

# Put back the cosign signature that cosign-remove-signature.sh took away,
# leaving the image exactly as it was signed.
#
#   cosign-restore-signature.sh [-d <dir>] [-f <file>] <registry>/<image>[:<tag>]
#
#     -d <dir>   where the saved signature was put, default COSIGN_SIGNATURE_DIR.
#     -f <file>  restore this saved index instead of the one named after the
#                image's current digest, for an image that was re-pushed since.
#
# The saved copy is named after the digest it was taken from, so an image that
# has been re-pushed in the meantime has no saved signature to restore: its new
# digest was never signed, and "make sign" is what signs it. The copy is kept
# after a restore, since restoring it does not consume it.

usage() {
  echo "usage: $(basename "$0") [-d <dir>] [-f <file>] <registry>/<image>[:<tag>]" >&2
  exit 2
}

dir=
file=
while getopts ':d:f:h' opt; do
  case "${opt}" in
  d) dir=${OPTARG} ;;
  f) file=${OPTARG} ;;
  *) usage ;;
  esac
done
shift $((OPTIND - 1))
[[ $# -eq 1 ]] || usage

# shellcheck source=cosign-signature-lib.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/cosign-signature-lib.sh"

[[ -z "${dir}" ]] || COSIGN_SIGNATURE_DIR=${dir}

sig_need curl jq
sig_parse "$1"
sig_resolve
sig_saved_file
[[ -z "${file}" ]] || sig_file=${file}

if [[ ! -f "${sig_file}" ]]; then
  echo "no saved signature for ${sig_ref} (${sig_digest}) at ${sig_file}" >&2
  if compgen -G "${COSIGN_SIGNATURE_DIR}/*.json" >/dev/null; then
    echo "saved signatures in ${COSIGN_SIGNATURE_DIR}, for other digests:" >&2
    ls -1 "${COSIGN_SIGNATURE_DIR}"/*.json >&2
    echo "pass one with -f if the image was re-pushed and you mean to reuse it" >&2
  fi
  exit 1
fi

saved=$(jq '.manifests | length' "${sig_file}")
[[ "${saved}" -gt 0 ]] || sig_die "${sig_file} holds no signatures: it was saved from an unsigned image"

sig_put_index "${sig_file}"

restored=$(sig_count_signatures)
[[ "${restored}" -eq "${saved}" ]] ||
  sig_die "${sig_ref}: ${restored} of ${saved} signature(s) are attached; the registry did not take the change"
echo "==> restored ${restored} signature(s) of ${sig_ref} (${sig_digest}) from ${sig_file}"
