ifneq (,$(wildcard ./.env))
    include runner-env
endif

VM_NAME       ?= macos-ci
MACOS_VERSION ?= sequoia
VM_SSH_PUB_KEY ?= ""

# Required for the 'run' and 'configure-runner' targets.
RUNNER_URL   ?=
RUNNER_TOKEN ?=
RUNNER_NAME  ?=
RUNNER_LABELS ?=

.PHONY: init validate build push clean run configure-runner

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

# Push the VM image to a registry, e.g.:
#   make push REGISTRY=ghcr.io/your-org
push:
	tart push $(VM_NAME) $(REGISTRY)/$(VM_NAME):latest

clean:
	tart delete $(VM_NAME) || true

# Start a cloned, ephemeral VM in the background.
# The VM name is suffixed with a timestamp to avoid collisions.
run:
	$(eval INSTANCE := $(VM_NAME)-$(shell date +%s))
	tart clone $(VM_NAME) $(INSTANCE)
	tart run --no-graphics $(INSTANCE) &
	@echo "VM instance: $(INSTANCE)"
	@echo "Run 'make configure-runner VM_NAME=$(INSTANCE) RUNNER_URL=... RUNNER_TOKEN=...' next."

# Write the runner config file into a running VM over SSH, then restart the
# launchd agent so it picks up the new config.
# Requires: RUNNER_URL, RUNNER_TOKEN (RUNNER_NAME and RUNNER_LABELS optional).
#   make configure-runner VM_NAME=<instance> RUNNER_URL=https://github.com/ORG/REPO RUNNER_TOKEN=<token>
configure-runner:
	@test -n "$(RUNNER_URL)"   || (echo "RUNNER_URL is required"   && exit 1)
	@test -n "$(RUNNER_TOKEN)" || (echo "RUNNER_TOKEN is required" && exit 1)
	$(eval SSH := ssh -o StrictHostKeyChecking=no -o ConnectTimeout=30 admin@$(shell tart ip $(VM_NAME) --wait 30))
	$(SSH) "printf 'RUNNER_URL=%s\nRUNNER_TOKEN=%s\nRUNNER_NAME=%s\nRUNNER_LABELS=%s\n' \
	  '$(RUNNER_URL)' '$(RUNNER_TOKEN)' '$(RUNNER_NAME)' '$(RUNNER_LABELS)' \
	  > /Users/admin/.github-runner-config"
	$(SSH) "launchctl kickstart -k gui/$$(id -u admin)/com.github.actions.runner" || true
