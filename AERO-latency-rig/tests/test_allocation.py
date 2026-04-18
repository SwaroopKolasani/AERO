"""
Tests for rig/allocation.py — T3.1.

Covered:
  append_event   — all four event types; payload validation; chain production
  read_events    — absent file, truncation detection, corruption detection
  verify_chain   — intact, tampered body, tampered prev, tampered tail
  active_vms     — allocation tracking; terminated VMs removed
  active_s3_objects — same pattern for S3 objects
  schema         — allocation_registry_event.schema.json validates written events
"""

import json
import time
from pathlib import Path

import pytest

from rig.allocation import (
    AllocationChainError,
    AllocationTruncatedError,
    InvalidAllocationPayload,
    KNOWN_EVENT_TYPES,
    UnknownAllocationEventType,
    _event_sha256,
    active_s3_objects,
    active_vms,
    append_event,
    read_events,
    verify_chain,
)
from rig.schemas import SchemaValidationError, validate


# ── canonical valid payloads ──────────────────────────────────────────────────

SHA64 = "a" * 64

_PAYLOADS: dict[str, dict] = {
    "vm_allocated": {
        "instance_id":   "i-0123456789abcdef0",
        "ami_id":        "ami-0deadbeef1234567",
        "instance_type": "t3.micro",
        "session_id":    "ses-001",
        "run_id":        "run-001",
    },
    "vm_terminated": {
        "instance_id": "i-0123456789abcdef0",
    },
    "s3_object_created": {
        "key":        "sessions/ses-001/input.zip",
        "version_id": "abc123",
        "sha256":     SHA64,
        "size_bytes": 4096,
        "purpose":    "input_archive",
        "session_id": "ses-001",
        "run_id":     "run-001",
    },
    "s3_object_deleted": {
        "key":        "sessions/ses-001/input.zip",
        "version_id": "abc123",
    },
}


def _ts() -> tuple[int, str]:
    return time.monotonic_ns(), "2025-06-01T12:00:00Z"


def _chain(path: Path, n: int = 2) -> list[str]:
    """Append n events (vm_allocated then vm_terminated); return sha list."""
    shas: list[str] = []
    ns, wall = _ts()
    sha = append_event(path, "vm_allocated", _PAYLOADS["vm_allocated"].copy(),
                       None, ns, wall)
    shas.append(sha)
    for _ in range(n - 1):
        sha = append_event(path, "vm_terminated", _PAYLOADS["vm_terminated"].copy(),
                           sha, ns, wall)
        shas.append(sha)
    return shas


# ── append_event ───────────────────────────────────────────────────────────────

def test_append_returns_64_hex(tmp_path):
    h = append_event(tmp_path / "r.jsonl", "vm_allocated",
                     _PAYLOADS["vm_allocated"].copy(), None, *_ts())
    assert len(h) == 64
    assert all(c in "0123456789abcdef" for c in h)


def test_append_creates_file(tmp_path):
    p = tmp_path / "r.jsonl"
    assert not p.exists()
    append_event(p, "vm_allocated", _PAYLOADS["vm_allocated"].copy(), None, *_ts())
    assert p.exists()


def test_append_seven_fields_stored(tmp_path):
    p = tmp_path / "r.jsonl"
    append_event(p, "vm_allocated", _PAYLOADS["vm_allocated"].copy(), None, *_ts())
    record = json.loads(p.read_text().strip())
    for field in ("event_id", "ts_monotonic_ns", "ts_wall", "event_type",
                  "payload", "prev_event_sha256", "event_sha256"):
        assert field in record, f"missing: {field}"


def test_append_first_event_prev_is_null(tmp_path):
    p = tmp_path / "r.jsonl"
    append_event(p, "vm_allocated", _PAYLOADS["vm_allocated"].copy(), None, *_ts())
    assert json.loads(p.read_text().strip())["prev_event_sha256"] is None


def test_append_returned_sha_matches_stored(tmp_path):
    p = tmp_path / "r.jsonl"
    sha = append_event(p, "vm_allocated", _PAYLOADS["vm_allocated"].copy(), None, *_ts())
    assert json.loads(p.read_text().strip())["event_sha256"] == sha


def test_append_sha256_recomputable(tmp_path):
    p = tmp_path / "r.jsonl"
    sha = append_event(p, "vm_allocated", _PAYLOADS["vm_allocated"].copy(), None, *_ts())
    record = json.loads(p.read_text().strip())
    body = {k: v for k, v in record.items() if k != "event_sha256"}
    assert _event_sha256(body) == sha


def test_append_all_four_event_types(tmp_path):
    p = tmp_path / "r.jsonl"
    prev = None
    for etype in KNOWN_EVENT_TYPES:
        ns, wall = _ts()
        prev = append_event(p, etype, _PAYLOADS[etype].copy(), prev, ns, wall)
    assert len(read_events(p)) == 4


def test_append_unknown_type_raises(tmp_path):
    with pytest.raises(UnknownAllocationEventType):
        append_event(tmp_path / "r.jsonl", "unknown_type", {}, None, *_ts())


def test_append_unknown_type_no_file(tmp_path):
    p = tmp_path / "r.jsonl"
    with pytest.raises(UnknownAllocationEventType):
        append_event(p, "unknown_type", {}, None, *_ts())
    assert not p.exists()


def test_append_missing_payload_field_raises(tmp_path):
    with pytest.raises(InvalidAllocationPayload, match="missing"):
        append_event(tmp_path / "r.jsonl", "vm_allocated",
                     {"instance_id": "i-1"}, None, *_ts())


def test_append_extra_payload_field_raises(tmp_path):
    bad = {**_PAYLOADS["vm_allocated"], "extra": "bad"}
    with pytest.raises(InvalidAllocationPayload, match="unexpected"):
        append_event(tmp_path / "r.jsonl", "vm_allocated", bad, None, *_ts())


def test_append_invalid_s3_purpose_raises(tmp_path):
    bad = {**_PAYLOADS["s3_object_created"], "purpose": "not_a_real_purpose"}
    with pytest.raises(InvalidAllocationPayload, match="purpose"):
        append_event(tmp_path / "r.jsonl", "s3_object_created", bad, None, *_ts())


def test_append_s3_all_purposes_accepted(tmp_path):
    for purpose in ("input_archive", "output_archive", "sidecar"):
        p = tmp_path / f"{purpose}.jsonl"
        payload = {**_PAYLOADS["s3_object_created"], "purpose": purpose}
        append_event(p, "s3_object_created", payload, None, *_ts())


# ── read_events ────────────────────────────────────────────────────────────────

def test_read_absent_returns_empty(tmp_path):
    assert read_events(tmp_path / "r.jsonl") == []


def test_read_returns_events_in_order(tmp_path):
    p = tmp_path / "r.jsonl"
    _chain(p, n=2)
    events = read_events(p)
    assert len(events) == 2
    assert events[0]["event_type"] == "vm_allocated"
    assert events[1]["event_type"] == "vm_terminated"


def test_read_truncated_last_line_raises(tmp_path):
    p = tmp_path / "r.jsonl"
    _chain(p, n=1)
    with open(p, "a") as f:
        f.write('{"event_id":"partial')
    with pytest.raises(AllocationTruncatedError):
        read_events(p)


def test_read_corrupted_non_final_raises(tmp_path):
    p = tmp_path / "r.jsonl"
    _chain(p, n=2)
    lines = p.read_text().splitlines()
    lines[0] = '{"broken'
    p.write_text("\n".join(lines) + "\n")
    with pytest.raises(AllocationChainError):
        read_events(p)


# ── verify_chain ───────────────────────────────────────────────────────────────

def test_verify_empty_passes():
    verify_chain([])


def test_verify_single_event_passes(tmp_path):
    p = tmp_path / "r.jsonl"
    _chain(p, n=1)
    verify_chain(read_events(p))


def test_verify_chain_of_three_passes(tmp_path):
    p = tmp_path / "r.jsonl"
    ns, wall = _ts()
    sha = append_event(p, "vm_allocated", _PAYLOADS["vm_allocated"].copy(), None, ns, wall)
    sha = append_event(p, "s3_object_created", _PAYLOADS["s3_object_created"].copy(), sha, ns, wall)
    sha = append_event(p, "vm_terminated", _PAYLOADS["vm_terminated"].copy(), sha, ns, wall)
    verify_chain(read_events(p))


def test_verify_detects_tampered_body(tmp_path):
    p = tmp_path / "r.jsonl"
    _chain(p, n=2)
    events = read_events(p)
    events[0] = {**events[0], "ts_wall": "tampered"}
    with pytest.raises(AllocationChainError):
        verify_chain(events)


def test_verify_detects_tampered_tail(tmp_path):
    """Tail event must be as tamper-evident as any other."""
    p = tmp_path / "r.jsonl"
    _chain(p, n=2)
    events = read_events(p)
    events[-1] = {**events[-1], "ts_wall": "tampered-tail"}
    with pytest.raises(AllocationChainError):
        verify_chain(events)


def test_verify_detects_tampered_prev(tmp_path):
    p = tmp_path / "r.jsonl"
    _chain(p, n=2)
    events = read_events(p)
    events[1] = {**events[1], "prev_event_sha256": "b" * 64}
    with pytest.raises(AllocationChainError):
        verify_chain(events)


def test_verify_first_event_non_null_prev_fails(tmp_path):
    p = tmp_path / "r.jsonl"
    _chain(p, n=1)
    events = read_events(p)
    events[0] = {**events[0], "prev_event_sha256": "c" * 64}
    with pytest.raises(AllocationChainError):
        verify_chain(events)


def test_verify_error_names_event_id(tmp_path):
    p = tmp_path / "r.jsonl"
    _chain(p, n=2)
    events = read_events(p)
    target = events[1]["event_id"]
    events[1] = {**events[1], "prev_event_sha256": "d" * 64}
    with pytest.raises(AllocationChainError, match=target):
        verify_chain(events)


# ── active_vms and active_s3_objects ─────────────────────────────────────────

def test_active_vms_allocated_appears(tmp_path):
    p = tmp_path / "r.jsonl"
    ns, wall = _ts()
    append_event(p, "vm_allocated", _PAYLOADS["vm_allocated"].copy(), None, ns, wall)
    vms = active_vms(read_events(p))
    assert len(vms) == 1
    assert vms[0]["instance_id"] == "i-0123456789abcdef0"


def test_active_vms_terminated_removed(tmp_path):
    p = tmp_path / "r.jsonl"
    ns, wall = _ts()
    sha = append_event(p, "vm_allocated", _PAYLOADS["vm_allocated"].copy(), None, ns, wall)
    append_event(p, "vm_terminated", _PAYLOADS["vm_terminated"].copy(), sha, ns, wall)
    assert active_vms(read_events(p)) == []


def test_active_vms_multiple_tracked(tmp_path):
    p = tmp_path / "r.jsonl"
    ns, wall = _ts()
    p1 = {**_PAYLOADS["vm_allocated"], "instance_id": "i-aaa"}
    p2 = {**_PAYLOADS["vm_allocated"], "instance_id": "i-bbb"}
    sha = append_event(p, "vm_allocated", p1, None, ns, wall)
    sha = append_event(p, "vm_allocated", p2, sha, ns, wall)
    # Terminate only the first.
    append_event(p, "vm_terminated", {"instance_id": "i-aaa"}, sha, ns, wall)
    vms = active_vms(read_events(p))
    assert len(vms) == 1
    assert vms[0]["instance_id"] == "i-bbb"


def test_active_s3_objects_created_appears(tmp_path):
    p = tmp_path / "r.jsonl"
    ns, wall = _ts()
    append_event(p, "s3_object_created", _PAYLOADS["s3_object_created"].copy(), None, ns, wall)
    objs = active_s3_objects(read_events(p))
    assert len(objs) == 1
    assert objs[0]["key"] == "sessions/ses-001/input.zip"


def test_active_s3_objects_deleted_removed(tmp_path):
    p = tmp_path / "r.jsonl"
    ns, wall = _ts()
    sha = append_event(p, "s3_object_created",
                       _PAYLOADS["s3_object_created"].copy(), None, ns, wall)
    append_event(p, "s3_object_deleted",
                 _PAYLOADS["s3_object_deleted"].copy(), sha, ns, wall)
    assert active_s3_objects(read_events(p)) == []


def test_active_s3_keyed_by_key_and_version(tmp_path):
    """Two objects with the same key but different version_id are both tracked."""
    p = tmp_path / "r.jsonl"
    ns, wall = _ts()
    p1 = {**_PAYLOADS["s3_object_created"], "version_id": "v1"}
    p2 = {**_PAYLOADS["s3_object_created"], "version_id": "v2"}
    sha = append_event(p, "s3_object_created", p1, None, ns, wall)
    append_event(p, "s3_object_created", p2, sha, ns, wall)
    objs = active_s3_objects(read_events(p))
    assert len(objs) == 2


# ── schema ────────────────────────────────────────────────────────────────────

def test_schema_validates_written_events(tmp_path):
    p = tmp_path / "r.jsonl"
    ns, wall = _ts()
    sha = append_event(p, "vm_allocated", _PAYLOADS["vm_allocated"].copy(), None, ns, wall)
    append_event(p, "vm_terminated", _PAYLOADS["vm_terminated"].copy(), sha, ns, wall)
    for event in read_events(p):
        validate("allocation_registry_event", event)


def test_schema_all_event_types_valid():
    for etype in KNOWN_EVENT_TYPES:
        validate("allocation_registry_event", {
            "event_id":          "test-uuid",
            "ts_monotonic_ns":   1000000,
            "ts_wall":           "2025-06-01T00:00:00Z",
            "event_type":        etype,
            "payload":           {},
            "prev_event_sha256": None,
            "event_sha256":      "a" * 64,
        })


def test_schema_rejects_unknown_event_type():
    with pytest.raises(SchemaValidationError):
        validate("allocation_registry_event", {
            "event_id":          "test",
            "ts_monotonic_ns":   0,
            "ts_wall":           "2025-06-01T00:00:00Z",
            "event_type":        "unknown_type",
            "payload":           {},
            "prev_event_sha256": None,
            "event_sha256":      "a" * 64,
        })


def test_schema_rejects_missing_field():
    from rig.schemas import SchemaValidationError as SVE
    bad = {
        "event_id":    "test",
        "ts_wall":     "2025-06-01T00:00:00Z",
        "event_type":  "vm_allocated",
        "payload":     {},
        "event_sha256": "a" * 64,
    }  # missing ts_monotonic_ns and prev_event_sha256
    with pytest.raises(SVE):
        validate("allocation_registry_event", bad)


def test_schema_rejects_extra_field():
    with pytest.raises(SchemaValidationError):
        validate("allocation_registry_event", {
            "event_id":          "test",
            "ts_monotonic_ns":   0,
            "ts_wall":           "x",
            "event_type":        "vm_allocated",
            "payload":           {},
            "prev_event_sha256": None,
            "event_sha256":      "a" * 64,
            "extra":             "bad",
        })


# ── integration: full round-trip ──────────────────────────────────────────────

def test_full_round_trip(tmp_path):
    p = tmp_path / "allocation_registry.jsonl"
    ns, wall = _ts()

    sha = append_event(p, "vm_allocated", _PAYLOADS["vm_allocated"].copy(), None, ns, wall)
    sha = append_event(p, "s3_object_created", _PAYLOADS["s3_object_created"].copy(), sha, ns, wall)
    sha = append_event(p, "s3_object_deleted", _PAYLOADS["s3_object_deleted"].copy(), sha, ns, wall)
    sha = append_event(p, "vm_terminated", _PAYLOADS["vm_terminated"].copy(), sha, ns, wall)

    events = read_events(p)
    assert len(events) == 4

    verify_chain(events)

    assert active_vms(events) == []
    assert active_s3_objects(events) == []


def test_all_event_shas_distinct(tmp_path):
    p = tmp_path / "r.jsonl"
    shas: list[str] = []
    prev = None
    ns, wall = _ts()
    for etype, payload in _PAYLOADS.items():
        sha = append_event(p, etype, payload.copy(), prev, ns, wall)
        shas.append(sha)
        prev = sha
    assert len(set(shas)) == 4