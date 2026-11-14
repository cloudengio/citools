# Signing of the pushed VM images with cosign (https://docs.sigstore.dev).
#
# A cosign signature is itself an OCI artifact, stored in the registry beside
# the image it signs and named after that image's digest, so an image can only
# be signed once it has been pushed. This file is included by the macos and
# linux image Makefiles, which define REGISTRY, IMAGE and TAG before including
# it; the signature covers the digest that REGISTRY/IMAGE:TAG resolves to at
# the time of signing.
#
# The flags used here are those of cosign v3 (brew install cosign).
#
# COSIGN_MODE picks what signs:
#
#   key      a key pair made once by "make cosign-keys" and held in the
#            keychain, never on disk: each half is read from the keychain and
#            piped to cosign when it is needed, see COSIGN_KEY_CMD. Nothing
#            leaves the host but the signature, and verification needs only
#            the public key.
#
#   keyless  an OIDC identity, eg. a Google login, certified by Fulcio for the
#            few minutes it takes to sign. There is no key to keep or to
#            distribute, but see the warnings below: it signs interactively and
#            it publishes what was signed.
#
# What consumes these signatures is the verification policy of the image, ie.
# ociimage.Source.Verify, which the orchestrator's pools name as:
#
#   tart_config:
#     image: mm-1:5001/macos-ci:latest
#     verify:
#       signers:                                    # key mode
#         - key: |                                  # "make cosign-pub-key"
#             -----BEGIN PUBLIC KEY-----
#             ...
#             -----END PUBLIC KEY-----
#
#   tart_config:
#     image: mm-1:5001/macos-ci:latest
#     verify:
#       signers:                                    # keyless mode
#         - issuer: https://oauth2.sigstore.dev/auth
#           subject: cnicolaou@cloudeng.io
#       require_transparency_log: true
#
# A policy that requires annotations needs the matching COSIGN_ANNOTATIONS.

COSIGN      ?= cosign
COSIGN_MODE ?= key

# cosign_mk_dir is this file's own directory, which is where the signing config
# below is kept so that both images share one. It has to be assigned here,
# while this file is the one being read.
cosign_mk_dir := $(dir $(lastword $(MAKEFILE_LIST)))

# The key pair lives in the keychain and nowhere else. cosign has no key store
# of its own and reads a key from a file, so each half is read out of the
# keychain and piped to it as /dev/stdin: no copy of the key is written to
# disk, not even while it is being generated, see cosign-keys.
#
# COSIGN_KEY_CMD and COSIGN_PUB_KEY_CMD write a key to their standard output,
# and the matching WRITE commands store what they read on their standard
# input, which only cosign-keys uses. Another store can be used by setting the
# four of them to its own commands.
COSIGN_KEYCHAIN_KEY ?= cosign-citools-runners-key
COSIGN_KEYCHAIN_PUB ?= cosign-citools-runners-pub

# The filename comes last and the flags before it, as "cloudeng-keychain
# write --help" says: a flag after the filename is a second filename, which
# the command refuses.
COSIGN_KEY_CMD           ?= cloudeng-keychain read --keychain-item=$(COSIGN_KEYCHAIN_KEY) -
COSIGN_PUB_KEY_CMD       ?= cloudeng-keychain read --keychain-item=$(COSIGN_KEYCHAIN_PUB) -
COSIGN_KEY_WRITE_CMD     ?= cloudeng-keychain write --keychain-item=$(COSIGN_KEYCHAIN_KEY) -
COSIGN_PUB_KEY_WRITE_CMD ?= cloudeng-keychain write --keychain-item=$(COSIGN_KEYCHAIN_PUB) -

# The identity that signs in keyless mode, and the issuer that certifies it.
# The default issuer is sigstore's own broker, which offers a Google login
# among others; the certificate then records the broker as the issuer and the
# email as the subject, which is what a policy has to name. To have the
# certificate name Google itself as the issuer, set COSIGN_OIDC_ISSUER to
# https://accounts.google.com and pass a Google ID token with an audience of
# "sigstore" in COSIGN_IDENTITY_TOKEN.
COSIGN_IDENTITY       ?= cnicolaou@cloudeng.io
COSIGN_OIDC_ISSUER    ?= https://oauth2.sigstore.dev/auth
COSIGN_IDENTITY_TOKEN ?=
# normal opens a browser on this host; device prints a code to enter on another
# one, which is what signing over ssh needs.
COSIGN_AUTH_FLOW ?= normal

# The registry is served over plain HTTP, as the --insecure given to tart push
# says, so cosign is told to accept it as well.
COSIGN_REGISTRY_FLAGS ?= --allow-insecure-registry --allow-http-registry

ifeq ($(COSIGN_MODE),keyless)

cosign_signer_flags = --fulcio-auth-flow "$(COSIGN_AUTH_FLOW)" \
  --oidc-issuer "$(COSIGN_OIDC_ISSUER)" \
  $(if $(COSIGN_IDENTITY_TOKEN),--identity-token "$(COSIGN_IDENTITY_TOKEN)",)
cosign_verifier_flags = --certificate-identity "$(COSIGN_IDENTITY)" \
  --certificate-oidc-issuer "$(COSIGN_OIDC_ISSUER)"

# A keyless certificate is valid for minutes, so a signature made with one can
# only be verified against a record of when it was made. Rekor, the public
# transparency log, is that record and is therefore not optional here: the
# image's reference and digest and the signing identity become public and
# permanent. Verification needs the internet too, for sigstore's trust root.
COSIGN_TLOG_SIGN_FLAGS   ?=
COSIGN_TLOG_VERIFY_FLAGS ?=

else ifeq ($(COSIGN_MODE),key)

# Each half of the key pair is read into a shell variable and piped to cosign,
# which is given the pipe as the key file. The key is checked for here rather
# than left to cosign, which reports an empty key as a malformed one.
cosign_signer_flags   = --key /dev/stdin
cosign_verifier_flags = --key /dev/stdin
cosign_sign_key = set -e; \
	  key=$$($(COSIGN_KEY_CMD) || true); \
	  test -n "$$key" || { echo "no signing key in $(COSIGN_KEYCHAIN_KEY): run 'make cosign-keys'"; exit 1; }; \
	  printf '%s\n' "$$key" |
cosign_verify_key = set -e; \
	  key=$$($(COSIGN_PUB_KEY_CMD) || true); \
	  test -n "$$key" || { echo "no public key in $(COSIGN_KEYCHAIN_PUB): run 'make cosign-keys'"; exit 1; }; \
	  printf '%s\n' "$$key" |

# The keychain is what protects the key, so it carries no password of its own.
# cosign has to be told that: left to ask, it would prompt for a password on
# the terminal while the key is on its standard input. The assignment sits
# immediately before the cosign command, which is on the far side of the pipe
# that carries the key.
cosign_no_password = COSIGN_PASSWORD=""

# Rekor is of no use for images in a private registry and cannot be reached
# from one, so a key signature is kept local. Verification has to be told the
# same thing, hence the pair of settings.
#
# Left to itself cosign v3 fetches sigstore's own signing config and uploads
# the signature to the public Rekor, which publishes the image's reference and
# digest permanently, takes some 30 seconds and needs the internet. It is told
# not to by being given a signing config that names no services at all;
# --tlog-upload=false, which said this in cosign v2, is deprecated in v3 and
# refused outright alongside a signing config.
COSIGN_SIGNING_CONFIG    ?= $(cosign_mk_dir)cosign-signing-config.json
COSIGN_TLOG_SIGN_FLAGS   ?= --signing-config "$(COSIGN_SIGNING_CONFIG)"
COSIGN_TLOG_VERIFY_FLAGS ?= --insecure-ignore-tlog=true

# sign needs that signing config to exist, see cosign-signing-config. Keyless
# signing wants the opposite, sigstore's own services, so it asks for nothing
# here and lets cosign fetch them.
cosign_sign_prereqs = $(COSIGN_SIGNING_CONFIG)

else
$(error COSIGN_MODE is "$(COSIGN_MODE)", want key or keyless)
endif

# COSIGN_ANNOTATIONS are signed with the image as "key=value" pairs, for a
# policy that requires them, eg. COSIGN_ANNOTATIONS="tier=trusted built_by=ci".
COSIGN_ANNOTATIONS ?=
cosign_annotation_flags = $(foreach a,$(COSIGN_ANNOTATIONS),-a "$(a)")

# Extra flags for signing and verifying, eg. a timestamp authority in place of
# the transparency log.
COSIGN_SIGN_FLAGS   ?=
COSIGN_VERIFY_FLAGS ?=

# cosign_scrub defines the shell function that cosign-keys removes the
# generated key files with: each one is overwritten with as many random bytes
# as it holds, flushed, and then unlinked, so that the key is not left behind
# in the blocks the file occupied.
#
# This makes the key harder to recover, rather than impossible: a
# copy-on-write filesystem such as APFS may put the overwriting bytes in new
# blocks and leave the old ones to be reused in their own time, and an SSD's
# own remapping is invisible from here. What does protect those blocks is
# FileVault. rm -P is not used because it no longer overwrites anything: macOS
# keeps the flag for compatibility and documents it as having no effect.
cosign_scrub = scrub() { \
	    for f in "$$@"; do \
	      test -f "$$f" || continue; \
	      n=$$(wc -c < "$$f" | tr -d '[:space:]'); \
	      dd if=/dev/urandom of="$$f" bs=1 count="$$n" conv=notrunc 2>/dev/null || \
	        echo "warning: $$f was not overwritten before being removed" >&2; \
	      sync; \
	      rm -f "$$f"; \
	    done; \
	  };

# IMAGE_REF is the image as the registry names it, which is what verify
# checks: a consumer of the image names the tag too, so that is the case worth
# testing.
IMAGE_REF = $(REGISTRY)/$(IMAGE):$(TAG)

# What is signed, though, is the digest that tag resolves to, since a tag can
# be moved between the push and the signature, and cosign warns that it will
# stop accepting a tag altogether. It is also what the orchestrator ends up
# using, see ociimage.Pin. $$digest is the shell variable that
# cosign_resolve_digest sets, so this belongs in a recipe that begins with it.
IMAGE_DIGEST_REF = $(REGISTRY)/$(IMAGE)@$$digest

# The media types a registry has to be asked for, so that it answers with the
# manifest the tag really names and so with that manifest's own digest.
cosign_manifest_accept = application/vnd.oci.image.index.v1+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.docker.distribution.manifest.v2+json

# COSIGN_REGISTRY_SCHEME is how the registry is asked, plain HTTP as the
# --insecure given to tart push and COSIGN_REGISTRY_FLAGS above both say.
COSIGN_REGISTRY_SCHEME ?= http

# COSIGN_DIGEST_CMD prints the digest of REGISTRY/IMAGE:TAG and nothing else.
# Replace it for a registry that wants credentials or a client certificate, eg.
# with "crane digest $(IMAGE_REF)".
COSIGN_DIGEST_CMD ?= curl -fsS -I -H "Accept: $(cosign_manifest_accept)" \
	  "$(COSIGN_REGISTRY_SCHEME)://$(REGISTRY)/v2/$(IMAGE)/manifests/$(TAG)" | \
	  awk 'tolower($$1) == "docker-content-digest:" { sub(/\r/, "", $$2); print $$2 }'

cosign_resolve_digest = set -e; \
	  digest=$$($(COSIGN_DIGEST_CMD)); \
	  case "$$digest" in \
	  sha256:*) ;; \
	  *) echo "could not resolve $(IMAGE_REF) to a digest: is the registry reachable, and the image pushed?"; exit 1 ;; \
	  esac;

.PHONY: sign verify cosign-signature cosign-keys cosign-pub-key cosign-check

# cosign-check reports what is easy to get wrong, a missing cosign and, in key
# mode, a missing signing key, before a command fails part way through: push
# depends on it so that it does not upload an image it cannot then sign.
cosign-check:
	@command -v $(COSIGN) >/dev/null 2>&1 || \
	  { echo "cosign is not installed: brew install cosign"; exit 1; }
ifeq ($(COSIGN_MODE),key)
	@set -e; key=$$($(COSIGN_KEY_CMD) || true); \
	  test -n "$$key" || \
	    { echo "no signing key in $(COSIGN_KEYCHAIN_KEY): run 'make cosign-keys'"; exit 1; }
endif

# sign signs the pushed image, by the digest its tag resolves to, see
# IMAGE_DIGEST_REF. It is run by push, and separately when an image that is
# already in the registry needs a signature.
sign: cosign-check $(cosign_sign_prereqs)
	$(cosign_resolve_digest) $(cosign_sign_key) $(cosign_no_password) \
	  $(COSIGN) sign --yes $(cosign_signer_flags) \
	  $(COSIGN_TLOG_SIGN_FLAGS) $(COSIGN_REGISTRY_FLAGS) \
	  $(cosign_annotation_flags) $(COSIGN_SIGN_FLAGS) \
	  $(IMAGE_DIGEST_REF)

# verify checks the signature of the image in the registry against the key or
# the identity that is trusted, which is what a consumer of the image runs. It
# needs the public key alone, never the signing key.
verify:
	@command -v $(COSIGN) >/dev/null 2>&1 || \
	  { echo "cosign is not installed: brew install cosign"; exit 1; }
	$(cosign_verify_key) $(COSIGN) verify $(cosign_verifier_flags) \
	  $(COSIGN_TLOG_VERIFY_FLAGS) $(COSIGN_REGISTRY_FLAGS) $(COSIGN_VERIFY_FLAGS) \
	  $(IMAGE_REF)

# The signing config that keeps a key signature out of the transparency log,
# see COSIGN_SIGNING_CONFIG. cosign writes it, rather than it being written out
# here, so that it stays whatever cosign's current format is; it holds no more
# than a media type and two empty service lists, and no secret, but it is not
# checked in because it is generated.
$(COSIGN_SIGNING_CONFIG):
	@command -v $(COSIGN) >/dev/null 2>&1 || \
	  { echo "cosign is not installed: brew install cosign"; exit 1; }
	$(COSIGN) signing-config create --out $@

# cosign-signature shows the signature that is attached to the image, which is
# what to look at when verify refuses one: the artifacts cosign finds, then the
# digest the tag resolves to, and for each signature its bundle, the key that
# made it and the digest it commits to. That digest is the one that matters: a
# signature is attached to a digest, so re-pushing a tag leaves the old
# signature behind on the image it was made for.
#
# This reads the registry directly, with curl and jq, because "cosign download
# signature" looks for the signature under the name cosign v2 gave it and v3 no
# longer writes that name.
cosign-signature:
	@command -v jq >/dev/null 2>&1 || \
	  { echo "jq is not installed: brew install jq"; exit 1; }
	-$(COSIGN) tree $(COSIGN_REGISTRY_FLAGS) $(IMAGE_REF)
	@$(cosign_resolve_digest) \
	  base="$(COSIGN_REGISTRY_SCHEME)://$(REGISTRY)/v2/$(IMAGE)"; \
	  echo "$(IMAGE_REF) is $$digest"; \
	  sigs=$$(curl -fsS -H "Accept: $(cosign_manifest_accept)" \
	    "$$base/manifests/$$(echo "$$digest" | tr ':' '-')" 2>/dev/null | \
	    jq -r '.manifests[]?.digest'); \
	  test -n "$$sigs" || { echo "no signature is attached to that digest"; exit 1; }; \
	  for sig in $$sigs; do \
	    echo; \
	    echo "signature $$sig"; \
	    blob=$$(curl -fsS -H "Accept: $(cosign_manifest_accept)" "$$base/manifests/$$sig" | \
	      jq -r '.layers[0].digest'); \
	    curl -fsS "$$base/blobs/$$blob" | jq '{bundle: .mediaType, \
	      key: .verificationMaterial.publicKey.hint, \
	      signs: (.dsseEnvelope.payload | @base64d | fromjson | .subject)}'; \
	  done

# cosign-pub-key writes the public key, which is what a verification policy
# needs: "make cosign-pub-key" to read it, and the orchestrator's pools carry
# it as the key of a signer, see the policy above.
cosign-pub-key:
	@set -e; key=$$($(COSIGN_PUB_KEY_CMD) || true); \
	  test -n "$$key" || \
	    { echo "no public key in $(COSIGN_KEYCHAIN_PUB): run 'make cosign-keys'"; exit 1; }; \
	  printf '%s\n' "$$key"

# cosign-keys creates the signing key pair, once, and stores it in the
# keychain. An existing key is never overwritten: it would invalidate every
# signature made with it. Keyless signing needs no key, so this is for
# COSIGN_MODE=key.
#
# cosign can only write a new key pair to files, so the pair is generated in a
# directory of its own under TMPDIR, stored, and the files overwritten and
# removed before the target finishes, whether it succeeds, fails or is
# interrupted, see cosign_scrub. That is the only moment at which the key is on
# disk, and it is as private as TMPDIR, so a shared TMPDIR is not the place to
# do this; setting TMPDIR for this one command is how to generate the pair
# somewhere else, such as a volume held in memory. The directory is named
# explicitly because mktemp -d ignores TMPDIR when it is given no template.
cosign-keys:
	@command -v $(COSIGN) >/dev/null 2>&1 || \
	  { echo "cosign is not installed: brew install cosign"; exit 1; }
	@set -e; key=$$($(COSIGN_KEY_CMD) 2>/dev/null || true); \
	  test -z "$$key" || \
	    { echo "$(COSIGN_KEYCHAIN_KEY) already holds a key: delete the item by hand to replace it, which makes every signature already made with it unverifiable"; exit 1; }
	set -e; \
	  $(cosign_scrub) \
	  dir=$$(mktemp -d "$${TMPDIR:-/tmp}/cosign.XXXXXXXX"); \
	  trap 'scrub "$$dir/cosign.key" "$$dir/cosign.pub"; rmdir "$$dir" 2>/dev/null || true' EXIT HUP INT TERM; \
	  (cd "$$dir" && $(cosign_no_password) $(COSIGN) generate-key-pair --output-key-prefix cosign); \
	  $(COSIGN_KEY_WRITE_CMD) < "$$dir/cosign.key"; \
	  $(COSIGN_PUB_KEY_WRITE_CMD) < "$$dir/cosign.pub"; \
	  stored=$$($(COSIGN_KEY_CMD) || true); \
	  test -n "$$stored" || { echo "the key does not read back from $(COSIGN_KEYCHAIN_KEY)"; exit 1; }
	@echo "stored the key pair in $(COSIGN_KEYCHAIN_KEY) and $(COSIGN_KEYCHAIN_PUB), and left nothing on disk"
	@echo "run 'make cosign-pub-key' for the public key that a verification policy needs"
