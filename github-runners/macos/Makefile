ifneq (,$(wildcard ./.env))
    include runner-env
endif

VM_NAME       ?= macos-ci
MACOS_VERSION ?= sequoia
VM_SSH_PUB_KEY ?= ""

.PHONY: init validate build clean

init:
	packer init build.pkr.hcl

validate: init
	packer validate \
	  -var "vm_name=$(VM_NAME)" \
	  -var "macos_version=$(MACOS_VERSION)" \
	  build.pkr.hcl

build: init
	packer build \
	  -var "vm_name=$(VM_NAME)" \
	  -var "macos_version=$(MACOS_VERSION)" \
	  build.pkr.hcl

clean:
	tart delete $(VM_NAME) || true

