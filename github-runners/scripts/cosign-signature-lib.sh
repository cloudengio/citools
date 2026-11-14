# shellcheck shell=bash
#
# Shared by cosign-remove-signature.sh and cosign-restore-signature.sh, which
# between them take an image's cosign signature away and put it back so that
# the failure path of a verifying consumer can be exercised. The two halves
# must agree exactly on which registry object holds the signature and on where
# the saved copy of it goes, which is why that logic lives here.
#
# A cosign v3 signature is an OCI referrer: an index stored in the image's own
# repository under the tag sha256-<image digest>, whose entries are the
# signature bundles. Emptying that index is what hides the signature, and it is
# done by overwriting the tag rather than by deleting anything, so this works
# against a registry that has deletes disabled, which is what "cosign clean"
# needs. The image itself is never touched: its digest, and so every other
# signature over it, is unchanged.
#
# Environment:
#   COSIGN_REGISTRY_SCHEME  http (the default) or https.
#   COSIGN_SIGNATURE_DIR    where the saved signature indexes are kept.

: "${COSIGN_REGISTRY_SCHEME:=http}"
: "${COSIGN_SIGNATURE_DIR:=${TMPDIR:-/tmp}/cosign-signatures}"

# The manifest types a registry may answer a tag with, so that the digest the
# tag resolves to is the one every other client would see.
sig_accept='application/vnd.oci.image.index.v1+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json,application/vnd.docker.distribution.manifest.list.v2+json'
sig_index_type='application/vnd.oci.image.index.v1+json'

sig_die() {
  echo "$*" >&2
  exit 1
}

# sig_need <command...> fails unless every command is installed.
sig_need() {
  local cmd
  for cmd in "$@"; do
    command -v "${cmd}" >/dev/null || sig_die "${cmd} is required but was not found in PATH"
  done
}

# sig_parse <registry>/<image>[:<tag>] sets sig_registry, sig_image, sig_tag,
# sig_ref and sig_url. The tag defaults to latest. A digest cannot be given:
# the point of resolving a tag here is to learn which digest it names now.
sig_parse() {
  local ref=$1 rest
  [[ "${ref}" == */* ]] || sig_die "${ref}: no registry; name the image as <registry>/<image>[:<tag>]"
  sig_registry=${ref%%/*}
  rest=${ref#*/}
  [[ "${rest}" != *@* ]] || sig_die "${ref}: name a tag, not a digest"
  if [[ "${rest}" == *:* ]]; then
    sig_image=${rest%:*}
    sig_tag=${rest##*:}
  else
    sig_image=${rest}
    sig_tag=latest
  fi
  sig_ref="${sig_registry}/${sig_image}:${sig_tag}"
  sig_url="${COSIGN_REGISTRY_SCHEME}://${sig_registry}/v2/${sig_image}"
}

# sig_resolve resolves the parsed tag to a digest and sets sig_digest and
# sig_sigtag, the tag under which the signature of that digest is stored.
sig_resolve() {
  local headers digest errfile why
  # curl's own diagnosis of a failure is carried in the message, since it
  # distinguishes a tag that is not there from a registry that is not. It is
  # kept apart from the headers, which curl writes to its standard output.
  errfile=$(mktemp "${TMPDIR:-/tmp}/cosign-sig.XXXXXXXX")
  if ! headers=$(curl -fsS -I -H "Accept: ${sig_accept}" "${sig_url}/manifests/${sig_tag}" 2>"${errfile}"); then
    why=$(tr '\n' ' ' <"${errfile}")
    rm -f "${errfile}"
    sig_die "${sig_ref}: could not be read over ${COSIGN_REGISTRY_SCHEME}: ${why}"
  fi
  rm -f "${errfile}"
  digest=$(printf '%s\n' "${headers}" |
    awk 'tolower($1) == "docker-content-digest:" { sub(/\r/, "", $2); print $2 }')
  [[ "${digest}" == sha256:* ]] || sig_die "${sig_ref}: the registry did not report a digest for it"
  sig_digest=${digest}
  sig_sigtag="sha256-${digest#sha256:}"
}

# sig_saved_file sets sig_file to where the signature of the resolved digest is
# saved. It is named after the digest, so a signature is only ever restored
# onto the image it was taken from, and re-pushing the image in between leaves
# the saved copy visibly unused rather than silently restored over the new one.
sig_saved_file() {
  local name
  name=$(printf '%s/%s' "${sig_registry}" "${sig_image}" | tr '/:' '__')
  # shellcheck disable=SC2034 # read by the scripts that source this file.
  sig_file="${COSIGN_SIGNATURE_DIR}/${name}.${sig_sigtag}.json"
}

# sig_fetch_index <file> writes the signature index of the resolved digest to
# file, or fails if there is none.
sig_fetch_index() {
  # An absent index is the ordinary case of an unsigned image, so curl's 404 is
  # left unsaid in favour of what it means here.
  curl -fsS -H "Accept: ${sig_accept}" "${sig_url}/manifests/${sig_sigtag}" -o "$1" 2>/dev/null ||
    sig_die "${sig_ref} (${sig_digest}) has no signature attached: nothing to do"
}

# sig_put_index <file> stores file as the signature index of the resolved
# digest, which is what both removing and restoring come down to.
sig_put_index() {
  local type out
  type=$(jq -r '.mediaType // empty' "$1")
  out=$(curl -fsS -o /dev/null -X PUT -H "Content-Type: ${type:-${sig_index_type}}" \
    --data-binary "@$1" "${sig_url}/manifests/${sig_sigtag}" 2>&1) ||
    sig_die "${sig_ref}: the registry refused the signature index, it may be read-only: ${out}"
}

# sig_count_signatures reports how many signatures the resolved digest now has,
# read back from the registry rather than assumed.
sig_count_signatures() {
  local tmp count
  tmp=$(mktemp "${TMPDIR:-/tmp}/cosign-sig.XXXXXXXX")
  if curl -fsS -H "Accept: ${sig_accept}" "${sig_url}/manifests/${sig_sigtag}" -o "${tmp}" 2>/dev/null; then
    count=$(jq '.manifests | length' "${tmp}")
  else
    count=0
  fi
  rm -f "${tmp}"
  echo "${count}"
}
