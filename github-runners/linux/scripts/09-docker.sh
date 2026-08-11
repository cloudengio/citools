#!/usr/bin/env bash
set -euo pipefail

# Install Docker Engine (plus the CLI, containerd, buildx and compose plugins)
# from Docker's official apt repository, and grant the runner user (admin)
# access to the daemon so CI jobs can run containers without sudo.
#
# Docker publishes arm64 packages, so unlike Chrome for Testing this works on the
# arm64 guest that tart runs on Apple Silicon as well as on x64.

export DEBIAN_FRONTEND=noninteractive

echo "==> Installing Docker apt repository prerequisites"
sudo apt-get update -y
sudo apt-get install -y --no-install-recommends ca-certificates curl

echo "==> Adding Docker's official GPG key"
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
  -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc

echo "==> Configuring the Docker apt repository"
ARCH="$(dpkg --print-architecture)"
CODENAME="$(. /etc/os-release && echo "${VERSION_CODENAME}")"
echo \
  "deb [arch=${ARCH} signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${CODENAME} stable" \
  | sudo tee /etc/apt/sources.list.d/docker.list >/dev/null

echo "==> Installing Docker Engine and plugins"
sudo apt-get update -y
sudo apt-get install -y --no-install-recommends \
  docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

echo "==> Enabling and starting docker and containerd at boot"
# `enable --now` both creates the boot-time symlinks AND starts the units in this
# build session, so we can smoke-test the daemon below. docker.socket must be
# enabled too: Docker is socket-activated on Ubuntu, and enabling only
# docker.service can leave the daemon un-started on boot.
sudo systemctl enable --now containerd.service
sudo systemctl enable --now docker.socket
sudo systemctl enable --now docker.service

# Confirm the boot-time symlinks actually exist (guards against a silent enable
# failure that would leave the daemon dead on the next boot).
sudo systemctl is-enabled docker.service docker.socket containerd.service

echo "==> Adding admin to the docker group (use docker without sudo)"
# Group membership takes effect on the runner's next login/session, which is how
# the orchestrator starts jobs, so no sudo is needed at job time.
sudo usermod -aG docker admin

echo "==> Creating admin's docker config dir (/home/admin/.docker)"
# The plain `docker` CLI tolerates a missing ~/.docker, but docker-SDK / context
# aware tooling (testcontainers, cli helpers) resolves the current context on
# startup and errors when the dir is absent. Pre-create it with a default
# config, owned by admin, so job-time clients find it.
sudo install -d -o admin -g admin -m 0700 /home/admin/.docker
printf '{}\n' | sudo tee /home/admin/.docker/config.json >/dev/null
sudo chown admin:admin /home/admin/.docker/config.json
sudo chmod 0600 /home/admin/.docker/config.json

echo "==> Docker version:"
docker --version
docker compose version || true

# Smoke-test the daemon. admin's docker group membership isn't active in this
# build session yet, so talk to the daemon via sudo. If the daemon didn't start,
# this fails the build instead of shipping a dead-daemon image.
echo "==> Verifying the docker daemon is up"
sudo systemctl is-active docker.service
sudo docker info >/dev/null
sudo docker run --rm hello-world

echo "==> Docker installed and daemon running"
