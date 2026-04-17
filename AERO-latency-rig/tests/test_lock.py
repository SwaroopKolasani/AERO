"""
Tests for rig/manifest.py T1.3: single-writer lock.

AC:
  1. Concurrent startup on same session directory fails with session_locked.
  2. Stale lock without --adopt-stale-lock refuses with stale_lock_needs_adoption.
  3. Stale lock with --adopt-stale-lock succeeds and records lock_adopted event.

Additional:
  - lock file contains the required four fields
  - release_lock removes the file; is a no-op if file is absent
  - session.lock is excluded from disk_orphan scanning in reconcile()
"""

import hashlib
import json
import os
import socket
import time
from pathlib import Path

import pytest

from rig.manifest import (
    LOCK_SESSION_LOCKED,
    LOCK_STALE_NEEDS_ADOPTION,
    SessionLockedError,
    StaleLockNeedsAdoptionError,
    _current_rig_sha256,
    _hostname_hash,
    _lock_is_stale,
    _pid_alive,
    acquire_lock,
    append_event,
    read_events,
    reconcile,
    release_lock,
    write_manifest,
)


# ── helpers ───────────────────────────────────────────────────────────────────

SHA64 = "a" * 64


def _ts():
    return time.monotonic_ns(), "2025-06-01T12:00:00Z"


def _open_session(events_path: Path) -> str:
    ns, wall = _ts()
    return append_event(
        events_path, "session_opened",
        {"session_id": "ses-001", "rig_version": "0.1.0",
         "rig_self_sha256": SHA64, "git_commit": "abc1234"},
        None, ns, wall,
    )


def _write_stale_lock(lock_path: Path, pid: int, sha: str = SHA64) -> None:
    """Write a lock file that looks stale (wrong sha or dead pid)."""
    lock_path.write_text(json.dumps({
        "pid": pid,
        "start_monotonic": 123456,
        "rig_self_sha256": sha,
        "hostname_hash": "b" * 64,
    }), encoding="utf-8")


# ── _pid_alive ────────────────────────────────────────────────────────────────

def test_pid_alive_current_process():
    assert _pid_alive(os.getpid()) is True


def test_pid_alive_dead_pid():
    # PID 0 is not a valid user process and cannot be signalled.
    # On Linux/macOS, kill(0, 0) signals the process group; we test a
    # large PID instead.  If this flakes, the PID space has wrapped.
    assert _pid_alive(2**20 + 7) is False


# ── _lock_is_stale ────────────────────────────────────────────────────────────

def test_lock_is_stale_dead_pid():
    lock = {
        "pid": 2**20 + 7,
        "start_monotonic": 0,
        "rig_self_sha256": _current_rig_sha256(),
        "hostname_hash": SHA64,
    }
    assert _lock_is_stale(lock, _current_rig_sha256()) is True


def test_lock_is_stale_wrong_sha():
    lock = {
        "pid": os.getpid(),
        "start_monotonic": 0,
        "rig_self_sha256": "0" * 64,   # wrong hash
        "hostname_hash": SHA64,
    }
    assert _lock_is_stale(lock, _current_rig_sha256()) is True


def test_lock_is_not_stale_live_pid_matching_sha():
    current_sha = _current_rig_sha256()
    lock = {
        "pid": os.getpid(),
        "start_monotonic": 0,
        "rig_self_sha256": current_sha,
        "hostname_hash": SHA64,
    }
    assert _lock_is_stale(lock, current_sha) is False


# ── acquire_lock ── normal path ───────────────────────────────────────────────

def test_acquire_creates_lock_file(tmp_path):
    acquire_lock(tmp_path)
    assert (tmp_path / "session.lock").exists()


def test_acquire_returns_dict_with_required_fields(tmp_path):
    lock = acquire_lock(tmp_path)
    for field in ("pid", "start_monotonic", "rig_self_sha256", "hostname_hash"):
        assert field in lock, f"missing field: {field}"


def test_acquire_records_current_pid(tmp_path):
    lock = acquire_lock(tmp_path)
    assert lock["pid"] == os.getpid()


def test_acquire_lock_file_matches_returned_dict(tmp_path):
    lock = acquire_lock(tmp_path)
    stored = json.loads((tmp_path / "session.lock").read_text())
    assert stored == lock


def test_hostname_hash_is_64_hex(tmp_path):
    lock = acquire_lock(tmp_path)
    h = lock["hostname_hash"]
    assert len(h) == 64
    assert all(c in "0123456789abcdef" for c in h)


def test_hostname_hash_is_sha256_of_hostname(tmp_path):
    lock = acquire_lock(tmp_path)
    expected = hashlib.sha256(socket.gethostname().encode()).hexdigest()
    assert lock["hostname_hash"] == expected


# ── acquire_lock ── AC 1: concurrent startup ──────────────────────────────────

def test_concurrent_startup_fails_with_session_locked(tmp_path):
    """AC: second acquire on the same directory fails with session_locked."""
    acquire_lock(tmp_path)
    with pytest.raises(SessionLockedError) as exc_info:
        acquire_lock(tmp_path)
    assert exc_info.value.error_code == LOCK_SESSION_LOCKED


def test_session_locked_error_carries_pid(tmp_path):
    acquire_lock(tmp_path)
    with pytest.raises(SessionLockedError) as exc_info:
        acquire_lock(tmp_path)
    assert exc_info.value.pid == os.getpid()


def test_session_locked_error_code_is_named_string(tmp_path):
    acquire_lock(tmp_path)
    with pytest.raises(SessionLockedError) as exc_info:
        acquire_lock(tmp_path)
    assert isinstance(exc_info.value.error_code, str)
    assert exc_info.value.error_code != "unknown"


# ── acquire_lock ── AC 2: stale lock without flag ─────────────────────────────

def test_stale_lock_wrong_sha_without_flag_raises(tmp_path):
    """AC: stale lock (wrong rig sha) without --adopt-stale-lock refuses."""
    _write_stale_lock(tmp_path / "session.lock", pid=os.getpid(), sha="0" * 64)
    with pytest.raises(StaleLockNeedsAdoptionError) as exc_info:
        acquire_lock(tmp_path)
    assert exc_info.value.error_code == LOCK_STALE_NEEDS_ADOPTION


def test_stale_lock_dead_pid_without_flag_raises(tmp_path):
    """AC: stale lock (dead PID) without --adopt-stale-lock refuses."""
    _write_stale_lock(tmp_path / "session.lock", pid=2**20 + 7)
    with pytest.raises(StaleLockNeedsAdoptionError):
        acquire_lock(tmp_path)


def test_stale_lock_error_code_is_named_string(tmp_path):
    _write_stale_lock(tmp_path / "session.lock", pid=os.getpid(), sha="0" * 64)
    with pytest.raises(StaleLockNeedsAdoptionError) as exc_info:
        acquire_lock(tmp_path)
    assert isinstance(exc_info.value.error_code, str)
    assert exc_info.value.error_code != "unknown"


# ── acquire_lock ── AC 3: stale lock with flag ────────────────────────────────

def test_stale_lock_with_flag_succeeds(tmp_path):
    """AC: --adopt-stale-lock succeeds and returns new lock."""
    events_path = tmp_path / "manifest_events.jsonl"
    _open_session(events_path)
    _write_stale_lock(tmp_path / "session.lock", pid=os.getpid(), sha="0" * 64)
    lock = acquire_lock(tmp_path, adopt_stale=True)
    assert lock["pid"] == os.getpid()
    assert lock["rig_self_sha256"] == _current_rig_sha256()


def test_stale_lock_adoption_emits_lock_adopted_event(tmp_path):
    """AC: adoption records the lock_adopted event."""
    events_path = tmp_path / "manifest_events.jsonl"
    _open_session(events_path)

    old_pid = os.getpid()
    old_mono = 99999
    _write_stale_lock(tmp_path / "session.lock", pid=old_pid, sha="0" * 64)
    # Manually set start_monotonic in the stale lock.
    lock_path = tmp_path / "session.lock"
    stale = json.loads(lock_path.read_text())
    stale["start_monotonic"] = old_mono
    lock_path.write_text(json.dumps(stale))

    acquire_lock(tmp_path, adopt_stale=True)

    events = read_events(events_path)
    adoption_events = [e for e in events if e["event_type"] == "lock_adopted"]
    assert len(adoption_events) == 1

    payload = adoption_events[0]["payload"]
    assert payload["old_lock"]["pid"] == old_pid
    assert payload["old_lock"]["start_monotonic"] == old_mono
    assert payload["old_lock"]["rig_self_sha256"] == "0" * 64
    assert "lock_path" in payload


def test_adoption_event_old_lock_has_all_four_fields(tmp_path):
    events_path = tmp_path / "manifest_events.jsonl"
    _open_session(events_path)
    _write_stale_lock(tmp_path / "session.lock", pid=os.getpid(), sha="0" * 64)
    acquire_lock(tmp_path, adopt_stale=True)

    events = read_events(events_path)
    adoption = next(e for e in events if e["event_type"] == "lock_adopted")
    for field in ("pid", "start_monotonic", "rig_self_sha256", "hostname_hash"):
        assert field in adoption["payload"]["old_lock"], f"missing: {field}"


def test_adoption_overwrites_lock_with_new_pid(tmp_path):
    events_path = tmp_path / "manifest_events.jsonl"
    _open_session(events_path)
    _write_stale_lock(tmp_path / "session.lock", pid=os.getpid(), sha="0" * 64)
    acquire_lock(tmp_path, adopt_stale=True)

    stored = json.loads((tmp_path / "session.lock").read_text())
    assert stored["pid"] == os.getpid()
    assert stored["rig_self_sha256"] == _current_rig_sha256()


def test_adoption_event_passes_chain_verification(tmp_path):
    from rig.manifest import verify_chain
    events_path = tmp_path / "manifest_events.jsonl"
    _open_session(events_path)
    _write_stale_lock(tmp_path / "session.lock", pid=os.getpid(), sha="0" * 64)
    acquire_lock(tmp_path, adopt_stale=True)
    verify_chain(read_events(events_path))


# ── release_lock ──────────────────────────────────────────────────────────────

def test_release_removes_lock_file(tmp_path):
    acquire_lock(tmp_path)
    release_lock(tmp_path)
    assert not (tmp_path / "session.lock").exists()


def test_release_is_noop_when_file_absent(tmp_path):
    release_lock(tmp_path)  # must not raise


def test_release_noop_after_double_release(tmp_path):
    acquire_lock(tmp_path)
    release_lock(tmp_path)
    release_lock(tmp_path)  # must not raise


def test_reacquire_after_release(tmp_path):
    acquire_lock(tmp_path)
    release_lock(tmp_path)
    lock = acquire_lock(tmp_path)
    assert lock["pid"] == os.getpid()


# ── integration with reconcile ────────────────────────────────────────────────

def test_session_lock_not_flagged_as_orphan(tmp_path):
    """session.lock must be excluded from disk_orphan scanning."""
    events_path = tmp_path / "manifest_events.jsonl"
    _open_session(events_path)
    write_manifest(tmp_path)
    acquire_lock(tmp_path)

    report = reconcile(tmp_path)
    assert report.ok is True, f"unexpected findings: {report.findings}"


# ── CorruptLockError (bounded named error code) ───────────────────────────────

from rig.manifest import CorruptLockError, LOCK_CORRUPT


def test_corrupt_lock_invalid_json_raises(tmp_path):
    """A corrupt (non-JSON) lock file is not treated as stale — it raises CorruptLockError."""
    (tmp_path / "session.lock").write_text("THIS IS NOT JSON", encoding="utf-8")
    with pytest.raises(CorruptLockError) as exc_info:
        acquire_lock(tmp_path)
    assert exc_info.value.error_code == LOCK_CORRUPT


def test_corrupt_lock_error_code_is_named_string():
    assert LOCK_CORRUPT == "corrupt_lock"
    assert LOCK_CORRUPT != "unknown"


def test_corrupt_lock_not_adoptable_without_inspection(tmp_path):
    """Corrupt lock cannot be silently adopted — CorruptLockError always raised."""
    (tmp_path / "session.lock").write_text("{bad json", encoding="utf-8")
    with pytest.raises(CorruptLockError):
        acquire_lock(tmp_path, adopt_stale=True)


def test_corrupt_lock_distinct_from_stale(tmp_path):
    """CorruptLockError is a different exception type from StaleLockNeedsAdoptionError."""
    (tmp_path / "session.lock").write_text("not json", encoding="utf-8")
    with pytest.raises(CorruptLockError):
        acquire_lock(tmp_path)
    # Verify it's NOT a StaleLockNeedsAdoptionError.
    assert not issubclass(CorruptLockError, StaleLockNeedsAdoptionError)