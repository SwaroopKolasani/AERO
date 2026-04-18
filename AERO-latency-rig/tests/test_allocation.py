"""
Tests for rig/allocation.py -- T3.1.

AC:
  1. Every entry validates against allocation_entry.schema.json.
  2. Hash chain verifies end-to-end.
  3. Append is atomic via rig/atomic.py.
  4. Tamper test: mutating a prior entry breaks chain verify.
     (Tail entry is also self-authenticating -- verified explicitly.)

Cross-field constraints tested:
  - trust_level must match source_of_record per the frozen mapping table.
  - wal_event_ref must be non-null for wal_event/sidecar sources,
    null for sweep/boto3/head sources.
"""

import json
import time
from pathlib import Path

import pytest

from rig.allocation import (
    FIRST_ENTRY_PREV_SHA256,
    KNOWN_RESOURCE_TYPES,
    KNOWN_SOURCES,
    KNOWN_TRUST_LEVELS,
    RESOURCE_EC2_INSTANCE,
    RESOURCE_S3_OBJECT_VERSION,
    SOURCE_BOTO3_LAUNCH_RESPONSE,
    SOURCE_HEAD_OBJECT_INDEPENDENT,
    SOURCE_SIDECAR_OBSERVED,
    SOURCE_SWEEP_DELAYED,
    SOURCE_SWEEP_POST,
    SOURCE_SWEEP_PRE,
    SOURCE_WAL_EVENT,
    TRUST_AUTHORITATIVE,
    TRUST_INFERRED,
    TRUST_OBSERVED,
    AllocationChainError,
    AllocationTruncatedError,
    InvalidAllocationEntry,
    _compute_entry_sha256,
    append_entry,
    read_entries,
    verify_chain,
)
from rig.schemas import SchemaValidationError, validate


# -- helpers ------------------------------------------------------------------

def _ts() -> int:
    return time.monotonic_ns()


def _append(path: Path, prev: str = FIRST_ENTRY_PREV_SHA256, **overrides) -> str:
    """Append one valid entry with authoritative boto3 defaults; return sha256."""
    defaults = dict(
        observation_ts_monotonic = _ts(),
        source_of_record         = SOURCE_BOTO3_LAUNCH_RESPONSE,
        resource_type            = RESOURCE_EC2_INSTANCE,
        resource_id              = "i-0abc123def456",
        correlation_key          = "run-001/attempt-1",
        originating_run_id       = "run-001",
        wal_event_ref            = None,
        trust_level              = TRUST_AUTHORITATIVE,
    )
    defaults.update(overrides)
    return append_entry(path, prev_entry_sha256=prev, **defaults)


def _chain(path: Path, n: int) -> list[str]:
    shas: list[str] = []
    prev = FIRST_ENTRY_PREV_SHA256
    for _ in range(n):
        sha = _append(path, prev=prev)
        shas.append(sha)
        prev = sha
    return shas


# -- append_entry: structure --------------------------------------------------

def test_append_returns_64_hex(tmp_path):
    h = _append(tmp_path / "r.jsonl")
    assert len(h) == 64
    assert all(c in "0123456789abcdef" for c in h)


def test_append_creates_file(tmp_path):
    p = tmp_path / "r.jsonl"
    assert not p.exists()
    _append(p)
    assert p.exists()


def test_append_stores_eleven_required_fields(tmp_path):
    p = tmp_path / "r.jsonl"
    _append(p)
    record = json.loads(p.read_text().strip())
    expected = {
        "schema_version", "observation_ts_monotonic", "source_of_record",
        "resource_type", "resource_id", "correlation_key",
        "originating_run_id", "wal_event_ref", "trust_level",
        "prev_entry_sha256", "entry_sha256",
    }
    assert set(record.keys()) == expected


def test_first_entry_prev_is_sentinel(tmp_path):
    p = tmp_path / "r.jsonl"
    _append(p)
    stored = json.loads(p.read_text().strip())["prev_entry_sha256"]
    assert stored == FIRST_ENTRY_PREV_SHA256
    assert stored == "0" * 64


def test_second_entry_prev_matches_first_sha(tmp_path):
    p = tmp_path / "r.jsonl"
    sha1 = _append(p)
    _append(p, prev=sha1)
    lines = [json.loads(l) for l in p.read_text().splitlines() if l.strip()]
    assert lines[1]["prev_entry_sha256"] == sha1


def test_returned_sha_matches_stored(tmp_path):
    p = tmp_path / "r.jsonl"
    sha = _append(p)
    assert json.loads(p.read_text().strip())["entry_sha256"] == sha


def test_entry_sha256_recomputable(tmp_path):
    p = tmp_path / "r.jsonl"
    sha = _append(p)
    record = json.loads(p.read_text().strip())
    body = {k: v for k, v in record.items() if k != "entry_sha256"}
    assert _compute_entry_sha256(body) == sha


def test_originating_run_id_nullable(tmp_path):
    p = tmp_path / "r.jsonl"
    _append(p, originating_run_id=None)
    assert json.loads(p.read_text().strip())["originating_run_id"] is None


def test_schema_version_is_one(tmp_path):
    p = tmp_path / "r.jsonl"
    _append(p)
    assert json.loads(p.read_text().strip())["schema_version"] == 1


# -- cross-field: trust_level must match source_of_record --------------------

def test_trust_authoritative_sources_accepted(tmp_path):
    for src in (SOURCE_BOTO3_LAUNCH_RESPONSE, SOURCE_HEAD_OBJECT_INDEPENDENT):
        p = tmp_path / f"{src}.jsonl"
        _append(p, source_of_record=src, trust_level=TRUST_AUTHORITATIVE,
                wal_event_ref=None)


def test_trust_observed_sources_accepted(tmp_path):
    for src in (SOURCE_WAL_EVENT, SOURCE_SIDECAR_OBSERVED):
        p = tmp_path / f"{src}.jsonl"
        _append(p, source_of_record=src, trust_level=TRUST_OBSERVED,
                wal_event_ref="evt-uuid-123")


def test_trust_inferred_sources_accepted(tmp_path):
    for src in (SOURCE_SWEEP_PRE, SOURCE_SWEEP_POST, SOURCE_SWEEP_DELAYED):
        p = tmp_path / f"{src}.jsonl"
        _append(p, source_of_record=src, trust_level=TRUST_INFERRED,
                wal_event_ref=None)


def test_wrong_trust_level_raises(tmp_path):
    """authoritative source must not accept observed trust_level."""
    with pytest.raises(InvalidAllocationEntry, match="trust_level"):
        _append(tmp_path / "r.jsonl",
                source_of_record=SOURCE_BOTO3_LAUNCH_RESPONSE,
                trust_level=TRUST_OBSERVED)


def test_inferred_source_with_authoritative_trust_raises(tmp_path):
    with pytest.raises(InvalidAllocationEntry, match="trust_level"):
        _append(tmp_path / "r.jsonl",
                source_of_record=SOURCE_SWEEP_PRE,
                trust_level=TRUST_AUTHORITATIVE,
                wal_event_ref=None)


# -- cross-field: wal_event_ref nullability per source -----------------------

def test_wal_event_source_requires_non_null_ref(tmp_path):
    with pytest.raises(InvalidAllocationEntry, match="wal_event_ref"):
        _append(tmp_path / "r.jsonl",
                source_of_record=SOURCE_WAL_EVENT,
                trust_level=TRUST_OBSERVED,
                wal_event_ref=None)


def test_sidecar_source_requires_non_null_ref(tmp_path):
    with pytest.raises(InvalidAllocationEntry, match="wal_event_ref"):
        _append(tmp_path / "r.jsonl",
                source_of_record=SOURCE_SIDECAR_OBSERVED,
                trust_level=TRUST_OBSERVED,
                wal_event_ref=None)


def test_boto3_source_must_have_null_ref(tmp_path):
    with pytest.raises(InvalidAllocationEntry, match="wal_event_ref"):
        _append(tmp_path / "r.jsonl",
                source_of_record=SOURCE_BOTO3_LAUNCH_RESPONSE,
                trust_level=TRUST_AUTHORITATIVE,
                wal_event_ref="should-not-be-here")


def test_sweep_source_must_have_null_ref(tmp_path):
    for src in (SOURCE_SWEEP_PRE, SOURCE_SWEEP_POST, SOURCE_SWEEP_DELAYED):
        with pytest.raises(InvalidAllocationEntry, match="wal_event_ref"):
            _append(tmp_path / f"{src}.jsonl",
                    source_of_record=src,
                    trust_level=TRUST_INFERRED,
                    wal_event_ref="non-null")


# -- unknown enum values ------------------------------------------------------

def test_unknown_source_raises(tmp_path):
    with pytest.raises(InvalidAllocationEntry, match="source_of_record"):
        _append(tmp_path / "r.jsonl", source_of_record="made_up_source")


def test_unknown_resource_type_raises(tmp_path):
    with pytest.raises(InvalidAllocationEntry, match="resource_type"):
        _append(tmp_path / "r.jsonl", resource_type="rds_instance")


def test_unknown_trust_level_raises(tmp_path):
    with pytest.raises(InvalidAllocationEntry, match="trust_level"):
        _append(tmp_path / "r.jsonl", trust_level="guessed")


def test_invalid_enum_does_not_create_file(tmp_path):
    p = tmp_path / "r.jsonl"
    with pytest.raises(InvalidAllocationEntry):
        _append(p, source_of_record="bad")
    assert not p.exists()


def test_known_sources_count():
    assert len(KNOWN_SOURCES) == 7


def test_known_resource_types_count():
    assert len(KNOWN_RESOURCE_TYPES) == 2


def test_known_trust_levels_count():
    assert len(KNOWN_TRUST_LEVELS) == 3


# -- read_entries -------------------------------------------------------------

def test_read_absent_returns_empty(tmp_path):
    assert read_entries(tmp_path / "r.jsonl") == []


def test_read_order_preserved(tmp_path):
    p = tmp_path / "r.jsonl"
    _chain(p, n=3)
    assert len(read_entries(p)) == 3


def test_read_truncated_last_line_raises(tmp_path):
    p = tmp_path / "r.jsonl"
    _append(p)
    with open(p, "a") as f:
        f.write('{"partial":')
    with pytest.raises(AllocationTruncatedError):
        read_entries(p)


def test_read_corrupted_non_final_raises(tmp_path):
    p = tmp_path / "r.jsonl"
    _chain(p, n=2)
    lines = p.read_text().splitlines()
    lines[0] = '{"broken'
    p.write_text("\n".join(lines) + "\n")
    with pytest.raises(AllocationChainError):
        read_entries(p)


# -- verify_chain: AC 2 and AC 4 ---------------------------------------------

def test_verify_empty_passes():
    verify_chain([])


def test_verify_single_entry_passes(tmp_path):
    p = tmp_path / "r.jsonl"
    _append(p)
    verify_chain(read_entries(p))


def test_verify_multi_entry_passes(tmp_path):
    p = tmp_path / "r.jsonl"
    _chain(p, n=5)
    verify_chain(read_entries(p))


def test_tamper_prior_entry_breaks_chain(tmp_path):
    """AC 4: mutating a prior entry breaks chain verify."""
    p = tmp_path / "r.jsonl"
    _chain(p, n=3)
    entries = read_entries(p)
    entries[0] = {**entries[0], "resource_id": "tampered"}
    with pytest.raises(AllocationChainError):
        verify_chain(entries)


def test_tamper_middle_entry_breaks_chain(tmp_path):
    """AC 4: mutating the middle entry is detected."""
    p = tmp_path / "r.jsonl"
    _chain(p, n=3)
    entries = read_entries(p)
    entries[1] = {**entries[1], "correlation_key": "tampered"}
    with pytest.raises(AllocationChainError):
        verify_chain(entries)


def test_tamper_tail_entry_breaks_chain(tmp_path):
    """Tail is self-authenticating -- tampering it is detected without a successor."""
    p = tmp_path / "r.jsonl"
    _chain(p, n=3)
    entries = read_entries(p)
    entries[-1] = {**entries[-1], "resource_id": "tampered-tail"}
    with pytest.raises(AllocationChainError):
        verify_chain(entries)


def test_tamper_prev_sha_breaks_chain(tmp_path):
    p = tmp_path / "r.jsonl"
    _chain(p, n=2)
    entries = read_entries(p)
    entries[1] = {**entries[1], "prev_entry_sha256": "a" * 64}
    with pytest.raises(AllocationChainError):
        verify_chain(entries)


def test_first_entry_non_sentinel_prev_breaks_chain(tmp_path):
    p = tmp_path / "r.jsonl"
    _append(p)
    entries = read_entries(p)
    entries[0] = {**entries[0], "prev_entry_sha256": "a" * 64}
    with pytest.raises(AllocationChainError):
        verify_chain(entries)


# -- AC 3: atomic write via rig/atomic.py ------------------------------------

def test_atomic_append_line_is_called(tmp_path, monkeypatch):
    import rig.allocation as _mod
    calls: list = []
    original = _mod.atomic_append_line

    def spy(path, line):
        calls.append(path)
        return original(path, line)

    monkeypatch.setattr(_mod, "atomic_append_line", spy)
    p = tmp_path / "r.jsonl"
    _append(p)
    assert len(calls) == 1
    assert calls[0] == str(p)


# -- AC 1: schema validation --------------------------------------------------

def test_written_entries_pass_schema(tmp_path):
    """AC 1: every generated entry validates against allocation_entry.schema.json."""
    p = tmp_path / "r.jsonl"
    sha = _append(p)
    sha = _append(p, prev=sha,
                  source_of_record=SOURCE_WAL_EVENT,
                  trust_level=TRUST_OBSERVED,
                  wal_event_ref="evt-uuid-1")
    sha = _append(p, prev=sha,
                  source_of_record=SOURCE_SWEEP_PRE,
                  trust_level=TRUST_INFERRED,
                  wal_event_ref=None)
    for entry in read_entries(p):
        validate("allocation_entry", entry)


def test_schema_all_sources_valid():
    source_trust_ref = [
        (SOURCE_BOTO3_LAUNCH_RESPONSE,   TRUST_AUTHORITATIVE, None),
        (SOURCE_HEAD_OBJECT_INDEPENDENT, TRUST_AUTHORITATIVE, None),
        (SOURCE_WAL_EVENT,               TRUST_OBSERVED,      "evt-1"),
        (SOURCE_SIDECAR_OBSERVED,        TRUST_OBSERVED,      "evt-2"),
        (SOURCE_SWEEP_PRE,               TRUST_INFERRED,      None),
        (SOURCE_SWEEP_POST,              TRUST_INFERRED,      None),
        (SOURCE_SWEEP_DELAYED,           TRUST_INFERRED,      None),
    ]
    for src, trust, ref in source_trust_ref:
        validate("allocation_entry", _schema_entry(
            source_of_record=src, trust_level=trust, wal_event_ref=ref
        ))


def test_schema_rejects_unknown_source():
    with pytest.raises(SchemaValidationError):
        validate("allocation_entry", _schema_entry(source_of_record="made_up"))


def test_schema_rejects_unknown_resource_type():
    with pytest.raises(SchemaValidationError):
        validate("allocation_entry", _schema_entry(resource_type="rds_cluster"))


def test_schema_rejects_unknown_trust_level():
    with pytest.raises(SchemaValidationError):
        validate("allocation_entry", _schema_entry(trust_level="maybe"))


def test_schema_rejects_extra_field():
    with pytest.raises(SchemaValidationError):
        validate("allocation_entry", {**_schema_entry(), "extra": "bad"})


def test_schema_rejects_missing_field():
    bad = _schema_entry()
    del bad["resource_id"]
    with pytest.raises(SchemaValidationError):
        validate("allocation_entry", bad)


def test_schema_ec2_and_s3_resource_types_valid():
    for rt in (RESOURCE_EC2_INSTANCE, RESOURCE_S3_OBJECT_VERSION):
        validate("allocation_entry", _schema_entry(resource_type=rt))


def test_schema_null_originating_run_id_valid():
    validate("allocation_entry", _schema_entry(originating_run_id=None))


def test_schema_null_wal_event_ref_valid():
    validate("allocation_entry", _schema_entry(wal_event_ref=None))


def _schema_entry(**overrides) -> dict:
    base = {
        "schema_version":           1,
        "observation_ts_monotonic": 1_000_000,
        "source_of_record":         SOURCE_BOTO3_LAUNCH_RESPONSE,
        "resource_type":            RESOURCE_EC2_INSTANCE,
        "resource_id":              "i-0abc123def456",
        "correlation_key":          "run-001/attempt-1",
        "originating_run_id":       "run-001",
        "wal_event_ref":            None,
        "trust_level":              TRUST_AUTHORITATIVE,
        "prev_entry_sha256":        "0" * 64,
        "entry_sha256":             "a" * 64,
    }
    base.update(overrides)
    return base


# -- integration --------------------------------------------------------------

def test_full_round_trip(tmp_path):
    p = tmp_path / "allocation_registry.jsonl"

    sha = append_entry(
        p, _ts(), SOURCE_BOTO3_LAUNCH_RESPONSE, RESOURCE_EC2_INSTANCE,
        "i-0abc123def456", "run-001/attempt-1", "run-001", None,
        TRUST_AUTHORITATIVE, FIRST_ENTRY_PREV_SHA256,
    )
    sha = append_entry(
        p, _ts(), SOURCE_WAL_EVENT, RESOURCE_S3_OBJECT_VERSION,
        "my-bucket/runs/r1/input.zip@ver123", "run-001/attempt-1", "run-001",
        "wal-evt-uuid", TRUST_OBSERVED, sha,
    )
    sha = append_entry(
        p, _ts(), SOURCE_SWEEP_POST, RESOURCE_EC2_INSTANCE,
        "i-0abc123def456", "run-001/sweep", None, None,
        TRUST_INFERRED, sha,
    )

    entries = read_entries(p)
    assert len(entries) == 3
    verify_chain(entries)
    for entry in entries:
        validate("allocation_entry", entry)


def test_all_shas_distinct(tmp_path):
    p = tmp_path / "r.jsonl"
    shas = _chain(p, n=5)
    assert len(set(shas)) == 5