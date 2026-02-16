"""Macro for consuming repos to create a devcontainer target."""

def _devcontainer_impl(ctx):
    script = ctx.actions.declare_file(ctx.label.name + ".sh")
    args = []
    if ctx.attr.docker:
        args.append("--docker")
    for port in ctx.attr.ports:
        args.extend(["--port", port])

    ctx.actions.write(
        output = script,
        content = '#!/bin/bash\nexec "{}" {} "$@"\n'.format(
            ctx.executable._binary.short_path,
            " ".join(args),
        ),
        is_executable = True,
    )

    return [DefaultInfo(
        executable = script,
        runfiles = ctx.runfiles().merge(ctx.attr._binary[DefaultInfo].default_runfiles),
    )]

devcontainer = rule(
    implementation = _devcontainer_impl,
    doc = "Creates a runnable target that launches the Claude devcontainer.",
    attrs = {
        "docker": attr.bool(
            default = False,
            doc = "Mount the Docker socket into the container.",
        ),
        "ports": attr.string_list(
            default = [],
            doc = "Port mappings to publish (hostPort:containerPort).",
        ),
        "_binary": attr.label(
            default = Label("//:devcontainer"),
            executable = True,
            cfg = "target",
        ),
    },
    executable = True,
)
