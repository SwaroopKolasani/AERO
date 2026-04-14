"""
Tests for rig/atomic.py — T0.2 acceptance criteria.

Covered:
  atomic_write
    - normal write: file exists with correct content
    - SHA-256 returned matches final file contents
    - parent dir fsync is called
    - temp file uses expected naming scheme
    - temp file cleaned on rename failure
    - target unchanged before rename (crash-between-write-and-rename proof)
    - overwrites existing file atomically
    - accepts str input (encoded to bytes)
    - empty data

  atomic_append_line
    - line appended with newline
    - newline not doubled when line already ends with one
    - multiple appends produce valid JSONL
    - file created if absent
    - fsync called on fd and parent dir
"""

import hashlib
import os
from pathlib import Path
from unittest.mock import call, patch, MagicMock

import pytest

from rig.atomic import atomic_write, atomic_append_line, _fsync_dir, _try_unlink


# ═══════════════════════════════════════════════════════════════════════════
# atomic_write — normal-path tests
# ═══════════════════════════════════════════════════════════════════════════

def test_write_creates_file_with_correct_content(tmp_path):
    target = tmp_path / "out.bin"
    atomic_write(str(target), b"hello world")
    assert target.read_bytes() == b"hello world"


def test_write_returns_sha256_matching_file(tmp_path):
    target = tmp_path / "out.bin"
    data = b"some payload"
    digest = atomic_write(str(target), data)
    expected = hashlib.sha256(data).hexdigest()
    assert digest == expected
    assert digest == hashlib.sha256(target.read_bytes()).hexdigest()


def test_write_str_input_encoded_to_utf8(tmp_path):
    target = tmp_path / "out.txt"
    digest = atomic_write(str(target), "café")
    assert target.read_bytes() == "café".encode()
    assert digest == hashlib.sha256("café".encode()).hexdigest()


def test_write_empty_data(tmp_path):
    target = tmp_path / "empty.bin"
    digest = atomic_write(str(target), b"")
    assert target.read_bytes() == b""
    assert digest == hashlib.sha256(b"").hexdigest()


def test_write_overwrites_existing_file(tmp_path):
    target = tmp_path / "out.bin"
    target.write_bytes(b"old content")
    atomic_write(str(target), b"new content")
    assert target.read_bytes() == b"new content"


def test_write_tmp_file_absent_after_success(tmp_path):
    target = tmp_path / "out.bin"
    atomic_write(str(target), b"data")
    tmp_files = list(tmp_path.glob("*.tmp-*"))
    assert tmp_files == [], f"unexpected tmp files: {tmp_files}"


def test_write_tmp_filename_scheme(tmp_path):
    """Temp file must be <target>.tmp-<uuid4> in the same directory."""
    target = tmp_path / "out.bin"
    seen_tmp: list[str] = []

    real_rename = os.rename

    def capturing_rename(src, dst):
        seen_tmp.append(src)
        real_rename(src, dst)

    with patch("rig.atomic.os.rename", capturing_rename):
        atomic_write(str(target), b"x")

    assert len(seen_tmp) == 1
    tmp_name = Path(seen_tmp[0]).name
    assert tmp_name.startswith("out.bin.tmp-")
    # The suffix after ".tmp-" must be a valid UUID4 (32 hex chars + 4 dashes).
    suffix = tmp_name[len("out.bin.tmp-"):]
    import uuid
    uuid.UUID(suffix, version=4)  # raises ValueError if not valid


# ═══════════════════════════════════════════════════════════════════════════
# atomic_write — fsync discipline
# ═══════════════════════════════════════════════════════════════════════════

def test_write_fsyncs_parent_dir(tmp_path):
    """_fsync_dir must be called with the target's parent directory."""
    target = tmp_path / "out.bin"
    called_with: list[str] = []

    real_fsync_dir = _fsync_dir.__wrapped__ if hasattr(_fsync_dir, "__wrapped__") else None

    with patch("rig.atomic._fsync_dir", side_effect=lambda p: called_with.append(p)) as mock_fd:
        atomic_write(str(target), b"data")

    assert len(called_with) == 1
    assert os.path.samefile(called_with[0], str(tmp_path))


def test_write_fsyncs_fd_before_rename(tmp_path):
    """os.fsync must be called on the file fd before os.rename is called."""
    target = tmp_path / "out.bin"
    call_order: list[str] = []

    real_fsync = os.fsync
    real_rename = os.rename

    def tracking_fsync(fd):
        call_order.append("fsync")
        real_fsync(fd)

    def tracking_rename(src, dst):
        call_order.append("rename")
        real_rename(src, dst)

    with patch("rig.atomic.os.fsync", tracking_fsync), \
         patch("rig.atomic.os.rename", tracking_rename):
        atomic_write(str(target), b"data")

    # fsync(fd) must precede rename; _fsync_dir is patched out above via os.fsync
    # so we see: fsync (file fd), rename, fsync (dir fd) — in that order.
    assert call_order.index("fsync") < call_order.index("rename")


# ═══════════════════════════════════════════════════════════════════════════
# atomic_write — failure / crash-safety tests
# ═══════════════════════════════════════════════════════════════════════════

def test_target_unchanged_before_rename(tmp_path):
    """Crash-between-write-and-rename proof.

    At the moment rename would fire, the target path must still contain the
    original data.  This proves the tmp-then-rename protocol prevents
    partial visibility even if the process dies between write and rename.
    """
    target = tmp_path / "out.bin"
    target.write_bytes(b"original")

    state: dict = {}

    def failing_rename(src, dst):
        # Snapshot target state at the exact rename boundary.
        state["target_bytes"] = Path(dst).read_bytes()
        state["tmp_exists"] = Path(src).exists()
        raise OSError("simulated crash at rename")

    with patch("rig.atomic.os.rename", failing_rename):
        with pytest.raises(OSError, match="simulated crash at rename"):
            atomic_write(str(target), b"new content")

    # At the rename boundary, target was still the original.
    assert state["target_bytes"] == b"original"
    # New content was only in the tmp file, not the target.
    assert state["tmp_exists"] is True
    # After the exception, the target is still the original (not corrupted).
    assert target.read_bytes() == b"original"


def test_tmp_cleaned_on_rename_failure(tmp_path):
    """If rename fails, the temp file must not be left on disk."""
    target = tmp_path / "out.bin"

    with patch("rig.atomic.os.rename", side_effect=OSError("disk full")):
        with pytest.raises(OSError):
            atomic_write(str(target), b"data")

    tmp_files = list(tmp_path.glob("*.tmp-*"))
    assert tmp_files == [], f"orphaned tmp files after rename failure: {tmp_files}"


def test_tmp_cleaned_on_write_failure(tmp_path):
    """If the write itself fails, the temp file must not be left on disk."""
    target = tmp_path / "out.bin"

    real_write = os.write
    call_count = [0]

    def failing_write(fd, data):
        call_count[0] += 1
        raise OSError("write error")

    with patch("rig.atomic.os.write", failing_write):
        with pytest.raises(OSError):
            atomic_write(str(target), b"data")

    tmp_files = list(tmp_path.glob("*.tmp-*"))
    assert tmp_files == [], f"orphaned tmp files after write failure: {tmp_files}"


def test_nonexistent_parent_raises(tmp_path):
    """Writing to a path whose parent does not exist must raise OSError, not silently fail."""
    target = tmp_path / "no_such_dir" / "out.bin"
    with pytest.raises(OSError):
        atomic_write(str(target), b"data")


def test_write_loops_on_short_write(tmp_path):
    """If os.write returns fewer bytes than requested, _write_all must loop
    until all bytes are persisted.  The final file must contain the full input
    and the returned digest must match it.
    """
    target = tmp_path / "out.bin"
    data = b"hello world"
    real_write = os.write
    call_count = [0]

    def short_write(fd, buf):
        call_count[0] += 1
        if call_count[0] == 1 and len(buf) > 1:
            # Deliver only the first byte on the initial call.
            return real_write(fd, bytes(buf)[:1])
        return real_write(fd, bytes(buf))

    with patch("rig.atomic.os.write", short_write):
        digest = atomic_write(str(target), data)

    assert target.read_bytes() == data
    assert digest == hashlib.sha256(data).hexdigest()
    assert call_count[0] > 1, "loop was never exercised"


# ═══════════════════════════════════════════════════════════════════════════
# atomic_append_line — normal-path tests
# ═══════════════════════════════════════════════════════════════════════════

def test_append_creates_file_if_absent(tmp_path):
    target = tmp_path / "events.jsonl"
    atomic_append_line(str(target), '{"event": "start"}')
    assert target.exists()


def test_append_line_has_newline(tmp_path):
    target = tmp_path / "events.jsonl"
    atomic_append_line(str(target), '{"event": "start"}')
    assert target.read_text().endswith("\n")


def test_append_no_double_newline(tmp_path):
    """A line already ending with \\n must not get a second one."""
    target = tmp_path / "events.jsonl"
    atomic_append_line(str(target), '{"event": "start"}\n')
    content = target.read_text()
    assert content.count("\n") == 1


def test_append_multiple_lines_valid_jsonl(tmp_path):
    import json
    target = tmp_path / "events.jsonl"
    lines = ['{"a": 1}', '{"b": 2}', '{"c": 3}']
    for line in lines:
        atomic_append_line(str(target), line)
    records = [json.loads(l) for l in target.read_text().splitlines()]
    assert records == [{"a": 1}, {"b": 2}, {"c": 3}]


def test_append_preserves_existing_content(tmp_path):
    target = tmp_path / "events.jsonl"
    atomic_append_line(str(target), '{"event": "first"}')
    atomic_append_line(str(target), '{"event": "second"}')
    lines = target.read_text().splitlines()
    assert len(lines) == 2
    assert "first" in lines[0]
    assert "second" in lines[1]


def test_append_loops_on_short_write(tmp_path):
    """If os.write returns fewer bytes than requested during an append,
    _write_all must loop until the complete line is written.
    """
    target = tmp_path / "events.jsonl"
    line = '{"event": "x"}'
    real_write = os.write
    call_count = [0]

    def short_write(fd, buf):
        call_count[0] += 1
        if call_count[0] == 1 and len(buf) > 1:
            return real_write(fd, bytes(buf)[:1])
        return real_write(fd, bytes(buf))

    with patch("rig.atomic.os.write", short_write):
        atomic_append_line(str(target), line)

    content = target.read_text()
    assert content == line + "\n"
    assert call_count[0] > 1, "loop was never exercised"


# ═══════════════════════════════════════════════════════════════════════════
# atomic_append_line — fsync discipline
# ═══════════════════════════════════════════════════════════════════════════

def test_append_fsyncs_parent_dir(tmp_path):
    """_fsync_dir must be called with the file's parent directory."""
    target = tmp_path / "events.jsonl"
    called_with: list[str] = []

    with patch("rig.atomic._fsync_dir", side_effect=lambda p: called_with.append(p)):
        atomic_append_line(str(target), '{"event": "x"}')

    assert len(called_with) == 1
    assert os.path.samefile(called_with[0], str(tmp_path))


def test_append_fsyncs_fd(tmp_path):
    """os.fsync must be called on the file fd during append."""
    target = tmp_path / "events.jsonl"
    fsync_called = [False]
    real_fsync = os.fsync

    def tracking_fsync(fd):
        fsync_called[0] = True
        real_fsync(fd)

    with patch("rig.atomic.os.fsync", tracking_fsync), \
         patch("rig.atomic._fsync_dir"):
        atomic_append_line(str(target), '{"event": "x"}')

    assert fsync_called[0]


# ═══════════════════════════════════════════════════════════════════════════
# Private helpers — direct tests
# ═══════════════════════════════════════════════════════════════════════════

def test_try_unlink_ignores_missing_file(tmp_path):
    _try_unlink(str(tmp_path / "ghost.bin"))  # must not raise


def test_try_unlink_removes_existing_file(tmp_path):
    p = tmp_path / "to_remove.bin"
    p.write_bytes(b"x")
    _try_unlink(str(p))
    assert not p.exists()


def test_fsync_dir_on_real_dir(tmp_path):
    _fsync_dir(str(tmp_path))  # must not raise