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
	packer build \
	  -var "vm_name=$(VM_NAME)" \
	  -var "base_image=$(BASE_IMAGE)" \
	  -var "ssh_public_key=$(VM_SSH_PUB_KEY)" \
	  -var "pull_concurrency=$(PULL_CONCURRENCY)" \
	  build.pkr.hcl

push:
	tart push $(IMAGE) $(REGISTRY)/$(IMAGE):$(TAG) --insecure --concurrency $(CONCURRENCY)

clean:
	tart delete $(VM_NAME) || true

