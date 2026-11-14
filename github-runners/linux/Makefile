ifneq (,$(wildcard ./runner-env))
    include runner-env
endif

VM_NAME        ?= linux-ci
BASE_IMAGE     ?= ghcr.io/cirruslabs/ubuntu:latest
VM_SSH_PUB_KEY ?=
HOST           ?= mm-1:5001
REGISTRY       ?= $(HOST)
TAG            ?= latest
IMAGE          ?= $(VM_NAME)
CONCURRENCY       ?= 16
PULL_CONCURRENCY  ?= 32

# tart refuses to clone over a VM that already exists and tells you to use
# --overwrite, but the packer plugin never passes that: it has no option for it
# and does not act on packer's own -force either. So the VM is deleted here
# before the build clones it. Set OVERWRITE empty to have a rebuild stop
# instead, leaving the VM that is already there alone.
OVERWRITE ?= yes

# Extra flags for packer build, eg. -debug or -on-error=ask.
BUILD_FLAGS ?=

.PHONY: init validate build push clean

init:
	packer init build.pkr.hcl

validate: init
	packer validate \
	  -var "vm_name=$(VM_NAME)" \
	  -var "base_image=$(BASE_IMAGE)" \
	  -var "pull_concurrency=$(PULL_CONCURRENCY)" \
	  build.pkr.hcl

build: init
ifneq ($(OVERWRITE),)
	-tart delete $(VM_NAME) 2>/dev/null
endif
	packer build $(BUILD_FLAGS) \
	  -var "vm_name=$(VM_NAME)" \
	  -var "base_image=$(BASE_IMAGE)" \
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

