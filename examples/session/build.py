"""Fixed disposable fixture driver. MUST be invoked by an external sandbox.

This is not a general-purpose process sandbox. The approved host tool supplies
isolation and the authoritative outer process status. Files here are untrusted
build-side evidence, not the host receipt consumed by cah --build-result.
"""
import hashlib
import json
import os
import resource
import signal
import stat
import subprocess
import sys
import time


def child_limits():
    # Bound this tiny fixture's outputs/resources, not the FUSE daemon's RSS.
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    resource.setrlimit(resource.RLIMIT_FSIZE, (8 << 20, 8 << 20))
    resource.setrlimit(resource.RLIMIT_CPU, (20, 20))


def phase(argv, label, timeout):
    start = time.monotonic()
    result = {"argv": argv, "label": label, "timed_out": False}
    # No output pipes: descendants cannot hold communicate() open indefinitely.
    # RLIMIT_FSIZE bounds child-generated logs. Files are intentionally exclusive.
    with open(label + ".stdout", "xb") as out, open(label + ".stderr", "xb") as err:
        child = None
        try:
            child = subprocess.Popen(
                argv, stdin=subprocess.DEVNULL, stdout=out, stderr=err,
                start_new_session=True, preexec_fn=child_limits,
            )
            try:
                result["exit_code"] = child.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                result["timed_out"] = True
                os.killpg(child.pid, signal.SIGKILL)
                result["exit_code"] = child.wait(timeout=5)
        except OSError as exc:
            result["exit_code"] = 127
            result["launch_error"] = str(exc)
        finally:
            if child is not None:
                # Kill remaining members even if the immediate child exited.
                # This does not contain hostile descendants that leave the group;
                # that responsibility remains with the external host sandbox.
                try:
                    os.killpg(child.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
        out.flush()
        err.flush()
    result["duration_ms"] = round((time.monotonic() - start) * 1000)
    return result


def artifact(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, "rb") as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode):
            raise RuntimeError("not a regular artifact: " + path)
        digest = hashlib.sha256()
        for chunk in iter(lambda: stream.read(65536), b""):
            digest.update(chunk)
    return {
        "sha256": digest.hexdigest(), "size": info.st_size,
        "mode": stat.S_IMODE(info.st_mode), "mtime_ns": info.st_mtime_ns,
    }


def main(mode):
    if mode not in ("success", "failure", "timeout"):
        raise ValueError("mode must be success, failure, or timeout")
    os.mkdir(".build-tmp")
    os.environ["TMPDIR"] = os.path.abspath(".build-tmp")
    report = {"version": 1, "mode": mode, "phases": [], "artifacts": {}}
    argv = ["cc", "-std=c11", "-O2", "-Wall", "-Wextra", "-Werror"]
    if mode == "failure":
        argv.append("-DFORCE_FAILURE")
    argv.extend(["hello.c", "-o", "hello"])
    report["phases"].append(phase(argv, "compile", 30))
    exit_code = report["phases"][-1]["exit_code"]
    timed_out = report["phases"][-1]["timed_out"]
    if exit_code == 0 and not timed_out:
        report["phases"].append(phase(["./hello"], "program", 5))
        exit_code = report["phases"][-1]["exit_code"]
        timed_out = report["phases"][-1]["timed_out"]
        if exit_code == 0 and not timed_out:
            with open("program.stdout", "rb") as output, open("expected.txt", "rb") as expected:
                if output.read() != expected.read():
                    report["verification_error"] = "executable output does not match native edit"
                    exit_code = 1
        if mode == "timeout" and exit_code == 0 and not timed_out:
            # Controlled timeout after a real successful compile and execution.
            report["phases"].append(phase([sys.executable, "-c", "import time; time.sleep(60)"], "timeout", 0.2))
            exit_code = report["phases"][-1]["exit_code"]
            timed_out = report["phases"][-1]["timed_out"]
    if mode == "failure" and exit_code != 0 and not timed_out:
        with open("compile.stderr", "rb") as diagnostic:
            if b"requested compiler failure" not in diagnostic.read(8 << 20):
                report["verification_error"] = "compiler failed for an unexpected reason"
                exit_code = 125
    if timed_out:
        status = 124
    elif exit_code == 0:
        status = 0
    elif exit_code in (125, 127):
        status = exit_code
    else:
        status = 1
    report["exit_code"] = status
    report["timed_out"] = timed_out
    paths = ["hello.c", "expected.txt", "compile.stdout", "compile.stderr"]
    for path in ("hello", "program.stdout", "program.stderr", "timeout.stdout", "timeout.stderr"):
        if os.path.exists(path):
            paths.append(path)
    for path in paths:
        report["artifacts"][path] = artifact(path)
    encoded = json.dumps(report, sort_keys=True)
    with open(".build-result.json", "x", encoding="utf-8") as out:
        out.write(encoded + "\n")
        out.flush()
        os.fsync(out.fileno())
    print(encoded, flush=True)
    return status


if __name__ == "__main__":
    sys.exit(main(sys.argv[1] if len(sys.argv) == 2 else "success"))
