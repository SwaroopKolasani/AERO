"""
Tests for rig/sweeper.py -- T3.2 (second revision).

AC coverage:
  1. Raw and normalized are "written together" per the completion marker rule:
     the pair is committed iff <phase>.complete exists.
  2. Raw artifact contains full ResponseMetadata per boto3 call.
  3. Delayed sweep cooldown: sleep on normal path; skip_sleep=True for T7.6.
     Note: full end-to-end crash-recovery orchestration is T7.6's responsibility.
     T3.2 exposes the skip_sleep interface hook that T7.6 will use.
  4. Both schemas validate written artifacts.
"""

import json
import time
from datetime import datetime, timezone
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from rig.schemas import SchemaValidationError, validate
from rig.sweeper import (
    DEFAULT_COOLDOWN_S,
    KNOWN_PHASES,
    PHASE_DELAYED,
    PHASE_POST,
    PHASE_PRE,
    _normalize_ec2,
    _normalize_s3,
    _raw_bytes,
    _request_ids,
    phase_is_complete,
    take_inventory,
)


# -- fake boto3 responses -----------------------------------------------------

def _ec2_page(instance_id: str = "i-0abc123def456") -> dict:
    return {
        "ResponseMetadata": {"RequestId": "ec2-req-001", "HTTPStatusCode": 200},
        "Reservations": [{
            "Instances": [{
                "InstanceId":   instance_id,
                "State":        {"Name": "running", "Code": 16},
                "InstanceType": "t3.micro",
                "LaunchTime":   datetime(2025, 6, 1, 12, 0, 0, tzinfo=timezone.utc),
                "Tags":         [{"Key": "Name", "Value": "test-vm"}],
            }],
        }],
    }


def _s3_page(key: str = "sessions/s1/input.zip", version_id: str = "ver001") -> dict:
    return {
        "ResponseMetadata": {"RequestId": "s3-req-001", "HTTPStatusCode": 200},
        "Versions": [{
            "Key":          key,
            "VersionId":    version_id,
            "Size":         4096,
            "LastModified": datetime(2025, 6, 1, 11, 0, 0, tzinfo=timezone.utc),
        }],
        "DeleteMarkers": [],
    }


def _empty_ec2_page() -> dict:
    return {"ResponseMetadata": {"RequestId": "ec2-empty", "HTTPStatusCode": 200},
            "Reservations": []}


def _empty_s3_page() -> dict:
    return {"ResponseMetadata": {"RequestId": "s3-empty", "HTTPStatusCode": 200},
            "Versions": [], "DeleteMarkers": []}


def _make_clients(ec2_pages=None, s3_pages=None):
    if ec2_pages is None:
        ec2_pages = [_ec2_page()]
    if s3_pages is None:
        s3_pages  = [_s3_page()]

    def _pager(pages):
        m = MagicMock()
        m.paginate.return_value = iter(pages)
        return m

    ec2 = MagicMock()
    ec2.get_paginator.return_value = _pager(ec2_pages)
    s3  = MagicMock()
    s3.get_paginator.return_value  = _pager(s3_pages)
    return ec2, s3


# -- AC 1: completion marker is the "written together" guarantee ---------------

def test_successful_phase_writes_completion_marker(tmp_path):
    """phase_is_complete returns True only after all three writes succeed."""
    ec2, s3 = _make_clients()
    take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    assert phase_is_complete(tmp_path, PHASE_PRE)


def test_phase_is_complete_false_before_run(tmp_path):
    assert not phase_is_complete(tmp_path, PHASE_PRE)


def test_all_three_artifacts_present_after_success(tmp_path):
    ec2, s3 = _make_clients()
    take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    assert (tmp_path / "inventory_pre_raw.jsonl").exists()
    assert (tmp_path / "inventory_pre.json").exists()
    assert (tmp_path / "inventory_pre.complete").exists()


def test_pagination_error_leaves_no_files_and_no_marker(tmp_path):
    """A boto3 error during pagination leaves the output dir clean."""
    ec2 = MagicMock()
    ec2.get_paginator.side_effect = RuntimeError("simulated boto3 error")
    s3 = MagicMock()
    with pytest.raises(RuntimeError):
        take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    assert not (tmp_path / "inventory_pre_raw.jsonl").exists()
    assert not (tmp_path / "inventory_pre.json").exists()
    assert not phase_is_complete(tmp_path, PHASE_PRE)


def test_marker_written_last_via_atomic_write(tmp_path, monkeypatch):
    """Completion marker must be the last atomic_write call."""
    import rig.sweeper as _mod
    written_order: list[str] = []
    original = _mod.atomic_write

    def spy(path, data):
        written_order.append(Path(path).name)
        return original(path, data)

    monkeypatch.setattr(_mod, "atomic_write", spy)
    ec2, s3 = _make_clients()
    take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    # Marker must be the last file written.
    assert written_order[-1] == "inventory_pre.complete"


def test_raw_written_before_normalized(tmp_path, monkeypatch):
    """Raw artifact must be written before normalized."""
    import rig.sweeper as _mod
    written_order: list[str] = []
    original = _mod.atomic_write

    def spy(path, data):
        written_order.append(Path(path).name)
        return original(path, data)

    monkeypatch.setattr(_mod, "atomic_write", spy)
    ec2, s3 = _make_clients()
    take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    raw_idx    = written_order.index("inventory_pre_raw.jsonl")
    norm_idx   = written_order.index("inventory_pre.json")
    marker_idx = written_order.index("inventory_pre.complete")
    assert raw_idx < norm_idx < marker_idx


def test_all_phases_write_marker(tmp_path):
    for phase in (PHASE_PRE, PHASE_POST):
        out = tmp_path / phase
        out.mkdir()
        ec2, s3 = _make_clients()
        take_inventory(phase, out, ec2, s3, "my-bucket")
        assert phase_is_complete(out, phase)


# -- AC 2: raw artifact contains full ResponseMetadata per call ---------------

def test_raw_each_line_has_response_metadata(tmp_path):
    ec2, s3 = _make_clients()
    take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    lines = [l for l in (tmp_path / "inventory_pre_raw.jsonl").read_text().splitlines() if l.strip()]
    assert len(lines) >= 2
    for line in lines:
        assert "ResponseMetadata" in json.loads(line)


def test_raw_response_metadata_has_request_id(tmp_path):
    ec2, s3 = _make_clients()
    take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    for line in (tmp_path / "inventory_pre_raw.jsonl").read_text().splitlines():
        if line.strip():
            assert "RequestId" in json.loads(line)["ResponseMetadata"]


def test_raw_validates_against_wrapper_schema(tmp_path):
    ec2, s3 = _make_clients()
    take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    for line in (tmp_path / "inventory_pre_raw.jsonl").read_text().splitlines():
        if line.strip():
            validate("inventory_snapshot_raw", json.loads(line))


def test_raw_has_one_line_per_page(tmp_path):
    """Two EC2 pages + one S3 page = three lines."""
    ec2, s3 = _make_clients([_ec2_page("i-001"), _ec2_page("i-002")], [_s3_page()])
    take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    lines = [l for l in (tmp_path / "inventory_pre_raw.jsonl").read_text().splitlines() if l.strip()]
    assert len(lines) == 3


def test_raw_is_single_atomic_write(tmp_path, monkeypatch):
    """Raw JSONL is written in one atomic_write call, not page-by-page."""
    import rig.sweeper as _mod
    raw_calls: list = []
    original = _mod.atomic_write

    def spy(path, data):
        if "raw.jsonl" in path:
            raw_calls.append(path)
        return original(path, data)

    monkeypatch.setattr(_mod, "atomic_write", spy)
    ec2, s3 = _make_clients([_ec2_page("i-001"), _ec2_page("i-002")], [_s3_page()])
    take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    assert len(raw_calls) == 1


# -- AC 3: delayed sweep cooldown and T7.6 interface hook ---------------------

def test_delayed_sweep_sleeps(tmp_path):
    ec2, s3 = _make_clients()
    with patch("time.sleep") as mock_sleep:
        take_inventory(PHASE_DELAYED, tmp_path, ec2, s3, "my-bucket", cooldown_s=300)
    mock_sleep.assert_called_once_with(300)


def test_pre_does_not_sleep(tmp_path):
    ec2, s3 = _make_clients()
    with patch("time.sleep") as mock_sleep:
        take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    mock_sleep.assert_not_called()


def test_post_does_not_sleep(tmp_path):
    ec2, s3 = _make_clients()
    with patch("time.sleep") as mock_sleep:
        take_inventory(PHASE_POST, tmp_path, ec2, s3, "my-bucket")
    mock_sleep.assert_not_called()


def test_default_cooldown_is_300():
    assert DEFAULT_COOLDOWN_S == 300


def test_skip_sleep_skips_cooldown_for_t76(tmp_path):
    """T7.6 interface hook: skip_sleep=True lets crash-recovery skip the sleep.

    This is the interface that T7.6 (crash-recovery adopter) will use to
    invoke the delayed sweep without re-sleeping.  Full crash-recovery
    orchestration is T7.6's responsibility; T3.2 exposes the hook.
    """
    ec2, s3 = _make_clients()
    with patch("time.sleep") as mock_sleep:
        take_inventory(PHASE_DELAYED, tmp_path, ec2, s3, "my-bucket",
                       cooldown_s=300, skip_sleep=True)
    mock_sleep.assert_not_called()


def test_skip_sleep_still_writes_all_three_artifacts(tmp_path):
    ec2, s3 = _make_clients()
    take_inventory(PHASE_DELAYED, tmp_path, ec2, s3, "my-bucket",
                   cooldown_s=0, skip_sleep=True)
    assert (tmp_path / "inventory_delayed_raw.jsonl").exists()
    assert (tmp_path / "inventory_delayed.json").exists()
    assert phase_is_complete(tmp_path, PHASE_DELAYED)


# -- AC 4: schema validation --------------------------------------------------

def test_normalized_validates(tmp_path):
    ec2, s3 = _make_clients()
    doc = take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    validate("inventory_snapshot", doc)


def test_normalized_on_disk_validates(tmp_path):
    ec2, s3 = _make_clients()
    take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    validate("inventory_snapshot", json.loads((tmp_path / "inventory_pre.json").read_text()))


def test_normalized_ec2_populated(tmp_path):
    ec2, s3 = _make_clients()
    doc = take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    assert doc["ec2_instances"][0]["instance_id"] == "i-0abc123def456"
    assert doc["ec2_instances"][0]["state"] == "running"


def test_normalized_s3_populated(tmp_path):
    ec2, s3 = _make_clients()
    doc = take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    assert doc["s3_object_versions"][0]["is_delete_marker"] is False


def test_normalized_delete_marker_flagged(tmp_path):
    dm_page = {
        "ResponseMetadata": {"RequestId": "dm-req", "HTTPStatusCode": 200},
        "Versions": [],
        "DeleteMarkers": [{"Key": "old/obj", "VersionId": "dm-v1",
                           "LastModified": datetime(2025, 1, 1, tzinfo=timezone.utc)}],
    }
    ec2, s3 = _make_clients(s3_pages=[dm_page])
    doc = take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    assert doc["s3_object_versions"][0]["is_delete_marker"] is True


def test_normalized_request_ids(tmp_path):
    ec2, s3 = _make_clients()
    doc = take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    assert "ec2-req-001" in doc["ec2_request_ids"]
    assert "s3-req-001"  in doc["s3_request_ids"]


def test_empty_inventory(tmp_path):
    ec2, s3 = _make_clients([_empty_ec2_page()], [_empty_s3_page()])
    doc = take_inventory(PHASE_PRE, tmp_path, ec2, s3, "my-bucket")
    assert doc["ec2_instances"] == []
    assert doc["s3_object_versions"] == []
    validate("inventory_snapshot", doc)


# -- scope: one bucket only ---------------------------------------------------

def test_s3_scoped_to_named_bucket(tmp_path):
    ec2, s3 = _make_clients()
    take_inventory(PHASE_PRE, tmp_path, ec2, s3, "specific-bucket")
    s3.get_paginator.return_value.paginate.assert_called_once_with(Bucket="specific-bucket")


# -- unknown phase raises ------------------------------------------------------

def test_unknown_phase_raises(tmp_path):
    ec2, s3 = _make_clients()
    with pytest.raises(ValueError, match="unknown inventory phase"):
        take_inventory("inventory_during_registry", tmp_path, ec2, s3, "b")


# -- schema structural tests --------------------------------------------------

def test_inventory_schema_valid():
    validate("inventory_snapshot", {
        "schema_version":      1, "phase": "inventory_pre",
        "captured_at_mono_ns": 0, "captured_at_wall": "2025-06-01T12:00:00Z",
        "s3_bucket": "b", "ec2_instances": [], "s3_object_versions": [],
        "ec2_request_ids": [], "s3_request_ids": [],
    })


def test_inventory_schema_all_phases():
    for phase in KNOWN_PHASES:
        validate("inventory_snapshot", {
            "schema_version":      1, "phase": phase,
            "captured_at_mono_ns": 0, "captured_at_wall": "x",
            "s3_bucket": "b", "ec2_instances": [], "s3_object_versions": [],
            "ec2_request_ids": [], "s3_request_ids": [],
        })


def test_inventory_schema_rejects_extra_field():
    with pytest.raises(SchemaValidationError):
        validate("inventory_snapshot", {
            "schema_version": 1, "phase": "inventory_pre",
            "captured_at_mono_ns": 0, "captured_at_wall": "x",
            "s3_bucket": "b", "ec2_instances": [], "s3_object_versions": [],
            "ec2_request_ids": [], "s3_request_ids": [], "extra": "bad",
        })


def test_inventory_schema_rejects_unknown_phase():
    with pytest.raises(SchemaValidationError):
        validate("inventory_snapshot", {
            "schema_version": 1, "phase": "inventory_during",
            "captured_at_mono_ns": 0, "captured_at_wall": "x",
            "s3_bucket": "b", "ec2_instances": [], "s3_object_versions": [],
            "ec2_request_ids": [], "s3_request_ids": [],
        })


# -- run_delayed_sweep: named public interface for T7.6 -----------------------

from rig.sweeper import run_delayed_sweep


def test_run_delayed_sweep_normal_path_sleeps(tmp_path):
    ec2, s3 = _make_clients()
    with patch("time.sleep") as mock_sleep:
        run_delayed_sweep(tmp_path, ec2, s3, "my-bucket", cooldown_s=300)
    mock_sleep.assert_called_once_with(300)


def test_run_delayed_sweep_crash_recovery_skips_sleep(tmp_path):
    """Revised AC integration test: skip_sleep=True produces valid artifacts."""
    ec2, s3 = _make_clients()
    raw, norm, marker = run_delayed_sweep(
        tmp_path, ec2, s3, "my-bucket", cooldown_s=0, skip_sleep=True
    )
    assert raw.exists()
    assert norm.exists()
    assert marker.exists()
    assert phase_is_complete(tmp_path, PHASE_DELAYED)
    validate("inventory_snapshot", json.loads(norm.read_text()))


def test_run_delayed_sweep_returns_three_paths(tmp_path):
    ec2, s3 = _make_clients()
    with patch("time.sleep"):
        result = run_delayed_sweep(tmp_path, ec2, s3, "my-bucket")
    raw, norm, marker = result
    assert "raw.jsonl" in str(raw)
    assert norm.suffix == ".json"
    assert marker.suffix == ".complete"


def test_run_delayed_sweep_artifacts_pass_schema(tmp_path):
    ec2, s3 = _make_clients()
    raw, norm, marker = run_delayed_sweep(
        tmp_path, ec2, s3, "my-bucket", cooldown_s=0, skip_sleep=True
    )
    for line in raw.read_text().splitlines():
        if line.strip():
            validate("inventory_snapshot_raw", json.loads(line))
    validate("inventory_snapshot", json.loads(norm.read_text()))


# -- fourth phase clarification: allocation_registry IS the "during" phase ---

def test_t32_produces_exactly_three_inventory_phase_names():
    """T3.2 produces three snapshot pairs; the fourth sweep input is T3.1.

    The sweep correlator (T3.4) consumes:
        allocation_registry.jsonl  (T3.1 -- the "during" observation)
        inventory_pre.json         (T3.2)
        inventory_post.json        (T3.2)
        inventory_delayed.json     (T3.2)

    There is no inventory_during_registry artifact in T3.2.
    """
    assert len(KNOWN_PHASES) == 3
    assert PHASE_PRE     in KNOWN_PHASES
    assert PHASE_POST    in KNOWN_PHASES
    assert PHASE_DELAYED in KNOWN_PHASES
    assert "inventory_during_registry" not in KNOWN_PHASES