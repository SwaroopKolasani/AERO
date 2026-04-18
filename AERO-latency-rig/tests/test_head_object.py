"""
Tests for T3.3 -- Independent HeadObject anchor (rig/sweeper.py).

AC:
  1. HeadObject call path shares no module imports with rig/completion.py.
     Verified by: (a) checking sweeper.py import lines contain no 'completion',
     and (b) confirming record_head_object only uses symbols available via
     module-level imports (rig.atomic, stdlib).

  2. Golden test for VersionId mismatch: T3.3 records the evidence T5.3
     needs to detect a mismatch.  The record preserves exactly what the rig
     requested (version_id passed to the call) alongside the full S3 response
     envelope.  T5.3 does the comparison; it is not tested here because T5.3
     does not exist yet.

Record shape (task spec + architecture requirement):
    version_id, etag, content_length, last_modified, request_id, ts_monotonic
    + response_metadata (full boto3 envelope, required by architecture)

Assumption surfaced: run_id/bucket/key are excluded per the reviewer's
guidance.  If T5.3 requires attribution fields, they will be added then.
"""

import inspect
import json
from datetime import datetime, timezone
from pathlib import Path
from unittest.mock import MagicMock

import pytest

from rig.schemas import SchemaValidationError, validate
from rig.sweeper import record_head_object


# -- fake boto3 HeadObject response -------------------------------------------

def _head_response(etag: str = '"abc123"', content_length: int = 4096) -> dict:
    return {
        "ETag":          etag,
        "ContentLength": content_length,
        "LastModified":  datetime(2025, 6, 1, 12, 0, 0, tzinfo=timezone.utc),
        "ResponseMetadata": {
            "RequestId":      "head-req-id-001",
            "HTTPStatusCode": 200,
        },
    }


def _mock_s3(response: dict | None = None) -> MagicMock:
    s3 = MagicMock()
    s3.head_object.return_value = response or _head_response()
    return s3


def _call(tmp_path, **overrides) -> tuple[dict, Path]:
    defaults = dict(
        s3_client  = _mock_s3(),
        bucket     = "my-bucket",
        key        = "runs/r1/output.zip",
        version_id = "ver001",
    )
    defaults.update(overrides)
    out = tmp_path / "head_object_raw.jsonl"
    rec = record_head_object(output_path=out, **defaults)
    return rec, out


# -- exact field set ----------------------------------------------------------

def test_record_has_exactly_the_seven_required_fields(tmp_path):
    rec, _ = _call(tmp_path)
    expected = {
        "version_id", "etag", "content_length", "last_modified",
        "request_id", "ts_monotonic", "response_metadata",
    }
    assert set(rec.keys()) == expected


def test_no_extra_identity_fields(tmp_path):
    """run_id, bucket, key, schema_version are not in the record."""
    rec, _ = _call(tmp_path)
    for forbidden in ("run_id", "bucket", "key", "schema_version"):
        assert forbidden not in rec, f"unexpected field: {forbidden!r}"


def test_version_id_stored(tmp_path):
    rec, _ = _call(tmp_path, version_id="ver-XYZ")
    assert rec["version_id"] == "ver-XYZ"


def test_etag_stored(tmp_path):
    rec, _ = _call(tmp_path, s3_client=_mock_s3(_head_response(etag='"deadbeef"')))
    assert rec["etag"] == '"deadbeef"'


def test_content_length_stored(tmp_path):
    rec, _ = _call(tmp_path, s3_client=_mock_s3(_head_response(content_length=8192)))
    assert rec["content_length"] == 8192


def test_request_id_stored(tmp_path):
    rec, _ = _call(tmp_path)
    assert rec["request_id"] == "head-req-id-001"


def test_ts_monotonic_is_positive_int(tmp_path):
    rec, _ = _call(tmp_path)
    assert isinstance(rec["ts_monotonic"], int)
    assert rec["ts_monotonic"] > 0


def test_full_response_metadata_stored(tmp_path):
    rec, _ = _call(tmp_path)
    assert "RequestId" in rec["response_metadata"]
    assert "HTTPStatusCode" in rec["response_metadata"]


# -- file output --------------------------------------------------------------

def test_creates_file_on_first_call(tmp_path):
    _, out = _call(tmp_path)
    assert out.exists()


def test_appends_one_line_per_call(tmp_path):
    out = tmp_path / "head_object_raw.jsonl"
    record_head_object(_mock_s3(), "b", "k", "v1", out)
    record_head_object(_mock_s3(), "b", "k", "v2", out)
    lines = [l for l in out.read_text().splitlines() if l.strip()]
    assert len(lines) == 2


def test_each_line_is_valid_json(tmp_path):
    _, out = _call(tmp_path)
    json.loads(out.read_text().strip())


def test_written_via_atomic_append_line(tmp_path, monkeypatch):
    import rig.sweeper as _mod
    calls: list[str] = []
    original = _mod.atomic_append_line

    def spy(path, line):
        calls.append(path)
        return original(path, line)

    monkeypatch.setattr(_mod, "atomic_append_line", spy)
    out = tmp_path / "head_object_raw.jsonl"
    record_head_object(_mock_s3(), "b", "k", "v1", out)
    assert len(calls) == 1
    assert calls[0] == str(out)


def test_head_object_called_with_exact_kwargs(tmp_path):
    s3 = _mock_s3()
    out = tmp_path / "head_object_raw.jsonl"
    record_head_object(s3, "my-bucket", "runs/r1/obj.zip", "ver-ABC", out)
    s3.head_object.assert_called_once_with(
        Bucket="my-bucket", Key="runs/r1/obj.zip", VersionId="ver-ABC"
    )


def test_multiple_calls_accumulate_in_one_file(tmp_path):
    """session-level JSONL: all runs' records go to one file."""
    out = tmp_path / "head_object_raw.jsonl"
    for i in range(4):
        record_head_object(_mock_s3(), "b", f"k{i}", f"v{i}", out)
    lines = [l for l in out.read_text().splitlines() if l.strip()]
    assert len(lines) == 4


# -- AC 2: VersionId mismatch evidence preserved for T5.3 --------------------

def test_version_id_mismatch_evidence_is_preserved(tmp_path):
    """AC 2: the record contains what T5.3 needs to detect a VersionId mismatch.

    T3.3 records exactly the version_id the rig passed plus the complete
    S3 ResponseMetadata.  T5.3 compares those against the WAL value and
    against any S3-returned version identity to detect the mismatch.

    T3.3's job is faithful recording.  The downstream classification by T5.3
    is not tested here because T5.3 does not exist yet.
    """
    wal_reported_version_id = "wal-ver-expected"

    s3 = _mock_s3(_head_response(etag='"wrong-etag-for-different-object"'))
    out = tmp_path / "head_object_raw.jsonl"

    rec = record_head_object(s3, "b", "k", wal_reported_version_id, out)

    # The requested version_id is preserved exactly.
    assert rec["version_id"] == wal_reported_version_id

    # The full S3 response envelope is present for T5.3 to inspect.
    assert "response_metadata" in rec
    assert rec["response_metadata"]["RequestId"] == "head-req-id-001"

    # The record on disk matches.
    stored = json.loads(out.read_text().strip())
    assert stored["version_id"] == wal_reported_version_id


def test_s3_clienterror_propagates_without_file(tmp_path):
    """HeadObject failure: no partial line written when the call raises."""
    from unittest.mock import MagicMock
    from botocore.exceptions import ClientError

    s3 = MagicMock()
    s3.head_object.side_effect = ClientError(
        {"Error": {"Code": "404", "Message": "Not Found"}}, "HeadObject"
    )
    out = tmp_path / "head_object_raw.jsonl"

    with pytest.raises(ClientError):
        record_head_object(s3, "b", "k", "v1", out)

    assert not out.exists()


# -- schema validation --------------------------------------------------------

def test_record_validates_against_schema(tmp_path):
    rec, _ = _call(tmp_path)
    validate("head_object_record", rec)


def test_disk_record_validates_against_schema(tmp_path):
    _, out = _call(tmp_path)
    validate("head_object_record", json.loads(out.read_text().strip()))


def test_schema_rejects_extra_field():
    with pytest.raises(SchemaValidationError):
        validate("head_object_record", {
            "version_id": "v", "etag": "", "content_length": 0,
            "last_modified": "", "request_id": "", "ts_monotonic": 1,
            "response_metadata": {}, "extra": "bad",
        })


def test_schema_rejects_missing_field():
    with pytest.raises(SchemaValidationError):
        validate("head_object_record", {"version_id": "v", "etag": ""})


# -- AC 1: import isolation ---------------------------------------------------

def test_sweeper_py_has_no_completion_imports():
    """AC 1: sweeper.py does not import from rig.completion (either direction).

    Scans only import-statement lines to avoid matching docstring references.
    When rig/completion.py is created for T5.3, this test ensures sweeper.py
    remains isolated from it.
    """
    import rig.sweeper as _mod
    import_lines = [
        l.strip() for l in inspect.getsource(_mod).splitlines()
        if l.strip().startswith(("import ", "from "))
    ]
    assert not any("completion" in l for l in import_lines), (
        f"sweeper.py imports from completion: "
        f"{[l for l in import_lines if 'completion' in l]}"
    )


def test_record_head_object_uses_only_module_level_names(tmp_path):
    """record_head_object uses only names from module-level imports.

    Specifically: json, time (stdlib) + atomic_append_line (rig.atomic).
    No imports inside the function body.
    """
    src = inspect.getsource(record_head_object)
    fn_lines = [l.strip() for l in src.splitlines()]
    # No import statements inside the function
    inner_imports = [l for l in fn_lines if l.startswith(("import ", "from "))]
    assert not inner_imports, f"unexpected imports inside function: {inner_imports}"