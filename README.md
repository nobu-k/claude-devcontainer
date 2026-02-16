# claude-devcontainer

A CLI tool that launches a Docker container pre-configured with [Claude Code](https://docs.anthropic.com/en/docs/claude-code) and common development tools. Each session runs in an isolated VCS worktree so your main working copy stays untouched.

## What's included

The container image is based on Ubuntu 24.04 and ships with:

- Claude Code (via npm)
- Git, jj (Jujutsu)
- Node.js, pnpm
- Bazelisk
- Rust toolchain (mounted from host)
- Go toolchain (mounted from host)
- Docker CLI (socket mounted from host)

## Installation

### go install

```sh
go install github.com/nobu-k/claude-devcontainer@latest
```

### Build from source

```sh
git clone https://github.com/nobu-k/claude-devcontainer.git
cd claude-devcontainer
go build .
```

### Bazel

```sh
bazel build //:devcontainer
```

## Usage

### `start` — Launch a new devcontainer

Run from any Git or jj repository:

```sh
# Launch Claude (default command) in an isolated worktree
claude-devcontainer start

# Run a specific command
claude-devcontainer start -- echo Hello

# Name the worktree/container for easy identification
claude-devcontainer start --name my-feature

# Override VCS auto-detection
claude-devcontainer start --vcs git

# Mount Docker socket into the container
claude-devcontainer start --docker

# Forward a port from the container to the host
claude-devcontainer start --port 8080:8080
```

#### Flags

| Flag | Description |
|------|-------------|
| `--name` | Name for worktree and container (default: random suffix) |
| `--vcs` | Override VCS type: `git` or `jj` (default: auto-detect from `.jj/` or `.git/`) |
| `--docker` | Mount the Docker socket into the container |
| `--port` | Publish a container port to the host (`hostPort:containerPort`) |

### `exec` — Attach to a running devcontainer

```sh
# Attach to the only running devcontainer
claude-devcontainer exec

# Attach by name (matches "my-feature" or "devcontainer-my-feature")
claude-devcontainer exec my-feature
```

If multiple devcontainers are running and no name is given, an interactive selection prompt is shown.

### Environment variables

| Variable | Description |
|----------|-------------|
| `CONTAINER_NAME` | Base container name (default: `claude-dev`) |
| `IMAGE_NAME` | Docker image name (default: `claude-devcontainer`) |
| `DEVCONTAINER_VCS` | VCS type, overridden by `--vcs` flag |

## Using with Bazel in another repository

Add the dependency to your `MODULE.bazel`:

```python
bazel_dep(name = "claude-devcontainer", version = "0.1.0")
git_override(
    module_name = "claude-devcontainer",
    remote = "https://github.com/nobu-k/claude-devcontainer.git",
    commit = "<commit-hash>",
)
```

Then create a target in your `BUILD.bazel`:

```python
load("@claude-devcontainer//:defs.bzl", "devcontainer")

devcontainer(name = "start")
devcontainer(name = "attach", command = "exec")
```

Run it:

```sh
bazel run //:start
bazel run //:start -- --name my-session
bazel run //:start -- -- echo Hello

# Attach to a running devcontainer
bazel run //:attach
bazel run //:attach -- my-feature
```

When invoked via `bazel run`, the tool automatically uses `BUILD_WORKSPACE_DIRECTORY` as the workspace root.

## How it works

**`start`** creates a new session:

1. Auto-detects VCS type (git or jj) in the current directory
2. Creates an isolated worktree so the container doesn't modify your working copy
3. Builds the Docker image (layer cache makes rebuilds fast)
4. Runs the container with host directories mounted (toolchains, SSH keys, Claude config, etc.)
5. On exit, cleans up the worktree automatically

**`exec`** attaches to a running container by opening a bash shell with `docker exec`.
