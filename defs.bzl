"""Macro for consuming repos to create a devcontainer target."""

load("@rules_shell//shell:sh_binary.bzl", "sh_binary")

def devcontainer(name = "devcontainer", **kwargs):
    """Creates an sh_binary target that launches the Claude devcontainer.

    Args:
        name: Target name.
        **kwargs: Additional arguments passed to sh_binary.
    """
    sh_binary(
        name = name,
        srcs = [Label("//:start-devcontainer.sh")],
        data = [
            Label("//:Dockerfile"),
            Label("//:.dockerignore"),
        ],
        **kwargs
    )
