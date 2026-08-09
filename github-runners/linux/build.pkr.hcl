packer {
  required_plugins {
    tart = {
      version = ">= 1.21.0"
      source  = "github.com/cirruslabs/tart"
    }
  }
}

variable "base_image" {
  description = "Base tart image (cirruslabs Ubuntu). See https://github.com/cirruslabs/linux-image-templates"
  default     = "ghcr.io/cirruslabs/ubuntu:latest"
}

variable "vm_name" {
  description = "Name of the resulting VM image"
  default     = "linux-ci"
}

variable "cpu_count" {
  default = 4
}

variable "memory_gb" {
  default = 8
}

variable "disk_size_gb" {
  default = 50
}

variable "ssh_public_key" {
  description = "SSH public key to add to authorized_keys for passwordless access"
  type        = string
  default     = ""
}

# cirruslabs Ubuntu base image. On Apple Silicon hosts tart runs an arm64 guest;
# the provisioning scripts detect the architecture at build time. Note that
# Chrome for Testing has no linux/arm64 build, so 07-chrome-for-testing.sh is a
# no-op on arm64 (see the script for details).
source "tart-cli" "linux" {
  vm_base_name = var.base_image
  vm_name      = var.vm_name
  cpu_count    = var.cpu_count
  memory_gb    = var.memory_gb
  disk_size_gb = var.disk_size_gb
  ssh_username = "admin"
  ssh_password = "admin"
  ssh_timeout  = "120s"

  # Run the build VM without a graphical console (tart run --no-graphics).
  headless = true
}

build {
  sources = ["source.tart-cli.linux"]

  provisioner "shell" {
    script = "scripts/00-apt-base.sh"
  }

  provisioner "shell" {
    script = "scripts/01-go.sh"
  }

  provisioner "shell" {
    script = "scripts/02-github-runner.sh"
  }

  provisioner "shell" {
    script = "scripts/03-github-runner-service.sh"
  }

  provisioner "shell" {
    script = "scripts/04-ssh-keys.sh"
    environment_vars = [
      "SSH_PUBLIC_KEY=${var.ssh_public_key}"
    ]
  }

  provisioner "shell" {
    script = "scripts/05-mkcert.sh"
  }

  provisioner "shell" {
    script = "scripts/06-hosts.sh"
  }

  provisioner "shell" {
    script = "scripts/07-chrome-for-testing.sh"
  }

  provisioner "shell" {
    script = "scripts/08-pebble-minica.sh"
  }

  provisioner "shell" {
    script = "scripts/09-docker.sh"
  }

  provisioner "shell" {
    script = "scripts/10-npm.sh"
  }
}
