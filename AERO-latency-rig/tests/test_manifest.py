"""
Tests for rig/manifest.py — T1.1 (final, confirmed payload shapes).

Sections:
  1. append_event  — field structure, type enforcement, payload validation
  2. read_events   — absent file, order, truncation vs corruption
  3. verify_chain  — intact chain, tampered body/prev/tail, first-null rule
  4. replay        — minimal chain projection, determinism, prefix
  5. payload validation — per-type closed shape, enum constraints, nested objects
  6. schema        — manifest_event.schema.json structural checks
  7. integration   — full append → read → verify → replay round-trip
"""

import json
import time
from pathlib import Path

import pytest

from rig.manifest import (
    ChainError,
    ChainProjection,
    InvalidPayloadError,
    TruncatedLogError,
    UnknownEventTypeError,
    KNOWN_EVENT_TYPES,
    _CRASH_CLASSIFICATIONS,
    _INVALIDATION_REASON_CODES,
    _VERDICT_VALUES,
    _compute_event_sha256,
    append_event,
    read_events,
    replay,
    verify_chain,
)
from rig.schemas import SchemaValidationError, validate


# ── canonical valid payloads (confirmed spec) ─────────────────────────────────

SHA64 = "a" * 64

_VALID_PAYLOADS: dict[str, dict] = {
    "session_opened": {
        "session_id": "ses-001",
        "rig_version": "0.1.0",
        "rig_self_sha256": SHA64,
        "git_commit": "abc1234",
    },
    "state_transition": {
        "from_state": "OPEN",
        "to_state": "RUNNING",
    },
    "artifact_created": {
        "artifact_type": "run_record",
        "path": "/runs/r1/run_record.json",
        "sha256": SHA64,
        "artifact_schema_version": 1,
    },
    "artifact_verified": {
        "artifact_type": "run_record",
        "path": "/runs/r1/run_record.json",
        "expected_sha256": SHA64,
        "observed_sha256": SHA64,
        "ok": True,
    },
    "session_finalizing": {
        "completeness_sha256": SHA64,
        "reconciliation_sha256": SHA64,
    },
    "session_closed": {
        "verdict": "GO",
        "decision_report_sha256": SHA64,
        "closure_sha256": SHA64,
    },
    "session_invalidated": {
        "reason_codes": ["thermal_event"],
        "invalidation_artifact_sha256": SHA64,
    },
    "session_incomplete": {
        "crash_classification": "crash_in_run",
        "last_completed_run_ordinal": 3,
    },
    "lock_adopted": {
        "lock_path": "/sessions/ses-001/session.lock",
        "old_lock": {
            "pid": 12345,
            "start_monotonic": 9876543210,
            "rig_self_sha256": SHA64,
            "hostname_hash": SHA64,
        },
    },
    "state_transition_illegal": {
        "from_state": "CLOSED",
        "attempted_to_state": "OPEN",
    },
}


# ── helpers ───────────────────────────────────────────────────────────────────

def _ts() -> tuple[int, str]:
    return time.monotonic_ns(), "2025-06-01T12:00:00Z"


def _open(path: Path) -> str:
    ns, wall = _ts()
    return append_event(path, "session_opened",
                        _VALID_PAYLOADS["session_opened"].copy(),
                        None, ns, wall)


def _chain(path: Path, n: int) -> list[str]:
    shas: list[str] = []
    sha = _open(path)
    shas.append(sha)
    for _ in range(n - 1):
        ns, wall = _ts()
        sha = append_event(path, "state_transition",
                           _VALID_PAYLOADS["state_transition"].copy(),
                           sha, ns, wall)
        shas.append(sha)
    return shas


# ═══════════════════════════════════════════════════════════════════════════
# 1. append_event — structure
# ═══════════════════════════════════════════════════════════════════════════

def test_append_returns_64_lowercase_hex(tmp_path):
    h = _open(tmp_path / "e.jsonl")
    assert len(h) == 64 and h == h.lower()
    assert all(c in "0123456789abcdef" for c in h)


def test_append_creates_file(tmp_path):
    p = tmp_path / "e.jsonl"
    assert not p.exists()
    _open(p)
    assert p.exists()


def test_append_event_stores_seven_fields(tmp_path):
    p = tmp_path / "e.jsonl"
    _open(p)
    r = json.loads(p.read_text().strip())
    for f in ("event_id", "ts_monotonic_ns", "ts_wall", "event_type",
              "payload", "prev_event_sha256", "event_sha256"):
        assert f in r, f"missing: {f}"


def test_append_first_event_prev_is_null(tmp_path):
    p = tmp_path / "e.jsonl"
    _open(p)
    assert json.loads(p.read_text().strip())["prev_event_sha256"] is None


def test_append_second_event_prev_matches_first(tmp_path):
    p = tmp_path / "e.jsonl"
    sha1 = _open(p)
    ns, wall = _ts()
    append_event(p, "state_transition",
                 _VALID_PAYLOADS["state_transition"].copy(), sha1, ns, wall)
    lines = [json.loads(l) for l in p.read_text().splitlines() if l.strip()]
    assert lines[1]["prev_event_sha256"] == sha1


def test_append_returned_sha_matches_stored(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open(p)
    assert json.loads(p.read_text().strip())["event_sha256"] == sha


def test_append_event_sha256_recomputable(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open(p)
    r = json.loads(p.read_text().strip())
    body = {k: v for k, v in r.items() if k != "event_sha256"}
    assert _compute_event_sha256(body) == sha


def test_append_unknown_type_raises(tmp_path):
    ns, wall = _ts()
    with pytest.raises(UnknownEventTypeError):
        append_event(tmp_path / "e.jsonl", "bad_type", {}, None, ns, wall)


def test_append_unknown_type_does_not_create_file(tmp_path):
    p = tmp_path / "e.jsonl"
    ns, wall = _ts()
    with pytest.raises(UnknownEventTypeError):
        append_event(p, "bad_type", {}, None, ns, wall)
    assert not p.exists()


def test_append_all_known_types_accepted(tmp_path):
    p = tmp_path / "e.jsonl"
    prev = None
    for etype in KNOWN_EVENT_TYPES:
        ns, wall = _ts()
        prev = append_event(p, etype, _VALID_PAYLOADS[etype].copy(), prev, ns, wall)


# ═══════════════════════════════════════════════════════════════════════════
# 2. read_events
# ═══════════════════════════════════════════════════════════════════════════

def test_read_absent_returns_empty(tmp_path):
    assert read_events(tmp_path / "e.jsonl") == []


def test_read_returns_events_in_order(tmp_path):
    p = tmp_path / "e.jsonl"
    _chain(p, 3)
    events = read_events(p)
    assert len(events) == 3
    assert events[0]["event_type"] == "session_opened"
    assert events[1]["event_type"] == "state_transition"


def test_read_seven_fields_present(tmp_path):
    p = tmp_path / "e.jsonl"
    _open(p)
    e = read_events(p)[0]
    for f in ("event_id", "ts_monotonic_ns", "ts_wall", "event_type",
              "payload", "prev_event_sha256", "event_sha256"):
        assert f in e


def test_read_truncated_last_line_raises_truncated_error(tmp_path):
    p = tmp_path / "e.jsonl"
    _open(p)
    with open(p, "a", encoding="utf-8") as f:
        f.write('{"event_id":"partial')
    with pytest.raises(TruncatedLogError):
        read_events(p)


def test_read_corrupted_non_final_line_raises_chain_error(tmp_path):
    p = tmp_path / "e.jsonl"
    _chain(p, 3)
    lines = p.read_text().splitlines()
    lines[0] = '{"broken'
    p.write_text("\n".join(lines) + "\n")
    with pytest.raises(ChainError):
        read_events(p)


# ═══════════════════════════════════════════════════════════════════════════
# 3. verify_chain
# ═══════════════════════════════════════════════════════════════════════════

def test_verify_empty_passes():
    verify_chain([])


def test_verify_single_event_passes(tmp_path):
    p = tmp_path / "e.jsonl"
    _open(p)
    verify_chain(read_events(p))


def test_verify_multi_event_passes(tmp_path):
    p = tmp_path / "e.jsonl"
    _chain(p, 5)
    verify_chain(read_events(p))


def test_verify_detects_tampered_body(tmp_path):
    p = tmp_path / "e.jsonl"
    _chain(p, 2)
    events = read_events(p)
    events[0] = {**events[0], "ts_wall": "tampered"}
    with pytest.raises(ChainError):
        verify_chain(events)


def test_verify_detects_tampered_prev(tmp_path):
    p = tmp_path / "e.jsonl"
    _chain(p, 2)
    events = read_events(p)
    events[1] = {**events[1], "prev_event_sha256": "b" * 64}
    with pytest.raises(ChainError):
        verify_chain(events)


def test_verify_detects_tampered_tail(tmp_path):
    """Tail event must be as tamper-evident as any other."""
    p = tmp_path / "e.jsonl"
    _chain(p, 3)
    events = read_events(p)
    events[-1] = {**events[-1], "ts_wall": "tampered-tail"}
    with pytest.raises(ChainError):
        verify_chain(events)


def test_verify_detects_replaced_event_sha256(tmp_path):
    p = tmp_path / "e.jsonl"
    _open(p)
    events = read_events(p)
    events[0] = {**events[0], "event_sha256": "c" * 64}
    with pytest.raises(ChainError):
        verify_chain(events)


def test_verify_first_event_non_null_prev_fails(tmp_path):
    p = tmp_path / "e.jsonl"
    _open(p)
    events = read_events(p)
    events[0] = {**events[0], "prev_event_sha256": "d" * 64}
    with pytest.raises(ChainError):
        verify_chain(events)


def test_verify_error_names_event_id(tmp_path):
    p = tmp_path / "e.jsonl"
    _chain(p, 3)
    events = read_events(p)
    target = events[1]["event_id"]
    events[1] = {**events[1], "prev_event_sha256": "e" * 64}
    with pytest.raises(ChainError, match=target):
        verify_chain(events)


# ═══════════════════════════════════════════════════════════════════════════
# 4. replay
# ═══════════════════════════════════════════════════════════════════════════

def test_replay_empty_returns_defaults():
    proj = replay([])
    assert proj.events_count == 0
    assert proj.last_event_sha256 == ""


def test_replay_counts_all_events(tmp_path):
    p = tmp_path / "e.jsonl"
    _chain(p, 4)
    assert replay(read_events(p)).events_count == 4


def test_replay_last_sha256_matches_final_event(tmp_path):
    p = tmp_path / "e.jsonl"
    shas = _chain(p, 3)
    assert replay(read_events(p)).last_event_sha256 == shas[-1]


def test_replay_deterministic(tmp_path):
    p = tmp_path / "e.jsonl"
    _chain(p, 3)
    events = read_events(p)
    assert replay(events).events_count == replay(events).events_count
    assert replay(events).last_event_sha256 == replay(events).last_event_sha256


def test_replay_prefix(tmp_path):
    p = tmp_path / "e.jsonl"
    _chain(p, 5)
    events = read_events(p)
    assert replay(events[:2]).events_count == 2
    assert replay(events).events_count == 5


def test_replay_tip_usable_as_next_prev(tmp_path):
    p = tmp_path / "e.jsonl"
    _chain(p, 3)
    proj = replay(read_events(p))
    ns, wall = _ts()
    append_event(p, "session_finalizing",
                 _VALID_PAYLOADS["session_finalizing"].copy(),
                 proj.last_event_sha256, ns, wall)
    verify_chain(read_events(p))


def test_replay_returns_chain_projection_type(tmp_path):
    p = tmp_path / "e.jsonl"
    _open(p)
    assert isinstance(replay(read_events(p)), ChainProjection)


# ═══════════════════════════════════════════════════════════════════════════
# 5. payload validation — confirmed field sets and semantic constraints
# ═══════════════════════════════════════════════════════════════════════════

def test_payload_missing_field_raises(tmp_path):
    ns, wall = _ts()
    bad = {"session_id": "s1", "rig_version": "0.1"}  # missing rig_self_sha256, git_commit
    with pytest.raises(InvalidPayloadError, match="missing"):
        append_event(tmp_path / "e.jsonl", "session_opened", bad, None, ns, wall)


def test_payload_extra_field_raises(tmp_path):
    ns, wall = _ts()
    bad = {**_VALID_PAYLOADS["session_opened"], "extra": "bad"}
    with pytest.raises(InvalidPayloadError, match="unexpected"):
        append_event(tmp_path / "e.jsonl", "session_opened", bad, None, ns, wall)


def test_payload_session_opened_requires_rig_self_sha256(tmp_path):
    ns, wall = _ts()
    bad = {"session_id": "s1", "rig_version": "0.1", "git_commit": "abc"}
    with pytest.raises(InvalidPayloadError, match="rig_self_sha256"):
        append_event(tmp_path / "e.jsonl", "session_opened", bad, None, ns, wall)


def test_payload_session_opened_requires_git_commit(tmp_path):
    ns, wall = _ts()
    bad = {"session_id": "s1", "rig_version": "0.1", "rig_self_sha256": SHA64}
    with pytest.raises(InvalidPayloadError, match="git_commit"):
        append_event(tmp_path / "e.jsonl", "session_opened", bad, None, ns, wall)


def test_payload_artifact_created_requires_artifact_schema_version(tmp_path):
    ns, wall = _ts()
    bad = {"artifact_type": "run_record", "path": "/r", "sha256": SHA64}
    with pytest.raises(InvalidPayloadError, match="artifact_schema_version"):
        append_event(tmp_path / "e.jsonl", "artifact_created", bad, None, ns, wall)


def test_payload_artifact_created_unknown_type_raises(tmp_path):
    ns, wall = _ts()
    bad = {"artifact_type": "not_a_real_type", "path": "/r",
           "sha256": SHA64, "artifact_schema_version": 1}
    with pytest.raises(InvalidPayloadError, match="no registered schema"):
        append_event(tmp_path / "e.jsonl", "artifact_created", bad, None, ns, wall)


def test_payload_artifact_verified_requires_expected_and_observed_sha256(tmp_path):
    ns, wall = _ts()
    bad = {"artifact_type": "run_record", "path": "/r",
           "sha256": SHA64, "ok": True}
    with pytest.raises(InvalidPayloadError):
        append_event(tmp_path / "e.jsonl", "artifact_verified", bad, None, ns, wall)


def test_payload_artifact_verified_ok_must_be_bool(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open(p)
    ns, wall = _ts()
    bad = {**_VALID_PAYLOADS["artifact_verified"], "ok": "yes"}
    with pytest.raises(InvalidPayloadError, match="boolean"):
        append_event(p, "artifact_verified", bad, sha, ns, wall)


def test_payload_session_finalizing_requires_both_hashes(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open(p)
    ns, wall = _ts()
    with pytest.raises(InvalidPayloadError):
        append_event(p, "session_finalizing",
                     {"completeness_sha256": SHA64}, sha, ns, wall)


def test_payload_session_closed_bad_verdict_raises(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open(p)
    ns, wall = _ts()
    bad = {"verdict": "MAYBE", "decision_report_sha256": SHA64,
           "closure_sha256": SHA64}
    with pytest.raises(InvalidPayloadError, match="verdict"):
        append_event(p, "session_closed", bad, sha, ns, wall)


def test_payload_session_closed_all_verdicts_accepted(tmp_path):
    for verdict in _VERDICT_VALUES:
        p = tmp_path / f"e_{verdict}.jsonl"
        sha = _open(p)
        ns, wall = _ts()
        payload = {"verdict": verdict, "decision_report_sha256": SHA64,
                   "closure_sha256": SHA64}
        append_event(p, "session_closed", payload, sha, ns, wall)


def test_payload_session_invalidated_empty_reason_codes_raises(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open(p)
    ns, wall = _ts()
    bad = {"reason_codes": [], "invalidation_artifact_sha256": SHA64}
    with pytest.raises(InvalidPayloadError, match="non-empty"):
        append_event(p, "session_invalidated", bad, sha, ns, wall)


def test_payload_session_invalidated_unknown_reason_code_raises(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open(p)
    ns, wall = _ts()
    bad = {"reason_codes": ["unknown_code"], "invalidation_artifact_sha256": SHA64}
    with pytest.raises(InvalidPayloadError):
        append_event(p, "session_invalidated", bad, sha, ns, wall)


def test_payload_session_invalidated_multiple_codes_accepted(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open(p)
    ns, wall = _ts()
    payload = {"reason_codes": ["thermal_event", "network_floor_breach"],
               "invalidation_artifact_sha256": SHA64}
    append_event(p, "session_invalidated", payload, sha, ns, wall)


def test_payload_session_invalidated_all_codes_individually_valid(tmp_path):
    for code in _INVALIDATION_REASON_CODES:
        p = tmp_path / f"e_{code}.jsonl"
        sha = _open(p)
        ns, wall = _ts()
        payload = {"reason_codes": [code], "invalidation_artifact_sha256": SHA64}
        append_event(p, "session_invalidated", payload, sha, ns, wall)


def test_payload_session_incomplete_bad_classification_raises(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open(p)
    ns, wall = _ts()
    bad = {"crash_classification": "crashed_badly",
           "last_completed_run_ordinal": None}
    with pytest.raises(InvalidPayloadError, match="crash_classification"):
        append_event(p, "session_incomplete", bad, sha, ns, wall)


def test_payload_session_incomplete_null_ordinal_accepted(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open(p)
    ns, wall = _ts()
    payload = {"crash_classification": "crash_in_preflight",
               "last_completed_run_ordinal": None}
    append_event(p, "session_incomplete", payload, sha, ns, wall)


def test_payload_session_incomplete_int_ordinal_accepted(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open(p)
    ns, wall = _ts()
    payload = {"crash_classification": "crash_in_run",
               "last_completed_run_ordinal": 5}
    append_event(p, "session_incomplete", payload, sha, ns, wall)


def test_payload_session_incomplete_all_classifications_valid(tmp_path):
    for cls in _CRASH_CLASSIFICATIONS:
        p = tmp_path / f"e_{cls}.jsonl"
        sha = _open(p)
        ns, wall = _ts()
        payload = {"crash_classification": cls, "last_completed_run_ordinal": None}
        append_event(p, "session_incomplete", payload, sha, ns, wall)


def test_payload_lock_adopted_missing_old_lock_raises(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open(p)
    ns, wall = _ts()
    with pytest.raises(InvalidPayloadError):
        append_event(p, "lock_adopted", {"lock_path": "/session.lock"}, sha, ns, wall)


def test_payload_lock_adopted_old_lock_extra_field_raises(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open(p)
    ns, wall = _ts()
    bad_old_lock = {**_VALID_PAYLOADS["lock_adopted"]["old_lock"], "extra": "bad"}
    bad = {"lock_path": "/session.lock", "old_lock": bad_old_lock}
    with pytest.raises(InvalidPayloadError, match="unexpected"):
        append_event(p, "lock_adopted", bad, sha, ns, wall)


def test_payload_lock_adopted_old_lock_missing_field_raises(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open(p)
    ns, wall = _ts()
    bad_old_lock = {"pid": 1, "start_monotonic": 0, "rig_self_sha256": SHA64}
    bad = {"lock_path": "/session.lock", "old_lock": bad_old_lock}
    with pytest.raises(InvalidPayloadError, match="hostname_hash"):
        append_event(p, "lock_adopted", bad, sha, ns, wall)


# ═══════════════════════════════════════════════════════════════════════════
# 6. schema validation
# ═══════════════════════════════════════════════════════════════════════════

def _schema_event(event_type="session_opened", prev=None):
    return {
        "event_id": "test-uuid",
        "ts_monotonic_ns": 1000000,
        "ts_wall": "2025-06-01T12:00:00Z",
        "event_type": event_type,
        "payload": {},
        "prev_event_sha256": prev,
        "event_sha256": SHA64,
    }


def test_schema_valid_event_passes():
    validate("manifest_event", _schema_event())


def test_schema_all_event_types_valid():
    for etype in KNOWN_EVENT_TYPES:
        validate("manifest_event", _schema_event(event_type=etype))


def test_schema_missing_event_sha256_rejected():
    bad = _schema_event()
    del bad["event_sha256"]
    with pytest.raises(SchemaValidationError):
        validate("manifest_event", bad)


def test_schema_missing_prev_event_sha256_rejected():
    bad = _schema_event()
    del bad["prev_event_sha256"]
    with pytest.raises(SchemaValidationError):
        validate("manifest_event", bad)


def test_schema_null_prev_valid():
    validate("manifest_event", _schema_event(prev=None))


def test_schema_hex64_prev_valid():
    validate("manifest_event", _schema_event(prev="b" * 64))


def test_schema_short_sha256_rejected():
    with pytest.raises(SchemaValidationError):
        validate("manifest_event", {**_schema_event(), "event_sha256": "abc"})


def test_schema_unknown_event_type_rejected():
    with pytest.raises(SchemaValidationError):
        validate("manifest_event", _schema_event(event_type="bogus"))


def test_schema_extra_field_rejected():
    with pytest.raises(SchemaValidationError):
        validate("manifest_event", {**_schema_event(), "notes": "bad"})


# ═══════════════════════════════════════════════════════════════════════════
# 7. integration
# ═══════════════════════════════════════════════════════════════════════════

def test_full_round_trip(tmp_path):
    p = tmp_path / "manifest_events.jsonl"
    ns, wall = _ts()

    sha = append_event(p, "session_opened",
                       _VALID_PAYLOADS["session_opened"].copy(), None, ns, wall)
    sha = append_event(p, "state_transition",
                       _VALID_PAYLOADS["state_transition"].copy(), sha, ns, wall)
    sha = append_event(p, "session_finalizing",
                       _VALID_PAYLOADS["session_finalizing"].copy(), sha, ns, wall)
    sha = append_event(p, "session_closed",
                       _VALID_PAYLOADS["session_closed"].copy(), sha, ns, wall)

    events = read_events(p)
    assert len(events) == 4
    verify_chain(events)
    proj = replay(events)
    assert proj.events_count == 4
    assert proj.last_event_sha256 == sha


def test_written_events_pass_schema(tmp_path):
    p = tmp_path / "e.jsonl"
    prev = None
    for etype in KNOWN_EVENT_TYPES:
        ns, wall = _ts()
        prev = append_event(p, etype, _VALID_PAYLOADS[etype].copy(), prev, ns, wall)
    for event in read_events(p):
        validate("manifest_event", event)


def test_all_sha256_values_distinct(tmp_path):
    p = tmp_path / "e.jsonl"
    shas = _chain(p, 5)
    assert len(set(shas)) == 5