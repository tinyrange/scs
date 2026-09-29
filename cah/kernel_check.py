"""Build and verify a tiny x86 kernel inside an already-mounted cah workspace.

Feed this script to sandboxed python3 stdin; use --verify after unmount/remount.
The default run cleans generated build outputs before timing configuration and
compilation separately. It does not install tools, mount, or bypass the sandbox.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import stat
import subprocess
import time

REPORT = Path(".cah-kernel-build.json")
ARTIFACTS = ("Makefile", ".config", "vmlinux", "System.map", "arch/x86/boot/bzImage")


def describe(name):
    path = Path(name)
    st = path.lstat()
    if not stat.S_ISREG(st.st_mode) or not st.st_size:
        raise RuntimeError(f"Not a nonempty regular file: {name}")
    digest = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            digest.update(chunk)
    return dict(size=st.st_size, mode=stat.S_IMODE(st.st_mode),
                mtime_ns=st.st_mtime_ns, sha256=digest.hexdigest())


def check_magic():
    with open("vmlinux", "rb") as f:
        if f.read(4) != b"\x7fELF":
            raise RuntimeError("vmlinux is not ELF")
    with open("arch/x86/boot/bzImage", "rb") as f:
        f.seek(0x1fe)
        if f.read(2) != b"\x55\xaa":
            raise RuntimeError("bzImage has no x86 boot signature")
        f.seek(0x202)
        if f.read(4) != b"HdrS":
            raise RuntimeError("bzImage has no Linux boot header")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verify", action="store_true")
    args = parser.parse_args()
    if not os.path.ismount(os.getcwd()):
        raise RuntimeError("Run at the root of the mounted filesystem")
    if args.verify:
        report = json.loads(REPORT.read_text())
        if not report.get("success"):
            raise RuntimeError("Previous build did not succeed")
        actual = {name: describe(name) for name in ARTIFACTS}
        if actual != report["artifacts"]:
            raise RuntimeError("Persisted file bytes or metadata differ from build manifest")
        check_magic()
        print(json.dumps(dict(persistence_verified=True, artifacts=actual), indent=2))
        return

    env = os.environ.copy()
    tmp = Path(".cah-tmp").resolve()
    tmp.mkdir(exist_ok=True)
    env.update(TMPDIR=str(tmp), KBUILD_BUILD_USER="cah", KBUILD_BUILD_HOST="sandbox")
    report = dict(success=False, clean_build=True, jobs=4,
                  build_user="cah", build_host="sandbox", phases=[])
    # Anonymous pipes avoid unsupported filesystem FIFOs. The option is
    # inherited by recursive make via MAKEFLAGS.
    for target in ("clean", "tinyconfig", "bzImage"):
        argv = ["make", "--jobserver-style=pipe"]
        if target == "bzImage":
            argv.append("-j4")
        argv.append(target)
        print("Running:", " ".join(argv), flush=True)
        start = time.monotonic()
        result = subprocess.run(argv, env=env, capture_output=True, text=True)
        phase = dict(argv=argv, duration_seconds=round(time.monotonic() - start, 3),
                     exit_code=result.returncode, stdout=result.stdout, stderr=result.stderr)
        report["phases"].append(phase)
        print(result.stdout, end="", flush=True)
        print(result.stderr, end="", flush=True)
        print(f"{target}: exit={result.returncode}, seconds={phase['duration_seconds']}", flush=True)
        REPORT.write_text(json.dumps(report, indent=2) + "\n")
        if result.returncode:
            raise SystemExit(result.returncode)
        if target == "clean":
            for name in ("vmlinux", "arch/x86/boot/bzImage"):
                if Path(name).exists():
                    raise RuntimeError(f"Clean left a stale image: {name}")
    check_magic()
    report["artifacts"] = {name: describe(name) for name in ARTIFACTS}
    report["success"] = True
    REPORT.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(dict(success=True, artifacts=report["artifacts"]), indent=2))


if __name__ == "__main__":
    main()
