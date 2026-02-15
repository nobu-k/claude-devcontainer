"""Macro for consuming repos to create a devcontainer target."""

load("@rules_shell//shell:sh_binary.bzl", "sh_binary")

def devcontainer(name = "devcontainer", vcs = "", **kwargs):
    """Creates an sh_binary target that launches the Claude devcontainer.

    Args:
        name: Target name.
        vcs: Version control system for worktree isolation ("git" or "jj").
             Empty string (default) mounts the repo directly.
        **kwargs: Additional arguments passed to sh_binary.
    """
    env = dict(kwargs.pop("env", {}))
    env["DEVCONTAINER_VCS"] = vcs
    sh_binary(
        name = name,
        srcs = [Label("//:start-devcontainer.sh")],
        data = [
            Label("//:Dockerfile"),
            Label("//:.dockerignore"),
        ],
        env = env,
        **kwargs
    )
