"""
rig/atomic.py — atomic file-write primitives.

Two public functions:
    atomic_write(path, data)        -> sha256 hex string
    atomic_append_line(path, line)  -> None

Protocol for atomic_write (ADR-005):
    write <path>.tmp-<uuid4>  →  fsync(fd)  →  close  →  rename  →  fsync(parent dir)

The tmp-then-rename protocol ensures the target path is never in a partially-written
state: a reader either sees the old file or the complete new one, never a torn write.

Protocol for atomic_append_line:
    open(O_WRONLY|O_APPEND|O_CREAT)  →  write  →  fsync(fd)  →  close  →  fsync(parent dir)

O_APPEND makes the kernel's seek-to-end + write a single atomic step, preventing
interleaved writes from concurrent openers on the same host.  fsync makes it durable.

Architecture constraints honoured:
- No retry logic.
- No convenience helpers that bypass this module.
- No WAL schema, session state, or measurement logic here.
"""

import hashlib
import os
import uuid


# ── private helpers ─────────────────────────────────────────────────────────

def _write_all(fd: int, data: bytes) -> None:
    """Write every byte of data to fd, looping on short writes.

    os.write(2) is not required to write all bytes in one call; it returns
    the number of bytes actually written, which may be less than len(data).
    Ignoring the return value silently truncates the output.  This loop is
    the correct primitive for all write paths in this module.
    """
    view = memoryview(data)
    written = 0
    total = len(data)
    while written < total:
        n = os.write(fd, view[written:])
        if n == 0:
            # os.write returning 0 for a non-empty buffer on a regular file
            # is not a normal short write; treat it as an I/O error.
            raise OSError("os.write returned 0")
        written += n


def _fsync_dir(dir_path: str) -> None:
    """fsync a directory file-descriptor to make a rename durable on disk."""
    fd = os.open(dir_path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def _try_unlink(path: str) -> None:
    """Delete path, ignoring errors (best-effort tmp cleanup only)."""
    try:
        os.unlink(path)
    except OSError:
        pass


# ── public API ───────────────────────────────────────────────────────────────

def atomic_write(path: str, data: "bytes | str") -> str:
    """Write data to path atomically; return the SHA-256 hex digest of data.

    The target path is never partially written: it flips from old to new
    (or from absent to present) in a single rename(2) call.

    Raises OSError on any failure.  If the failure occurs before rename,
    the temp file is removed before the exception propagates.  If it occurs
    after rename (i.e., during the parent-dir fsync), the file is already
    in place and no rollback is attempted.
    """
    if isinstance(data, str):
        data = data.encode()

    digest = hashlib.sha256(data).hexdigest()
    parent = os.path.dirname(os.path.abspath(path))
    tmp = path + ".tmp-" + str(uuid.uuid4())

    try:
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o644)
        try:
            _write_all(fd, data)
            os.fsync(fd)
        finally:
            os.close(fd)
        os.rename(tmp, path)
    except BaseException:
        _try_unlink(tmp)
        raise

    _fsync_dir(parent)
    return digest


def atomic_append_line(path: str, line: str) -> None:
    """Append one JSONL line to path with full fsync discipline.

    If line does not end with '\\n', one is added.  The file is created if
    it does not exist.  No return value; SHA-256 of the whole log file is
    not computed here because it would require reading the entire file.
    """
    if not line.endswith("\n"):
        line = line + "\n"
    data = line.encode()

    parent = os.path.dirname(os.path.abspath(path))

    fd = os.open(path, os.O_WRONLY | os.O_APPEND | os.O_CREAT, 0o644)
    try:
        _write_all(fd, data)
        os.fsync(fd)
    finally:
        os.close(fd)

    _fsync_dir(parent)