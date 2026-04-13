packer {
  required_plugins {
    tart = {
      version = ">= 1.14.0"
      source  = "github.com/cirruslabs/tart"
    }
  }
}

variable "macos_version" {
  description = "macOS version codename (e.g. sequoia, sonoma)"
  default     = "sequoia"
}

variable "vm_name" {
  description = "Name of the resulting VM image"
  default     = "macos-ci"
}

variable "cpu_count" {
  default = 4
}

variable "memory_gb" {
  default = 8
}

variable "disk_size_gb" {
  default = 70
}

variable "ssh_public_key" {
  description = "SSH public key to add to authorized_keys for passwordless access"
  type        = string
  default     = ""
}

# Base image from cirruslabs — minimal macOS with Homebrew and dev tools, no Xcode.
# Command Line Tools (macOS SDK + clang) are installed by 00-clt.sh.
# This is significantly smaller than the xcode variant (~50 GB vs ~140 GB disk).
# See https://github.com/cirruslabs/macos-image-templates for available tags.
source "tart-cli" "macos" {
  vm_base_name = "ghcr.io/cirruslabs/macos-${var.macos_version}-base:latest"
  vm_name      = var.vm_name
  cpu_count    = var.cpu_count
  memory_gb    = var.memory_gb
  disk_size_gb = var.disk_size_gb
  ssh_username = "admin"
  ssh_password = "admin"
  ssh_timeout  = "120s"
}

build {
  sources = ["source.tart-cli.macos"]

  provisioner "shell" {
    script = "scripts/00-clt.sh"
  }

  provisioner "shell" {
    script = "scripts/01-homebrew.sh"
  }

  provisioner "shell" {
    script = "scripts/02-bash.sh"
  }

  provisioner "shell" {
    script = "scripts/03-go.sh"
  }

  provisioner "shell" {
    script = "scripts/04-github-runner.sh"
  }

  provisioner "shell" {
    script = "scripts/05-github-runner-service.sh"
  }

  provisioner "shell" {
    script = "scripts/06-ssh-keys.sh"
    environment_vars = [
      "SSH_PUBLIC_KEY=${var.ssh_public_key}"
    ]
  }
}
