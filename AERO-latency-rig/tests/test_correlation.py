"""
Tests for T3.4 -- Sweep correlation (rig/sweeper.py).

Confirmed spec rules:
  - Known set = registry entries with trust_level authoritative or observed only.
    Inferred entries (sweep sources) do NOT count.
  - pre_existing: in inventory_pre but not in known set (not an orphan).
  - orphan: appears in post/delayed, NOT in pre, NOT in known set.
  - anomaly: in known set but unexpected cloud state. Reported only -- NOT gate-failing.
    (ADR-005: anomalies are "reported, never auto-acted on".)
  - vanished: in known set but absent from all inventory snapshots. Not an error.
  - delete_markers_observed: recorded, excluded from all classification.
  - No gate_failed field -- gate verdicts belong to evaluator T7.3.

AC goldens:
  1. Orphan EC2 → orphan_vm_known.
  2. Orphan S3 version → orphan_s3_recorded_current_version.
  3. Stopped EC2 in known set → cloud_state_anomaly, reported only (ADR-005).
"""

import json
import time
from pathlib import Path

import pytest

from rig.allocation import (
    FIRST_ENTRY_PREV_SHA256,
    RESOURCE_EC2_INSTANCE,
    RESOURCE_S3_OBJECT_VERSION,
    SOURCE_BOTO3_LAUNCH_RESPONSE,
    SOURCE_SWEEP_PRE,
    TRUST_AUTHORITATIVE,
    TRUST_INFERRED,
    append_entry,
    read_entries,
)
from rig.schemas import SchemaValidationError, validate
from rig.sweeper import (
    CLOUD_STATE_ANOMALY,
    ORPHAN_S3_RECORDED_CURRENT_VERSION,
    ORPHAN_VM_KNOWN,
    _s3_composite_id,
    correlate,
    write_correlation,
)


# -- builders -----------------------------------------------------------------

def _ts() -> int:
    return time.monotonic_ns()


def _empty_snap(phase: str = "inventory_pre", bucket: str = "my-bucket") -> dict:
    return {
        "schema_version": 1, "phase": phase,
        "captured_at_mono_ns": 0, "captured_at_wall": "2025-06-01T12:00:00Z",
        "s3_bucket": bucket,
        "ec2_instances": [], "s3_object_versions": [],
        "ec2_request_ids": [], "s3_request_ids": [],
    }


def _with_ec2(snap: dict, *instances) -> dict:
    snap = dict(snap)
    snap["ec2_instances"] = [
        {"instance_id": iid, "state": st, "instance_type": "t3.micro",
         "launch_time": "", "tags": []}
        for iid, st in instances
    ]
    return snap


def _with_s3(snap: dict, *versions) -> dict:
    snap = dict(snap)
    snap["s3_object_versions"] = []
    for v in versions:
        key, vid = v[0], v[1]
        dm = v[2] if len(v) > 2 else False
        snap["s3_object_versions"].append({
            "key": key, "version_id": vid, "size": 0,
            "last_modified": "", "is_delete_marker": dm,
        })
    return snap


def _reg(tmp_path: Path, *resources) -> list[dict]:
    p = tmp_path / "reg.jsonl"
    prev = FIRST_ENTRY_PREV_SHA256
    for rt, rid, trust in resources:
        src = SOURCE_BOTO3_LAUNCH_RESPONSE if trust == TRUST_AUTHORITATIVE else SOURCE_SWEEP_PRE
        prev = append_entry(
            p, _ts(), src, rt, rid,
            "run-001/attempt-1", "run-001", None, trust, prev,
        )
    return read_entries(p)


def _corr(entries, pre=None, post=None, delayed=None):
    pre     = pre     or _empty_snap("inventory_pre")
    post    = post    or _empty_snap("inventory_post")
    delayed = delayed or _empty_snap("inventory_delayed")
    return correlate(entries, pre, post, delayed)


# -- AC 1: orphan EC2 ---------------------------------------------------------

def test_orphan_ec2_detected(tmp_path):
    """AC 1: EC2 in post, not in known set, not in pre → orphan_vm_known."""
    entries = _reg(tmp_path, (RESOURCE_EC2_INSTANCE, "i-known", TRUST_AUTHORITATIVE))
    post = _with_ec2(_empty_snap("inventory_post"), ("i-known", "running"), ("i-orphan", "running"))
    result = _corr(entries, post=post)
    orphan_ids = [o["resource_id"] for o in result["orphans"]]
    assert "i-orphan" in orphan_ids
    assert "i-known" not in orphan_ids


def test_orphan_ec2_category(tmp_path):
    entries = _reg(tmp_path)
    post = _with_ec2(_empty_snap("inventory_post"), ("i-orphan", "running"))
    result = _corr(entries, post=post)
    assert result["orphans"][0]["orphan_category"] == ORPHAN_VM_KNOWN
    assert result["orphans"][0]["resource_type"] == "ec2_instance"


def test_orphan_ec2_in_orphans_not_correlated(tmp_path):
    entries = _reg(tmp_path)
    post = _with_ec2(_empty_snap("inventory_post"), ("i-orphan", "running"))
    result = _corr(entries, post=post)
    assert len(result["orphans"]) == 1
    assert len(result["correlated"]) == 0


# -- AC 2: orphan S3 version --------------------------------------------------

def test_orphan_s3_detected(tmp_path):
    """AC 2: S3 version in post, not in known set, not in pre → orphan_s3."""
    cid = _s3_composite_id("my-bucket", "k", "v-known")
    entries = _reg(tmp_path, (RESOURCE_S3_OBJECT_VERSION, cid, TRUST_AUTHORITATIVE))
    post = _with_s3(_empty_snap("inventory_post"), ("k", "v-known"), ("k-orphan", "v-orphan"))
    result = _corr(entries, post=post)
    orphan_ids = [o["resource_id"] for o in result["orphans"]]
    assert _s3_composite_id("my-bucket", "k-orphan", "v-orphan") in orphan_ids


def test_orphan_s3_category(tmp_path):
    entries = _reg(tmp_path)
    post = _with_s3(_empty_snap("inventory_post"), ("k", "v1"))
    result = _corr(entries, post=post)
    assert result["orphans"][0]["orphan_category"] == ORPHAN_S3_RECORDED_CURRENT_VERSION


def test_orphan_s3_in_orphans_not_correlated(tmp_path):
    entries = _reg(tmp_path)
    post = _with_s3(_empty_snap("inventory_post"), ("k", "v1"))
    result = _corr(entries, post=post)
    assert len(result["orphans"]) == 1
    assert len(result["correlated"]) == 0


# -- AC 3: stopped EC2 anomaly (reported only, not gate-failing) --------------

def test_stopped_ec2_in_known_set_is_anomaly(tmp_path):
    """AC 3: stopped EC2 in known set → cloud_state_anomaly, not orphan."""
    entries = _reg(tmp_path, (RESOURCE_EC2_INSTANCE, "i-stopped", TRUST_AUTHORITATIVE))
    post = _with_ec2(_empty_snap("inventory_post"), ("i-stopped", "stopped"))
    result = _corr(entries, post=post)
    assert len(result["orphans"]) == 0
    assert result["anomalies"][0]["anomaly_type"] == CLOUD_STATE_ANOMALY
    assert result["anomalies"][0]["resource_id"] == "i-stopped"


def test_anomaly_does_not_produce_gate_failed_field(tmp_path):
    """ADR-005: cloud_state_anomaly is 'reported, never auto-acted on'.
    The correlator does not emit a gate verdict -- that belongs to T7.3.
    """
    entries = _reg(tmp_path, (RESOURCE_EC2_INSTANCE, "i-stopped", TRUST_AUTHORITATIVE))
    post = _with_ec2(_empty_snap("inventory_post"), ("i-stopped", "stopped"))
    result = _corr(entries, post=post)
    assert "gate_failed" not in result


def test_anomaly_detail_contains_state(tmp_path):
    entries = _reg(tmp_path, (RESOURCE_EC2_INSTANCE, "i-term", TRUST_AUTHORITATIVE))
    post = _with_ec2(_empty_snap("inventory_post"), ("i-term", "terminated"))
    result = _corr(entries, post=post)
    assert "terminated" in result["anomalies"][0]["detail"]


def test_running_ec2_is_not_anomaly(tmp_path):
    entries = _reg(tmp_path, (RESOURCE_EC2_INSTANCE, "i-run", TRUST_AUTHORITATIVE))
    post = _with_ec2(_empty_snap("inventory_post"), ("i-run", "running"))
    result = _corr(entries, post=post)
    assert len(result["anomalies"]) == 0


def test_correlated_resource_still_in_correlated_even_if_anomalous(tmp_path):
    """Stopped VM is correlated (in known set) AND in anomalies[]."""
    entries = _reg(tmp_path, (RESOURCE_EC2_INSTANCE, "i-stopped", TRUST_AUTHORITATIVE))
    post = _with_ec2(_empty_snap("inventory_post"), ("i-stopped", "stopped"))
    result = _corr(entries, post=post)
    corr_ids = [c["resource_id"] for c in result["correlated"]]
    assert "i-stopped" in corr_ids


# -- no gate_failed field at all ----------------------------------------------

def test_correlation_output_has_no_gate_failed_field(tmp_path):
    """The correlator never emits gate_failed -- that is T7.3's job."""
    entries = _reg(tmp_path)
    post = _with_ec2(_empty_snap("inventory_post"), ("i-orphan", "running"))
    result = _corr(entries, post=post)
    assert "gate_failed" not in result


def test_clean_session_has_no_gate_failed_field(tmp_path):
    entries = _reg(tmp_path, (RESOURCE_EC2_INSTANCE, "i-x", TRUST_AUTHORITATIVE))
    post = _with_ec2(_empty_snap("inventory_post"), ("i-x", "running"))
    result = _corr(entries, post=post)
    assert "gate_failed" not in result


# -- inferred entries do NOT count as known -----------------------------------

def test_inferred_entry_does_not_satisfy_correlation(tmp_path):
    entries = _reg(tmp_path, (RESOURCE_EC2_INSTANCE, "i-inferred", TRUST_INFERRED))
    post = _with_ec2(_empty_snap("inventory_post"), ("i-inferred", "running"))
    result = _corr(entries, post=post)
    orphan_ids = [o["resource_id"] for o in result["orphans"]]
    assert "i-inferred" in orphan_ids


def test_only_authoritative_and_observed_count(tmp_path):
    entries = _reg(
        tmp_path,
        (RESOURCE_EC2_INSTANCE, "i-auth", TRUST_AUTHORITATIVE),
        (RESOURCE_EC2_INSTANCE, "i-infer", TRUST_INFERRED),
    )
    post = _with_ec2(_empty_snap("inventory_post"),
                     ("i-auth", "running"), ("i-infer", "running"))
    result = _corr(entries, post=post)
    corr_ids  = [c["resource_id"] for c in result["correlated"]]
    orphan_ids = [o["resource_id"] for o in result["orphans"]]
    assert "i-auth" in corr_ids
    assert "i-infer" in orphan_ids


# -- pre_existing: in pre but not in known set --------------------------------

def test_pre_existing_ec2_is_not_orphan(tmp_path):
    """Resource in inventory_pre but not in known set → pre_existing, not orphan."""
    entries = _reg(tmp_path)
    pre = _with_ec2(_empty_snap("inventory_pre"), ("i-pre", "running"))
    result = _corr(entries, pre=pre)
    pre_ids   = [p["resource_id"] for p in result["pre_existing"]]
    orphan_ids = [o["resource_id"] for o in result["orphans"]]
    assert "i-pre" in pre_ids
    assert "i-pre" not in orphan_ids


def test_pre_existing_not_in_orphans_or_correlated(tmp_path):
    entries = _reg(tmp_path)
    pre = _with_ec2(_empty_snap("inventory_pre"), ("i-pre", "running"))
    result = _corr(entries, pre=pre)
    corr_ids  = [c["resource_id"] for c in result["correlated"]]
    orphan_ids = [o["resource_id"] for o in result["orphans"]]
    assert "i-pre" not in corr_ids
    assert "i-pre" not in orphan_ids


def test_resource_in_pre_and_post_not_known_is_pre_existing(tmp_path):
    entries = _reg(tmp_path)
    pre  = _with_ec2(_empty_snap("inventory_pre"),  ("i-old", "running"))
    post = _with_ec2(_empty_snap("inventory_post"), ("i-old", "running"))
    result = _corr(entries, pre=pre, post=post)
    pre_ids   = [p["resource_id"] for p in result["pre_existing"]]
    orphan_ids = [o["resource_id"] for o in result["orphans"]]
    assert "i-old" in pre_ids
    assert "i-old" not in orphan_ids


# -- vanished: in known set but absent from all snapshots ---------------------

def test_vanished_resource_recorded(tmp_path):
    entries = _reg(tmp_path, (RESOURCE_EC2_INSTANCE, "i-gone", TRUST_AUTHORITATIVE))
    result = _corr(entries)
    vanished_ids = [v["resource_id"] for v in result["vanished"]]
    assert "i-gone" in vanished_ids


def test_vanished_not_in_orphans(tmp_path):
    entries = _reg(tmp_path, (RESOURCE_EC2_INSTANCE, "i-gone", TRUST_AUTHORITATIVE))
    result = _corr(entries)
    assert len(result["orphans"]) == 0


def test_vanished_inferred_not_reported(tmp_path):
    entries = _reg(tmp_path, (RESOURCE_EC2_INSTANCE, "i-infer", TRUST_INFERRED))
    result = _corr(entries)
    vanished_ids = [v["resource_id"] for v in result["vanished"]]
    assert "i-infer" not in vanished_ids


# -- delete markers -----------------------------------------------------------

def test_delete_markers_not_orphaned(tmp_path):
    entries = _reg(tmp_path)
    post = _with_s3(_empty_snap("inventory_post"), ("k", "dm-ver", True))
    result = _corr(entries, post=post)
    assert len(result["orphans"]) == 0


def test_delete_markers_recorded_in_output(tmp_path):
    entries = _reg(tmp_path)
    post = _with_s3(_empty_snap("inventory_post"), ("k", "dm-ver", True))
    result = _corr(entries, post=post)
    dm_keys = [dm["key"] for dm in result["delete_markers_observed"]]
    assert "k" in dm_keys


def test_delete_markers_not_in_correlated(tmp_path):
    cid = _s3_composite_id("my-bucket", "k", "dm-ver")
    entries = _reg(tmp_path, (RESOURCE_S3_OBJECT_VERSION, cid, TRUST_AUTHORITATIVE))
    post = _with_s3(_empty_snap("inventory_post"), ("k", "dm-ver", True))
    result = _corr(entries, post=post)
    corr_ids = [c["resource_id"] for c in result["correlated"]]
    assert cid not in corr_ids


# -- clean session -----------------------------------------------------------

def test_clean_session(tmp_path):
    iid = "i-clean"
    cid = _s3_composite_id("my-bucket", "k", "v1")
    entries = _reg(
        tmp_path,
        (RESOURCE_EC2_INSTANCE, iid, TRUST_AUTHORITATIVE),
        (RESOURCE_S3_OBJECT_VERSION, cid, TRUST_AUTHORITATIVE),
    )
    post = _with_ec2(_with_s3(_empty_snap("inventory_post"), ("k", "v1")), (iid, "running"))
    result = _corr(entries, post=post)
    assert len(result["orphans"]) == 0
    assert len(result["correlated"]) == 2
    assert "gate_failed" not in result


def test_empty_inputs_clean():
    result = _corr([])
    assert result == {
        "schema_version": 1,
        "correlated": [], "pre_existing": [], "orphans": [],
        "anomalies": [], "vanished": [], "delete_markers_observed": [],
        "inputs": {"registry_sha256": "", "pre_sha256": "",
                   "post_sha256": "", "delayed_sha256": ""},
    }


# -- schema -------------------------------------------------------------------

def test_result_validates_against_schema(tmp_path):
    entries = _reg(tmp_path, (RESOURCE_EC2_INSTANCE, "i-x", TRUST_AUTHORITATIVE))
    post = _with_ec2(_empty_snap("inventory_post"), ("i-x", "running"), ("i-orp", "running"))
    result = _corr(entries, post=post)
    validate("sweep_correlation", result)


def test_schema_rejects_gate_failed_field():
    """gate_failed must not appear in the schema -- it belongs to T7.3."""
    with pytest.raises(SchemaValidationError):
        validate("sweep_correlation", {
            "schema_version": 1,
            "correlated": [], "pre_existing": [], "orphans": [],
            "anomalies": [], "vanished": [], "delete_markers_observed": [],
            "inputs": {"registry_sha256": "", "pre_sha256": "",
                       "post_sha256": "", "delayed_sha256": ""},
            "gate_failed": False,
        })


def test_schema_rejects_unknown_orphan_category():
    with pytest.raises(SchemaValidationError):
        validate("sweep_correlation", {
            "schema_version": 1,
            "correlated": [], "pre_existing": [],
            "orphans": [{"resource_id": "x", "resource_type": "ec2_instance",
                         "orphan_category": "made_up"}],
            "anomalies": [], "vanished": [], "delete_markers_observed": [],
            "inputs": {"registry_sha256": "", "pre_sha256": "",
                       "post_sha256": "", "delayed_sha256": ""},
        })


def test_write_correlation_creates_valid_file(tmp_path):
    result = _corr([])
    out = tmp_path / "correlation.json"
    write_correlation(result, out)
    assert out.exists()
    validate("sweep_correlation", json.loads(out.read_text()))


def test_write_uses_atomic_write(tmp_path, monkeypatch):
    import rig.sweeper as _mod
    calls: list[str] = []
    original = _mod.atomic_write

    def spy(path, data):
        calls.append(path)
        return original(path, data)

    monkeypatch.setattr(_mod, "atomic_write", spy)
    out = tmp_path / "correlation.json"
    write_correlation(_corr([]), out)
    assert str(out) in calls