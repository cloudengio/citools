# Signing of the pushed VM images with cosign (https://docs.sigstore.dev).
#
# A cosign signature is itself an OCI artifact, stored in the registry beside
# the image it signs and named after that image's digest, so an image can only
# be signed once it has been pushed. This file is included by the macos and
# linux image Makefiles, which define REGISTRY, IMAGE and TAG before including
# it; the signature covers the digest that REGISTRY/IMAGE:TAG resolves to at
# the time of signing.
#
# The flags used here are those of cosign v2 and later (brew install cosign).
#
# COSIGN_MODE picks what signs:
#
#   key      a key pair made once by "make cosign-keys", which lives outside
#            the repository. Nothing leaves the host but the signature, and
#            verification needs only the public key. cosign reads the key's
#            password from COSIGN_PASSWORD, so set that in runner-env (which
#            is not checked in) to sign without being prompted.
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
#         - key: ~/.cosign/citools-runners.pub
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

COSIGN_KEY     ?= $(HOME)/.cosign/citools-runners.key
COSIGN_PUB_KEY ?= $(COSIGN_KEY:.key=.pub)

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

cosign_signer_flags   = --key "$(COSIGN_KEY)"
cosign_verifier_flags = --key "$(COSIGN_PUB_KEY)"

# Rekor is of no use for images in a private registry and cannot be reached
# from one, so a key signature is kept local. Verification has to be told the
# same thing, hence the pair of flags.
COSIGN_TLOG_SIGN_FLAGS   ?= --tlog-upload=false
COSIGN_TLOG_VERIFY_FLAGS ?= --insecure-ignore-tlog=true

else
$(error COSIGN_MODE is "$(COSIGN_MODE)", want key or keyless)
endif

# COSIGN_ANNOTATIONS are signed with the image as "key=value" pairs, for a
# policy that requires them, eg. COSIGN_ANNOTATIONS="tier=trusted built_by=ci".
COSIGN_ANNOTATIONS ?=
cosign_annotation_flags = $(foreach a,$(COSIGN_ANNOTATIONS),-a "$(a)")

# Extra flags for signing and verifying, eg. a key provider such as a KMS URI
# in COSIGN_KEY, or a timestamp authority in place of the transparency log.
COSIGN_SIGN_FLAGS   ?=
COSIGN_VERIFY_FLAGS ?=

# IMAGE_REF is the image as the registry names it, which is what is signed.
IMAGE_REF = $(REGISTRY)/$(IMAGE):$(TAG)

.PHONY: sign verify cosign-keys cosign-check

# cosign-check reports what is easy to get wrong, a missing cosign and, in key
# mode, a missing signing key, before a command fails part way through: push
# depends on it so that it does not upload an image it cannot then sign.
cosign-check:
	@command -v $(COSIGN) >/dev/null 2>&1 || \
	  { echo "cosign is not installed: brew install cosign"; exit 1; }
ifeq ($(COSIGN_MODE),key)
	@test -f "$(COSIGN_KEY)" || \
	  { echo "no cosign key at $(COSIGN_KEY): run 'make cosign-keys', or set COSIGN_KEY"; exit 1; }
endif

# sign signs the pushed image. It is run by push, and separately when an image
# that is already in the registry needs a signature.
sign: cosign-check
	$(COSIGN) sign --yes $(cosign_signer_flags) \
	  $(COSIGN_TLOG_SIGN_FLAGS) $(COSIGN_REGISTRY_FLAGS) \
	  $(cosign_annotation_flags) $(COSIGN_SIGN_FLAGS) \
	  $(IMAGE_REF)

# verify checks the signature of the image in the registry against the key or
# the identity that is trusted, which is what a consumer of the image runs. It
# needs the public key alone, never the signing key.
verify:
	@command -v $(COSIGN) >/dev/null 2>&1 || \
	  { echo "cosign is not installed: brew install cosign"; exit 1; }
ifeq ($(COSIGN_MODE),key)
	@test -f "$(COSIGN_PUB_KEY)" || \
	  { echo "no cosign public key at $(COSIGN_PUB_KEY): set COSIGN_PUB_KEY"; exit 1; }
endif
	$(COSIGN) verify $(cosign_verifier_flags) \
	  $(COSIGN_TLOG_VERIFY_FLAGS) $(COSIGN_REGISTRY_FLAGS) $(COSIGN_VERIFY_FLAGS) \
	  $(IMAGE_REF)

# cosign-keys creates the signing key pair, once, in COSIGN_KEY's directory. An
# existing key is never overwritten: it would invalidate every signature made
# with it. Keyless signing needs no key, so this is for COSIGN_MODE=key.
cosign-keys:
	@command -v $(COSIGN) >/dev/null 2>&1 || \
	  { echo "cosign is not installed: brew install cosign"; exit 1; }
	@test ! -e "$(COSIGN_KEY)" || \
	  { echo "$(COSIGN_KEY) already exists, delete it by hand to replace it"; exit 1; }
	mkdir -p "$(dir $(COSIGN_KEY))"
	cd "$(dir $(COSIGN_KEY))" && \
	  $(COSIGN) generate-key-pair --output-key-prefix "$(basename $(notdir $(COSIGN_KEY)))"
	@echo "created $(COSIGN_KEY) and $(COSIGN_PUB_KEY); distribute the .pub to whatever verifies the images"
