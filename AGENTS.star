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

# Disposable, fixed-command end-to-end session validation. These helpers never
# open the retained Linux artifact. The factory creates a fresh temporary root.
def session_demo_new(mode="success"):
    """Create a disposable native build session (success, failure, or timeout).

    Exposes only fixed scs/cah operations and a fixed Python fixture under the
    host sandbox. No caller-selected commands, paths, environment, or sandbox
    switch. No package installation and no unsandboxed build fallback.
    """
    if mode not in ["success", "failure", "timeout"]:
        fail("invalid demo mode")
    root = privileged.tempdir()
    root.mkdir("mount")
    for src, dst in [("seed.star", "seed.star"), ("edit.star", "edit.star"), ("inspect.star", "inspect.star")]:
        root.write_file(dst, workspace.read_file("examples/session/" + src))
    driver = workspace.read_file("examples/session/build.py")
    scs = workspace.path("bin/scs")
    cah = workspace.path("bin/cah")
    repo_path = root.path("demo.scs")
    mount_path = root.path("mount")
    receipt_path = root.path("host-result.json")

    def scs_call(args):
        return privileged.run(scs, cwd=root, check=True, timeout_ms=30000, output_limit=1048576, *args)

    def refs():
        text = scs_call(["refs", repo_path]).stdout
        result = {}
        for line in text.splitlines():
            parts = line.split()
            if len(parts) != 2:
                fail("invalid refs output")
            result[parts[0]] = parts[1]
        return result

    scs_call(["init", repo_path])
    scs_call(["run", "-publish", repo_path, root.path("seed.star")])
    scs_call(["fork", repo_path, "main", "sibling"])
    state = {"initial": refs(), "daemon": None, "build": None, "token": None}

    def mount():
        if state["daemon"] != None:
            fail("demo mount is single-use")
        p = privileged.run(
            cah, "-session", "-workspace", "candidate", "-from", "main",
            "-before", root.path("edit.star"), "-after", root.path("inspect.star"),
            "-build-result", receipt_path, repo_path, mount_path,
            cwd=root, background=True, output_limit=1048576,
        )
        state["daemon"] = p
        ready = p.expect("\n", timeout_ms=15000)
        if ready["status"] != "match" or "session=" not in ready["text"]:
            fail("mount did not report readiness; retain demo and call unmount for cleanup: " + p.stderr)
        token = ready["text"].strip().split("session=")[-1]
        if len(token) != 32:
            fail("invalid session token")
        state["token"] = token
        return {"ready": True, "mode": mode}

    def build():
        p = state["daemon"]
        if p == None or p.done or state["token"] == None or state["build"] != None:
            fail("build requires a fresh ready demo mount")
        run = privileged.run(
            "python3", "-", mode,
            cwd=mount_path, sandbox=True, readonly_workspaces=[host_c_headers],
            clear_env=True,
            env={"PATH": "/usr/bin:/bin", "HOME": mount_path, "LC_ALL": "C"},
            stdin=driver, timeout_ms=120000, output_limit=1048576,
        )
        state["build"] = run
        # Exit 124 is this fixed driver's documented internal timeout status.
        root.write_file("host-result.json", json.encode({
            "version": 1, "session": state["token"], "exit_code": run.exit_code,
            "timed_out": run.timed_out or run.exit_code == 124, "duration_ms": run.duration_ms,
        }))
        return run

    def unmount():
        p = state["daemon"]
        if p == None:
            fail("demo has not been mounted")
        if not p.done:
            u = privileged.run("/usr/bin/fusermount3", "-u", mount_path, cwd=root, timeout_ms=30000, output_limit=1048576)
            if not u.success:
                return u
            p.wait(check=False)
        return p

    def verify():
        p = state["daemon"]
        run = state["build"]
        if p == None or not p.done or run == None:
            fail("verification requires completed build and unmount")
        expected = {"success": 0, "failure": 1, "timeout": 124}[mode]
        if run.exit_code != expected or run.timed_out:
            fail("fixture runner failed unexpectedly; inspect build output; do not install dependencies or bypass sandbox")
        if (p.exit_code == 0) != (mode == "success"):
            fail("cah did not propagate the runner status: " + p.stderr)
        if "INSPECTED-CANDIDATE" not in p.stdout:
            fail("post-mount inspection did not complete: " + p.stderr)
        scs_call(["check", repo_path])
        current = refs()
        if current.get("main") != state["initial"]["main"] or current.get("sibling") != state["initial"]["sibling"]:
            fail("source/sibling changed")
        report = json.decode(run.stdout)
        if report["mode"] != mode or report["exit_code"] != expected:
            fail("build manifest disagrees with host status")
        if mode in ["success", "timeout"] and "hello" not in report["artifacts"]:
            fail("compiled executable missing")
        if mode == "failure" and "hello" in report["artifacts"]:
            fail("failed compile unexpectedly produced executable")
        changes = json.decode(scs_call(["diff", "-json", repo_path, "main", "candidate"]).stdout)
        by_path = {c["path"]: c for c in changes}
        for path, info in report["artifacts"].items():
            change = by_path.get(path)
            if change == None or "after" not in change:
                fail("persisted artifact missing: " + path)
            after = change["after"]
            if after.get("sha256") != info["sha256"] or after.get("size", 0) != info["size"] or after.get("mode") != info["mode"]:
                fail("artifact differs after reopen: " + path)
        scs_call(["run", "-readonly", "-workspace", "candidate", repo_path, root.path("inspect.star")])
        result = {"mode": mode, "sandbox": True, "build_exit": run.exit_code,
                  "cah_exit": p.exit_code, "source_unchanged": True,
                  "artifacts_verified": len(report["artifacts"]), "build": report}
        root.write_file("verified.json", json.encode_indent(result, indent="  "))
        return result

    def diagnostics():
        p = state["daemon"]
        b = state["build"]
        return {"temporary_root": root.path(), "daemon": p, "build": b}

    return module("disposable_session", mount=mount, build=build, unmount=unmount, verify=verify, diagnostics=diagnostics)

session_demo = module("session_demo", new=session_demo_new)

environment = {"git": git_tools, "workspace": workspace, "propose_agents_star": propose_agents_star, "go": go_tools, "go_test": go_test, "go_run": go_run, "cah_test": cah_test, "session_demo": session_demo}
default_repl = repl(environment)
default = privileged.model("gpt-6-astra").create(default_repl, prompt_addons=[])
