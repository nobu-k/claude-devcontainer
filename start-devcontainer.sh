#!/usr/bin/env bash
set -euo pipefail

CONTAINER_NAME="${CONTAINER_NAME:-claude-dev}"
IMAGE_NAME="${IMAGE_NAME:-claude-devcontainer}"

# Docker context: where Dockerfile lives (works for both direct invocation and Bazel runfiles)
DOCKER_CONTEXT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Workspace: what gets mounted as /workspace (caller's repo when run via bazel run)
WORKSPACE_DIR="${BUILD_WORKSPACE_DIRECTORY:-$DOCKER_CONTEXT}"

# Create worktree/workspace for VCS isolation
VCS="${DEVCONTAINER_VCS:-}"
if [ -z "$VCS" ]; then
    if [ -d "$WORKSPACE_DIR/.jj" ]; then
        VCS="jj"
    elif [ -d "$WORKSPACE_DIR/.git" ]; then
        VCS="git"
    fi
fi
WORKTREE_DIR=""
if [ -n "$VCS" ]; then
    WORKTREE_DIR="$(mktemp -d "/tmp/devcontainer-XXXXXX")"
    rmdir "$WORKTREE_DIR"  # git/jj will create it
    CONTAINER_NAME="devcontainer-$(basename "$WORKTREE_DIR")"

    case "$VCS" in
        git)
            BRANCH_NAME="devcontainer-$(basename "$WORKTREE_DIR")"
            git -C "$WORKSPACE_DIR" worktree add -b "$BRANCH_NAME" "$WORKTREE_DIR"
            ;;
        jj)
            WORKTREE_NAME="devcontainer-$(basename "$WORKTREE_DIR")"
            jj -R "$WORKSPACE_DIR" workspace add --name "$WORKTREE_NAME" "$WORKTREE_DIR"
            ;;
        *)
            echo "Unknown VCS type: $VCS (expected 'git' or 'jj')" >&2
            exit 1
            ;;
    esac

    ORIGINAL_WORKSPACE="$WORKSPACE_DIR"
    WORKSPACE_DIR="$WORKTREE_DIR"
fi

# Auto-detect UID/GID and Docker socket GID
HOST_UID="$(id -u)"
HOST_GID="$(id -g)"
DOCKER_SOCK=/var/run/docker.sock
if [ -S "$DOCKER_SOCK" ]; then
    DOCKER_GID="$(stat -c '%g' "$DOCKER_SOCK")"
else
    DOCKER_GID=984
fi

DEV_HOME="/home/dev"

# Always build (layer cache makes this fast when nothing changes)
docker build \
    --build-arg USER_UID="$HOST_UID" \
    --build-arg USER_GID="$HOST_GID" \
    --build-arg DOCKER_GID="$DOCKER_GID" \
    -t "$IMAGE_NAME" \
    "$DOCKER_CONTEXT"

# Remove pre-existing container with the same name
docker rm -f "$CONTAINER_NAME" 2>/dev/null || true

# Ensure /workspace is trusted in Claude config (container always uses this path)
if [ -f "$HOME/.claude.json" ]; then
    jq '.projects["/workspace"].hasTrustDialogAccepted = true
        | .projects["/workspace"].hasCompletedProjectOnboarding = true' \
        "$HOME/.claude.json" > "$HOME/.claude.json.tmp" \
        && mv "$HOME/.claude.json.tmp" "$HOME/.claude.json"
fi

# Build mount and env arguments
env_args=()
mounts=(
    -v "$WORKSPACE_DIR:/workspace"
    -v "$HOME/.cache/bazelisk:${DEV_HOME}/.cache/bazelisk:ro"
    -v "$HOME/.cargo:${DEV_HOME}/.cargo:ro"
    -v "$HOME/.rustup:${DEV_HOME}/.rustup:ro"
    -v "$HOME/go:${DEV_HOME}/go:ro"
    -v "$HOME/dev/go:${DEV_HOME}/gopath"
    -v "$HOME/.npm:${DEV_HOME}/.npm:ro"
    -v "$HOME/.cache/pnpm:${DEV_HOME}/.cache/pnpm:ro"
    -v "$HOME/.claude:${DEV_HOME}/.claude"
    -v "$HOME/.claude.json:${DEV_HOME}/.claude.json"
)

# Bazel output base (only if repo is configured with Bazel)
if [ -f "$WORKSPACE_DIR/MODULE.bazel" ]; then
    HOST_BAZEL_OUTPUT_BASE="$(cd "$WORKSPACE_DIR" && bazel info output_base)"
    BAZEL_RC="$(mktemp)"
    printf 'startup --output_base=%s\n' "$HOST_BAZEL_OUTPUT_BASE" > "$BAZEL_RC"
    mounts+=(
        -v "$HOST_BAZEL_OUTPUT_BASE:$HOST_BAZEL_OUTPUT_BASE"
        -v "$BAZEL_RC:/etc/bazel.bazelrc:ro"
    )
fi

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
if [ -n "${SSH_AUTH_SOCK:-}" ]; then
    mounts+=(-v "$SSH_AUTH_SOCK:/tmp/ssh-agent.sock")
    env_args+=(-e SSH_AUTH_SOCK=/tmp/ssh-agent.sock)
fi

cleanup() {
    if [ -z "${WORKTREE_DIR:-}" ]; then return; fi
    case "$VCS" in
        git)
            git -C "$ORIGINAL_WORKSPACE" worktree remove --force "$WORKTREE_DIR" 2>/dev/null || true
            # Delete the branch only if it was fully merged (no unmerged commits)
            if git -C "$ORIGINAL_WORKSPACE" merge-base --is-ancestor "$BRANCH_NAME" HEAD 2>/dev/null; then
                git -C "$ORIGINAL_WORKSPACE" branch -d "$BRANCH_NAME" 2>/dev/null || true
            fi
            ;;
        jj)
            jj -R "$ORIGINAL_WORKSPACE" workspace forget "$WORKTREE_NAME" 2>/dev/null || true
            rm -rf "$WORKTREE_DIR"
            ;;
    esac
}

# "$@" pass-through defaults to CMD (claude)
docker_run_args=(--rm -i --name "$CONTAINER_NAME")
if [ -t 0 ]; then
    docker_run_args+=(-t)
fi
if [ -n "${WORKTREE_DIR:-}" ]; then
    trap cleanup EXIT
    docker run \
        "${docker_run_args[@]}" \
        "${mounts[@]}" \
        "${env_args[@]}" \
        "$IMAGE_NAME" \
        "$@"
else
    # exec for proper signal forwarding when no cleanup is needed
    exec docker run \
        "${docker_run_args[@]}" \
        "${mounts[@]}" \
        "${env_args[@]}" \
        "$IMAGE_NAME" \
        "$@"
fi
