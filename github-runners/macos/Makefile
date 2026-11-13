ifneq (,$(wildcard ./runner-env))
    include runner-env
endif

VM_NAME       ?= macos-ci
MACOS_VERSION ?= tahoe
VM_SSH_PUB_KEY ?=
HOST          ?= mm-1:5001
REGISTRY      ?= $(HOST)
TAG           ?= latest
IMAGE         ?= $(VM_NAME)
CONCURRENCY      ?= 16
PULL_CONCURRENCY ?= 32

.PHONY: init validate build push clean

init:
	packer init build.pkr.hcl

validate: init
	packer validate \
	  -var "vm_name=$(VM_NAME)" \
	  -var "macos_version=$(MACOS_VERSION)" \
	  -var "pull_concurrency=$(PULL_CONCURRENCY)" \
	  build.pkr.hcl

build: init
	packer build \
	  -var "vm_name=$(VM_NAME)" \
	  -var "macos_version=$(MACOS_VERSION)" \
	  -var "ssh_public_key=$(VM_SSH_PUB_KEY)" \
	  -var "pull_concurrency=$(PULL_CONCURRENCY)" \
	  build.pkr.hcl

# push publishes the image and then signs it in the registry, since a cosign
# signature names the digest of an image that is already there.
push: cosign-check
	tart push $(IMAGE) $(IMAGE_REF) --insecure --concurrency $(CONCURRENCY)
	$(MAKE) sign

clean:
	tart delete $(VM_NAME) || true

# Signing of the pushed image: the sign, verify and cosign-keys targets, and
# the COSIGN_* variables that configure them. Included last so that init stays
# the default goal.
include ../cosign.mk

