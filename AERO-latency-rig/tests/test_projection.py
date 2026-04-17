"""
Tests for rig/manifest.py T1.2: projection, write_manifest, reconcile.
And for session_manifest.schema.json and reconciliation.schema.json.

Sections:
  1. project_manifest  -- pure projection
  2. write_manifest    -- sole permitted write path
  3. reconcile         -- all named error codes + clean pass + multi-finding
  4. schemas           -- structural checks for both new schemas
"""

import hashlib
import json
import time
from pathlib import Path

import pytest

from rig.manifest import (
    Finding,
    REC_ARTIFACT_HASH_MISMATCH,
    REC_ARTIFACT_MISSING,
    REC_DISK_ORPHAN,
    REC_EVENT_CHAIN_BROKEN,
    REC_EVENT_LOG_MISSING,
    REC_EVENT_LOG_TRUNCATED,
    REC_MANIFEST_EVENTS_MISMATCH,
    REC_MANIFEST_MISSING,
    ReconciliationReport,
    append_event,
    project_manifest,
    read_events,
    reconcile,
    write_manifest,
)
from rig.schemas import SchemaValidationError, validate


SHA64 = "a" * 64


def _ts():
    return time.monotonic_ns(), "2025-06-01T12:00:00Z"


def _open_payload():
    return {"session_id": "ses-001", "rig_version": "0.1.0",
            "rig_self_sha256": SHA64, "git_commit": "abc1234"}


def _make_session(session_dir: Path) -> tuple[Path, Path]:
    session_dir.mkdir(parents=True, exist_ok=True)
    return (session_dir / "manifest_events.jsonl",
            session_dir / "session_manifest.json")


def _open_session(events_path: Path) -> str:
    ns, wall = _ts()
    return append_event(events_path, "session_opened", _open_payload(), None, ns, wall)


def _sha256_of(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _add_artifact(events_path: Path, prev: str, artifact_file: Path) -> str:
    """Record artifact_created + artifact_verified (ok=True) for a real file."""
    sha = _sha256_of(artifact_file)
    ns, wall = _ts()
    prev = append_event(
        events_path, "artifact_created",
        {"artifact_type": "run_record", "path": str(artifact_file),
         "sha256": sha, "artifact_schema_version": 1},
        prev, ns, wall,
    )
    prev = append_event(
        events_path, "artifact_verified",
        {"artifact_type": "run_record", "path": str(artifact_file),
         "expected_sha256": sha, "observed_sha256": sha, "ok": True},
        prev, ns, wall,
    )
    return prev


# ═══════════════════════════════════════════════════════════════════════════
# 1. project_manifest
# ═══════════════════════════════════════════════════════════════════════════

def test_project_empty_events_returns_defaults():
    proj = project_manifest([])
    assert proj["session_id"] == ""
    assert proj["state"] == "OPEN"
    assert proj["expected_artifacts"] == []
    assert proj["observed_artifacts"] == []
    assert proj["run_count_expected"] == 0
    assert proj["run_count_observed"] == 0
    assert proj["closure_ref"] is None


def test_project_session_opened_sets_identity(tmp_path):
    p = tmp_path / "e.jsonl"
    _open_session(p)
    proj = project_manifest(read_events(p))
    assert proj["session_id"] == "ses-001"
    assert proj["rig_version"] == "0.1.0"
    assert proj["rig_self_sha256"] == SHA64
    assert proj["git_commit"] == "abc1234"
    assert proj["state"] == "OPEN"
    assert proj["opened_at"] != ""


def test_project_state_transition_updates_state(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open_session(p)
    ns, wall = _ts()
    append_event(p, "state_transition", {"from_state": "OPEN", "to_state": "RUNNING"},
                 sha, ns, wall)
    assert project_manifest(read_events(p))["state"] == "RUNNING"


def test_project_artifact_created_populates_expected(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open_session(p)
    ns, wall = _ts()
    append_event(p, "artifact_created",
                 {"artifact_type": "run_record", "path": "/r1",
                  "sha256": SHA64, "artifact_schema_version": 1},
                 sha, ns, wall)
    proj = project_manifest(read_events(p))
    assert len(proj["expected_artifacts"]) == 1
    assert proj["expected_artifacts"][0]["path"] == "/r1"
    assert proj["run_count_expected"] == 1


def test_project_artifact_verified_ok_populates_observed(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open_session(p)
    ns, wall = _ts()
    sha = append_event(p, "artifact_created",
                       {"artifact_type": "run_record", "path": "/r1",
                        "sha256": SHA64, "artifact_schema_version": 1},
                       sha, ns, wall)
    append_event(p, "artifact_verified",
                 {"artifact_type": "run_record", "path": "/r1",
                  "expected_sha256": SHA64, "observed_sha256": SHA64, "ok": True},
                 sha, ns, wall)
    proj = project_manifest(read_events(p))
    assert len(proj["observed_artifacts"]) == 1
    assert proj["observed_artifacts"][0]["path"] == "/r1"
    assert proj["observed_artifacts"][0]["sha256"] == SHA64
    assert proj["observed_artifacts"][0]["schema_version"] == 1
    assert proj["run_count_observed"] == 1


def test_project_artifact_verified_false_not_in_observed(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open_session(p)
    ns, wall = _ts()
    sha = append_event(p, "artifact_created",
                       {"artifact_type": "run_record", "path": "/r1",
                        "sha256": SHA64, "artifact_schema_version": 1},
                       sha, ns, wall)
    append_event(p, "artifact_verified",
                 {"artifact_type": "run_record", "path": "/r1",
                  "expected_sha256": SHA64, "observed_sha256": "b" * 64, "ok": False},
                 sha, ns, wall)
    proj = project_manifest(read_events(p))
    assert proj["observed_artifacts"] == []
    assert proj["run_count_observed"] == 0


def test_project_closure_ref_null_until_session_closed(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open_session(p)
    assert project_manifest(read_events(p))["closure_ref"] is None

    ns, wall = _ts()
    append_event(p, "session_closed",
                 {"verdict": "GO", "decision_report_sha256": SHA64,
                  "closure_sha256": "b" * 64},
                 sha, ns, wall)
    proj = project_manifest(read_events(p))
    assert proj["state"] == "CLOSED"
    assert proj["closure_ref"] == "b" * 64


def test_project_session_invalidated_sets_invalid(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open_session(p)
    ns, wall = _ts()
    append_event(p, "session_invalidated",
                 {"reason_codes": ["thermal_event"],
                  "invalidation_artifact_sha256": SHA64},
                 sha, ns, wall)
    assert project_manifest(read_events(p))["state"] == "INVALID"


def test_project_last_event_id_tracks_latest(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open_session(p)
    ns, wall = _ts()
    append_event(p, "state_transition", {"from_state": "OPEN", "to_state": "RUNNING"},
                 sha, ns, wall)
    events = read_events(p)
    proj = project_manifest(events)
    assert proj["last_event_id"] == events[-1]["event_id"]


def test_project_is_deterministic(tmp_path):
    p = tmp_path / "e.jsonl"
    _open_session(p)
    events = read_events(p)
    assert project_manifest(events) == project_manifest(events)


def test_project_prefix_gives_earlier_state(tmp_path):
    p = tmp_path / "e.jsonl"
    sha = _open_session(p)
    ns, wall = _ts()
    append_event(p, "session_closed",
                 {"verdict": "GO", "decision_report_sha256": SHA64,
                  "closure_sha256": SHA64},
                 sha, ns, wall)
    events = read_events(p)
    assert project_manifest(events[:1])["state"] == "OPEN"
    assert project_manifest(events)["state"] == "CLOSED"


# ═══════════════════════════════════════════════════════════════════════════
# 2. write_manifest
# ═══════════════════════════════════════════════════════════════════════════

def test_write_manifest_creates_file(tmp_path):
    events_path, manifest_path = _make_session(tmp_path)
    _open_session(events_path)
    write_manifest(tmp_path)
    assert manifest_path.exists()


def test_write_manifest_content_matches_projection(tmp_path):
    events_path, manifest_path = _make_session(tmp_path)
    _open_session(events_path)
    write_manifest(tmp_path)
    stored = json.loads(manifest_path.read_text())
    projected = project_manifest(read_events(events_path))
    assert stored == projected


def test_write_manifest_passes_schema(tmp_path):
    events_path, _ = _make_session(tmp_path)
    _open_session(events_path)
    write_manifest(tmp_path)
    stored = json.loads((tmp_path / "session_manifest.json").read_text())
    validate("session_manifest", stored)


def test_write_manifest_reflects_latest_events(tmp_path):
    events_path, _ = _make_session(tmp_path)
    sha = _open_session(events_path)
    write_manifest(tmp_path)
    assert json.loads((tmp_path / "session_manifest.json").read_text())["state"] == "OPEN"

    ns, wall = _ts()
    append_event(events_path, "state_transition",
                 {"from_state": "OPEN", "to_state": "RUNNING"}, sha, ns, wall)
    write_manifest(tmp_path)
    assert json.loads((tmp_path / "session_manifest.json").read_text())["state"] == "RUNNING"


# ═══════════════════════════════════════════════════════════════════════════
# 3. reconcile — named error codes, clean pass, multi-finding
# ═══════════════════════════════════════════════════════════════════════════

def test_reconcile_clean_session(tmp_path):
    """AC: clean session with all artifacts verified → all-green."""
    events_path, _ = _make_session(tmp_path)
    artifact = tmp_path / "artifact.json"
    artifact.write_text('{"x": 1}')

    sha = _open_session(events_path)
    sha = _add_artifact(events_path, sha, artifact)
    write_manifest(tmp_path)

    report = reconcile(tmp_path)
    assert report.ok is True
    assert report.error_code is None
    assert report.findings == []


def test_reconcile_clean_session_no_artifacts(tmp_path):
    events_path, _ = _make_session(tmp_path)
    _open_session(events_path)
    write_manifest(tmp_path)
    report = reconcile(tmp_path)
    assert report.ok is True


def test_reconcile_manifest_missing(tmp_path):
    """AC: manifest_missing when session_manifest.json is absent."""
    events_path, _ = _make_session(tmp_path)
    _open_session(events_path)
    report = reconcile(tmp_path)
    assert report.ok is False
    assert report.error_code == REC_MANIFEST_MISSING
    assert len(report.findings) == 1
    assert report.findings[0].error_code == REC_MANIFEST_MISSING


def test_reconcile_event_log_missing(tmp_path):
    events_path, _ = _make_session(tmp_path)
    _open_session(events_path)
    write_manifest(tmp_path)
    events_path.unlink()
    report = reconcile(tmp_path)
    assert report.ok is False
    assert report.error_code == REC_EVENT_LOG_MISSING


def test_reconcile_event_log_truncated(tmp_path):
    """AC: truncated last line → event_log_truncated."""
    events_path, _ = _make_session(tmp_path)
    _open_session(events_path)
    write_manifest(tmp_path)
    with open(events_path, "a", encoding="utf-8") as f:
        f.write('{"event_id":"partial')
    report = reconcile(tmp_path)
    assert report.ok is False
    assert report.error_code == REC_EVENT_LOG_TRUNCATED


def test_reconcile_event_chain_broken(tmp_path):
    events_path, _ = _make_session(tmp_path)
    _open_session(events_path)
    write_manifest(tmp_path)
    lines = events_path.read_text().splitlines()
    record = json.loads(lines[0])
    record["event_sha256"] = "f" * 64
    lines[0] = json.dumps(record)
    events_path.write_text("\n".join(lines) + "\n")
    report = reconcile(tmp_path)
    assert report.ok is False
    assert report.error_code == REC_EVENT_CHAIN_BROKEN


def test_reconcile_manifest_events_mismatch(tmp_path):
    """Stored manifest tampered to disagree with events projection."""
    events_path, manifest_path = _make_session(tmp_path)
    _open_session(events_path)
    write_manifest(tmp_path)
    stored = json.loads(manifest_path.read_text())
    stored["state"] = "CLOSED"
    manifest_path.write_text(json.dumps(stored))
    report = reconcile(tmp_path)
    assert report.ok is False
    assert report.error_code == REC_MANIFEST_EVENTS_MISMATCH
    assert report.findings[0].detail.get("field") == "state"


def test_reconcile_manifest_stale_last_event_id(tmp_path):
    """Manifest written before the last event was appended → mismatch on last_event_id."""
    events_path, _ = _make_session(tmp_path)
    sha = _open_session(events_path)
    write_manifest(tmp_path)  # manifest has event 1

    ns, wall = _ts()
    append_event(events_path, "state_transition",
                 {"from_state": "OPEN", "to_state": "RUNNING"}, sha, ns, wall)
    # Do NOT call write_manifest — manifest is now stale.
    report = reconcile(tmp_path)
    assert report.ok is False
    assert report.error_code == REC_MANIFEST_EVENTS_MISMATCH


def test_reconcile_artifact_missing(tmp_path):
    """AC: artifact in observed_artifacts deleted from disk → artifact_missing."""
    events_path, _ = _make_session(tmp_path)
    artifact = tmp_path / "run_record.json"
    artifact.write_text('{"run": 1}')

    sha = _open_session(events_path)
    sha = _add_artifact(events_path, sha, artifact)
    write_manifest(tmp_path)
    artifact.unlink()

    report = reconcile(tmp_path)
    assert report.ok is False
    assert any(f.error_code == REC_ARTIFACT_MISSING for f in report.findings)
    assert any("path" in f.detail for f in report.findings)


def test_reconcile_artifact_hash_mismatch(tmp_path):
    """AC: artifact content changed after verification → artifact_hash_mismatch."""
    events_path, _ = _make_session(tmp_path)
    artifact = tmp_path / "run_record.json"
    artifact.write_text('{"run": 1}')

    sha = _open_session(events_path)
    sha = _add_artifact(events_path, sha, artifact)
    write_manifest(tmp_path)
    artifact.write_text('{"run": 999}')

    report = reconcile(tmp_path)
    assert report.ok is False
    assert any(f.error_code == REC_ARTIFACT_HASH_MISMATCH for f in report.findings)
    assert any("path" in f.detail for f in report.findings)


def test_reconcile_disk_orphan(tmp_path):
    """AC: file on disk absent from manifest (not in expected or observed) → disk_orphan."""
    events_path, _ = _make_session(tmp_path)
    _open_session(events_path)
    write_manifest(tmp_path)

    # Drop a file that was never recorded in any event.
    orphan = tmp_path / "untracked_artifact.json"
    orphan.write_text('{"surprise": true}')

    report = reconcile(tmp_path)
    assert report.ok is False
    assert any(f.error_code == REC_DISK_ORPHAN for f in report.findings)
    assert any(str(orphan) in f.detail.get("path", "") for f in report.findings)


def test_reconcile_disk_orphan_not_triggered_for_tracked_unverified(tmp_path):
    """A file in expected_artifacts but not observed_artifacts is tracked, not orphaned."""
    events_path, _ = _make_session(tmp_path)
    artifact = tmp_path / "run_record.json"
    artifact.write_text('{"run": 1}')
    sha = _sha256_of(artifact)

    prev = _open_session(events_path)
    ns, wall = _ts()
    # Record artifact_created ONLY — no artifact_verified.
    prev = append_event(
        events_path, "artifact_created",
        {"artifact_type": "run_record", "path": str(artifact),
         "sha256": sha, "artifact_schema_version": 1},
        prev, ns, wall,
    )
    write_manifest(tmp_path)

    report = reconcile(tmp_path)
    # The file is in expected_artifacts so it is tracked — not a disk_orphan.
    assert not any(f.error_code == REC_DISK_ORPHAN for f in report.findings)


def test_reconcile_multiple_artifact_findings_collected(tmp_path):
    """All artifact-level issues are collected, not just the first."""
    events_path, _ = _make_session(tmp_path)
    a1 = tmp_path / "r1.json"
    a2 = tmp_path / "r2.json"
    a1.write_text('{"r": 1}')
    a2.write_text('{"r": 2}')

    sha = _open_session(events_path)
    sha = _add_artifact(events_path, sha, a1)
    sha = _add_artifact(events_path, sha, a2)
    write_manifest(tmp_path)

    # Corrupt one, delete the other.
    a1.write_text('{"r": 999}')
    a2.unlink()

    report = reconcile(tmp_path)
    assert report.ok is False
    codes = {f.error_code for f in report.findings}
    assert REC_ARTIFACT_HASH_MISMATCH in codes
    assert REC_ARTIFACT_MISSING in codes
    assert len(report.findings) >= 2


def test_reconcile_report_as_dict_passes_schema(tmp_path):
    events_path, _ = _make_session(tmp_path)
    _open_session(events_path)
    report = reconcile(tmp_path)   # manifest_missing
    validate("reconciliation", report.as_dict())


def test_reconcile_clean_report_passes_schema(tmp_path):
    events_path, _ = _make_session(tmp_path)
    _open_session(events_path)
    write_manifest(tmp_path)
    report = reconcile(tmp_path)
    validate("reconciliation", report.as_dict())


def test_reconcile_all_error_codes_are_named_strings():
    codes = [
        REC_MANIFEST_MISSING, REC_EVENT_LOG_MISSING, REC_EVENT_LOG_TRUNCATED,
        REC_EVENT_CHAIN_BROKEN, REC_MANIFEST_EVENTS_MISMATCH,
        REC_ARTIFACT_MISSING, REC_ARTIFACT_HASH_MISMATCH, REC_DISK_ORPHAN,
    ]
    for code in codes:
        assert isinstance(code, str) and code != "unknown"


# ═══════════════════════════════════════════════════════════════════════════
# 4. schema validation
# ═══════════════════════════════════════════════════════════════════════════

def _valid_manifest():
    return {
        "schema_version": 1,
        "session_id": "ses-001",
        "rig_version": "0.1.0",
        "rig_self_sha256": SHA64,
        "git_commit": "abc1234",
        "state": "OPEN",
        "expected_artifacts": [],
        "observed_artifacts": [],
        "run_count_expected": 0,
        "run_count_observed": 0,
        "opened_at": "2025-06-01T12:00:00Z",
        "last_event_id": "some-uuid",
        "closure_ref": None,
    }


def test_session_manifest_schema_valid():
    validate("session_manifest", _valid_manifest())


def test_session_manifest_schema_all_states_valid():
    for state in ("OPEN", "RUNNING", "FINALIZING", "CLOSED", "INVALID", "INCOMPLETE"):
        validate("session_manifest", {**_valid_manifest(), "state": state})


def test_session_manifest_schema_unknown_state_rejected():
    with pytest.raises(SchemaValidationError):
        validate("session_manifest", {**_valid_manifest(), "state": "PENDING"})


def test_session_manifest_schema_extra_field_rejected():
    with pytest.raises(SchemaValidationError):
        validate("session_manifest", {**_valid_manifest(), "notes": "bad"})


def test_session_manifest_schema_missing_field_rejected():
    bad = _valid_manifest()
    del bad["session_id"]
    with pytest.raises(SchemaValidationError):
        validate("session_manifest", bad)


def test_session_manifest_schema_with_artifacts():
    m = _valid_manifest()
    m["expected_artifacts"] = [{
        "artifact_type": "run_record",
        "path": "/runs/r1/run_record.json",
        "sha256": SHA64,
        "artifact_schema_version": 1,
    }]
    m["observed_artifacts"] = [{
        "path": "/runs/r1/run_record.json",
        "sha256": SHA64,
        "schema_version": 1,
    }]
    validate("session_manifest", m)


def _valid_reconciliation(ok=True, error_code=None, findings=None):
    return {
        "schema_version": 1,
        "ok": ok,
        "error_code": error_code,
        "findings": findings or [],
    }


def test_reconciliation_schema_clean_pass():
    validate("reconciliation", _valid_reconciliation())


def test_reconciliation_schema_with_structural_finding():
    validate("reconciliation", _valid_reconciliation(
        ok=False,
        error_code="manifest_missing",
        findings=[{"error_code": "manifest_missing", "detail": {}}],
    ))


def test_reconciliation_schema_with_multiple_findings():
    validate("reconciliation", _valid_reconciliation(
        ok=False,
        error_code="artifact_hash_mismatch",
        findings=[
            {"error_code": "artifact_hash_mismatch", "detail": {"path": "/r1"}},
            {"error_code": "artifact_missing", "detail": {"path": "/r2"}},
        ],
    ))


def test_reconciliation_schema_all_error_codes_valid():
    codes = [
        "manifest_missing", "event_log_missing", "event_log_truncated",
        "event_chain_broken", "manifest_events_mismatch",
        "artifact_missing", "artifact_hash_mismatch", "disk_orphan",
    ]
    for code in codes:
        validate("reconciliation", _valid_reconciliation(
            ok=False, error_code=code,
            findings=[{"error_code": code, "detail": {}}],
        ))


def test_reconciliation_schema_unknown_code_rejected():
    with pytest.raises(SchemaValidationError):
        validate("reconciliation", {**_valid_reconciliation(),
                 "findings": [{"error_code": "made_up", "detail": {}}]})


def test_reconciliation_schema_extra_top_field_rejected():
    with pytest.raises(SchemaValidationError):
        validate("reconciliation", {**_valid_reconciliation(), "free_text": "bad"})