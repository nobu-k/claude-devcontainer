"""Macro for consuming repos to create a devcontainer target."""

def devcontainer(name = "devcontainer", **kwargs):
    """Creates a runnable target that launches the Claude devcontainer."""
    native.alias(
        name = name,
        actual = Label("//:devcontainer"),
        **kwargs
    )
