"""Run inside a mounted cah workspace: python3 - < cah/mount_check.py.
Uses only filesystem calls and a temporary test directory within the mount.
"""
import errno
import mmap
import os
import shutil
import tempfile
from concurrent.futures import ThreadPoolExecutor

root = tempfile.mkdtemp(prefix=".cah-check-", dir=".")
try:
    a, b = root + "/a", root + "/b"
    with open(a, "w+b", buffering=0) as source, open(b, "w+b", buffering=0) as old:
        source.write(b"source")
        old.write(b"target")
        os.replace(a, b)
        assert os.pread(source.fileno(), 6, 0) == b"source"
        assert os.pread(old.fileno(), 6, 0) == b"target"
        os.pwrite(old.fileno(), b"OLD", 0)
        assert open(b, "rb").read() == b"source"
        source.truncate(2)
        source.truncate(8)
        assert os.pread(source.fileno(), 8, 0) == b"so" + bytes(6)
        os.fsync(source.fileno())
        with mmap.mmap(source.fileno(), 8, access=mmap.ACCESS_READ) as m:
            assert m[:] == b"so" + bytes(6)
        os.unlink(b)
        assert os.fstat(source.fileno()).st_nlink == 0
        assert os.pread(source.fileno(), 8, 0) == b"so" + bytes(6)
    # Exercise the kernel's copy_file_range fallback when go-fuse rejects
    # COPY_FILE_RANGE_64 with ENOSYS. Check offsets, repeated partial copies,
    # EOF, and exact bytes rather than assuming an opcode warning means loss.
    payload = bytes(range(256)) * 1025 + b"copy-tail"
    with open(root + "/copy-src", "w+b", buffering=0) as src, open(root + "/copy-dst", "w+b", buffering=0) as dst:
        src.write(payload)
        src.seek(0)
        dst.write(b"prefix:")
        copied = 0
        while copied < len(payload):
            n = os.copy_file_range(src.fileno(), dst.fileno(), 65537)
            assert n > 0, (copied, len(payload))
            copied += n
        assert os.copy_file_range(src.fileno(), dst.fileno(), 1) == 0
        assert src.tell() == len(payload)
        assert dst.tell() == 7 + len(payload)
        dst.seek(0)
        assert dst.read() == b"prefix:" + payload
        assert os.pread(src.fileno(), len(payload), 0) == payload
    os.mkdir(root + "/dir")
    with open(root + "/dir/f", "w+b", buffering=0) as f:
        os.rename(root + "/dir", root + "/moved")
        def write(i):
            os.pwrite(f.fileno(), ("%02d" % i).encode(), 2*i)
        with ThreadPoolExecutor(max_workers=8) as pool:
            list(pool.map(write, range(32)))
        expected = "".join("%02d" % i for i in range(32)).encode()
        assert open(root + "/moved/f", "rb").read() == expected
        os.fsync(f.fileno())
    os.symlink("moved/f", root + "/link")
    assert os.readlink(root + "/link") == "moved/f"
    assert open(root + "/link", "rb").read() == expected
    os.chmod(root + "/moved/f", 0o750)
    assert os.stat(root + "/moved/f").st_mode & 0o777 == 0o750
    os.utime(root + "/moved/f", ns=(1234567890123456789, 1234567890987654321))
    assert os.stat(root + "/moved/f").st_mtime_ns == 1234567890987654321
    os.mkdir(root + "/other")
    open(root + "/other/keep", "wb").close()
    try:
        os.replace(root + "/moved", root + "/other")
        raise AssertionError("nonempty replacement succeeded")
    except OSError as e:
        assert e.errno == errno.ENOTEMPTY, e
    assert open(root + "/moved/f", "rb").read() == expected
    print("Mounted semantics passed: copy_file_range fallback, replacement, open-unlinked, truncate/zero-fill, mmap, parallel offset writes, directory rename, symlinks, chmod, utime, failed rename atomicity")
finally:
    shutil.rmtree(root)
