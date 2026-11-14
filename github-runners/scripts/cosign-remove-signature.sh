#!/usr/bin/env bash
set -euo pipefail

# Save an image's cosign signature and then take it away, so that a consumer
# that verifies the image can be seen to refuse it. Restore it afterwards with
# cosign-restore-signature.sh, which reads the copy this saves.
#
#   cosign-remove-signature.sh [-d <dir>] <registry>/<image>[:<tag>]
#
#     -d <dir>  where to save the signature, default COSIGN_SIGNATURE_DIR.
#
# The signature is emptied by overwriting the registry tag that holds it, not
# by deleting anything, so this works against a registry with deletes
# disabled; see cosign-signature-lib.sh. The image itself is untouched, so the
# saved copy remains valid for as long as the tag resolves to the same digest.
#
# Nothing else removes a signature here: "cosign clean" needs a registry that
# allows deletes, and in cosign v3.1.4 it also ignores --allow-http-registry
# when it connects to delete.

usage() {
  echo "usage: $(basename "$0") [-d <dir>] <registry>/<image>[:<tag>]" >&2
  exit 2
}

dir=
while getopts ':d:h' opt; do
  case "${opt}" in
  d) dir=${OPTARG} ;;
  *) usage ;;
  esac
done
shift $((OPTIND - 1))
[[ $# -eq 1 ]] || usage

self_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=cosign-signature-lib.sh
source "${self_dir}/cosign-signature-lib.sh"

[[ -z "${dir}" ]] || COSIGN_SIGNATURE_DIR=${dir}

sig_need curl jq
sig_parse "$1"
sig_resolve
sig_saved_file

tmp=$(mktemp "${TMPDIR:-/tmp}/cosign-remove.XXXXXXXX")
trap 'rm -f "${tmp}"' EXIT HUP INT TERM
sig_fetch_index "${tmp}"

# Refuse to save an already-emptied index over a good saved copy, which is what
# a second run would otherwise do, losing the signature for good.
count=$(jq '.manifests | length' "${tmp}")
if [[ "${count}" -eq 0 ]]; then
  echo "${sig_ref} (${sig_digest}) already has no signatures"
  [[ -f "${sig_file}" ]] && echo "the saved copy is ${sig_file}"
  exit 0
fi

mkdir -p "${COSIGN_SIGNATURE_DIR}"
cp "${tmp}" "${sig_file}"
echo "==> saved ${count} signature(s) of ${sig_digest} to ${sig_file}"

jq -c '.manifests = []' "${sig_file}" >"${tmp}"
sig_put_index "${tmp}"

remaining=$(sig_count_signatures)
[[ "${remaining}" -eq 0 ]] ||
  sig_die "${sig_ref}: ${remaining} signature(s) are still attached; the registry did not take the change"
echo "==> removed the signature of ${sig_ref} (${sig_digest})"
echo "    restore it with: ${self_dir}/cosign-restore-signature.sh ${sig_ref}"
