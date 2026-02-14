#!/usr/bin/env bash
set -euo pipefail

CONTAINER_NAME="${CONTAINER_NAME:-claude-dev}"
IMAGE_NAME="${IMAGE_NAME:-claude-devcontainer}"

# Auto-detect UID/GID and Docker socket GID
HOST_UID="$(id -u)"
HOST_GID="$(id -g)"
DOCKER_SOCK=/var/run/docker.sock
if [ -S "$DOCKER_SOCK" ]; then
    DOCKER_GID="$(stat -c '%g' "$DOCKER_SOCK")"
else
    DOCKER_GID=984
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEV_HOME="/home/dev"

# Always build (layer cache makes this fast when nothing changes)
docker build \
    --build-arg USER_UID="$HOST_UID" \
    --build-arg USER_GID="$HOST_GID" \
    --build-arg DOCKER_GID="$DOCKER_GID" \
    -t "$IMAGE_NAME" \
    "$SCRIPT_DIR"

# Remove pre-existing container with the same name
docker rm -f "$CONTAINER_NAME" 2>/dev/null || true

# Build mount arguments
mounts=(
    -v "$(pwd):/workspace"
    -v "$HOME/.cache/bazel:${DEV_HOME}/.cache/bazel"
    -v "$HOME/.cache/bazelisk:${DEV_HOME}/.cache/bazelisk:ro"
    -v "$HOME/.cargo:${DEV_HOME}/.cargo:ro"
    -v "$HOME/.rustup:${DEV_HOME}/.rustup:ro"
    -v "$HOME/go:${DEV_HOME}/go:ro"
    -v "$HOME/dev/go:${DEV_HOME}/gopath"
    -v "$HOME/.npm:${DEV_HOME}/.npm:ro"
    -v "$HOME/.cache/pnpm:${DEV_HOME}/.cache/pnpm:ro"
    -v "$HOME/.claude:${DEV_HOME}/.claude"
)

# Docker socket
if [ -S "$DOCKER_SOCK" ]; then
    mounts+=(-v "$DOCKER_SOCK:$DOCKER_SOCK")
fi

# Conditional mounts (only if they exist on host)
if [ -f "$HOME/.gitconfig" ]; then
    mounts+=(-v "$HOME/.gitconfig:${DEV_HOME}/.gitconfig:ro")
fi

if [ -d "$HOME/.config/jj" ]; then
    mounts+=(-v "$HOME/.config/jj:${DEV_HOME}/.config/jj:ro")
fi

if [ -d "$HOME/.ssh" ]; then
    mounts+=(-v "$HOME/.ssh:${DEV_HOME}/.ssh:ro")
fi

# SSH agent forwarding
env_args=()
if [ -n "${SSH_AUTH_SOCK:-}" ]; then
    mounts+=(-v "$SSH_AUTH_SOCK:/tmp/ssh-agent.sock")
    env_args+=(-e SSH_AUTH_SOCK=/tmp/ssh-agent.sock)
fi

# exec for proper signal forwarding; "$@" pass-through defaults to CMD (claude)
exec docker run \
    --rm \
    -it \
    --name "$CONTAINER_NAME" \
    "${mounts[@]}" \
    "${env_args[@]}" \
    "$IMAGE_NAME" \
    "$@"
