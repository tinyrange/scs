workspace = privileged.workspace(".", readonly=False)

# Fixed host header tree; exposed only as a read-only sandbox grant.
host_c_headers = privileged.workspace("/usr/include", readonly=True)

def propose_agents_star(content):
    """Stage a full configuration and request user approval before installing it."""
    candidate = privileged.tempdir()
    path = candidate.path("candidate.star")
    privileged.write_file(path, content)
    privileged.edit_agents_star(path)

load("//stdlib/git.star", git_tools="tools")
load("//stdlib/golang.star", go_tools="tools", go_test="test", go_run="run")

def cah_mount():
    """Mount the Linux test artifact using an isolated named workspace."""
    return privileged.run(
        workspace.path("bin/cah"),
        "-workspace", "cah-linux", "-from", "main",
        workspace.path(".work/linux-fast-open.scs"),
        workspace.path(".work/cah-mount"),
        cwd=workspace,
        background=True,
        output_limit=1048576,
    )

def cah_exec(command, args=[], stdin=None, background=False, timeout_ms=600000, output_limit=4194304):
    """Execute a command in the host sandbox at the fixed FUSE mount.

    Host C headers at /usr/include are granted read-only.
    No caller-selected cwd, environment, or sandbox override. A sandbox failure
    is an error, never permission to retry outside it. Use this for builds and
    filesystem checks, not installing software; ask the user for dependencies.
    """
    if type(command) != "string" or command == "":
        fail("command must be a nonempty string")
    if type(args) not in ["list", "tuple"]:
        fail("args must be a list or tuple of strings")
    for arg in args:
        if type(arg) != "string":
            fail("command arguments must be strings")
    if type(timeout_ms) != "int" or timeout_ms < 1 or timeout_ms > 3600000:
        fail("timeout_ms must be between 1 and 3600000")
    return privileged.run(
        command,
        cwd=workspace.path(".work/cah-mount"),
        sandbox=True,
        readonly_workspaces=[host_c_headers],
        stdin=stdin,
        background=background,
        timeout_ms=timeout_ms,
        output_limit=output_limit,
        *args
    )

def cah_unmount():
    """Normally unmount only the fixed cah test mount; no force/lazy options."""
    return privileged.run(
        "/usr/bin/fusermount3", "-u", workspace.path(".work/cah-mount"),
        cwd=workspace,
        timeout_ms=30000,
        output_limit=1048576,
    )

cah_test = module("cah_test", mount=cah_mount, exec=cah_exec, unmount=cah_unmount)
environment = {"git": git_tools, "workspace": workspace, "propose_agents_star": propose_agents_star, "go": go_tools, "go_test": go_test, "go_run": go_run, "cah_test": cah_test}
default_repl = repl(environment)
default = privileged.model("gpt-6-astra").create(default_repl, prompt_addons=[])
