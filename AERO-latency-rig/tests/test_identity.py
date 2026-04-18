"""
Tests for rig/identity.py — T2.1.

AC scoping:
  "Identities are deterministic given inputs" applies to new_run_identity()
  only.  new_session_id() is intentionally non-deterministic (unique, not
  reproducible); that is correct for a session-ID generator.

AC:
  1. Run identity is deterministic given inputs (new_run_identity).
  2. identity.schema.json validates example tuples.
  3. Unknown schema_version on an identity blob is rejected.

Session ID tests cover uniqueness and format, not determinism.
"""

import hashlib
import re

import pytest

from rig.identity import (
    RUN_IDENTITY_SCHEMA_VERSION,
    UNKNOWN_SCHEMA_VERSION,
    UnknownSchemaVersionError,
    new_run_identity,
    new_session_id,
    validate_run_identity,
)
from rig.schemas import SchemaValidationError, validate


# ── new_session_id: unique, not deterministic ─────────────────────────────────

def test_session_id_format():
    sid = new_session_id()
    # The last 36 characters are a UUID4 (fixed length; the ISO prefix has dashes too).
    uuid_part = sid[-36:]
    assert re.fullmatch(
        r"[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}",
        uuid_part,
    ), f"uuid4 suffix malformed: {uuid_part!r}"


def test_session_id_separator_between_ts_and_uuid():
    sid = new_session_id()
    # The character just before the 36-char UUID suffix must be a dash.
    assert sid[-37] == "-"


def test_session_id_contains_utc_marker():
    sid = new_session_id()
    assert "+00:00" in sid


def test_session_id_is_unique():
    ids = {new_session_id() for _ in range(100)}
    assert len(ids) == 100


def test_session_id_two_calls_differ():
    assert new_session_id() != new_session_id()


# ── new_run_identity: AC 1 — deterministic given inputs ──────────────────────

def test_run_identity_deterministic():
    """AC: same inputs always produce the same dict."""
    args = ("ses-001", 1, "B3a", 1, "rig run --session ses-001", "1.0.0", "0.1.0")
    assert new_run_identity(*args) == new_run_identity(*args)


def test_run_identity_deterministic_repeated():
    args = ("ses-001", 2, "full_aero", 1, "rig run", "2.0.0", "0.2.0")
    results = [new_run_identity(*args) for _ in range(10)]
    assert all(r == results[0] for r in results)


def test_run_identity_different_session_differs():
    base = ("ses-001", 1, "B3a", 1, "rig run", "1.0.0", "0.1.0")
    r1 = new_run_identity(*base)
    r2 = new_run_identity("ses-002", 1, "B3a", 1, "rig run", "1.0.0", "0.1.0")
    assert r1 != r2


def test_run_identity_different_ordinal_differs():
    base = ("ses-001", 1, "B3a", 1, "rig run", "1.0.0", "0.1.0")
    r1 = new_run_identity(*base)
    r2 = new_run_identity("ses-001", 2, "B3a", 1, "rig run", "1.0.0", "0.1.0")
    assert r1 != r2


def test_run_identity_different_command_differs():
    r1 = new_run_identity("s", 1, "B3a", 1, "cmd-a", "1.0.0", "0.1.0")
    r2 = new_run_identity("s", 1, "B3a", 1, "cmd-b", "1.0.0", "0.1.0")
    assert r1["cli_command_sha256"] != r2["cli_command_sha256"]


def test_run_identity_cli_command_sha256_is_hex64():
    rid = new_run_identity("s", 1, "B3a", 1, "rig run", "1.0.0", "0.1.0")
    h = rid["cli_command_sha256"]
    assert len(h) == 64
    assert all(c in "0123456789abcdef" for c in h)


def test_run_identity_cli_command_sha256_matches_input():
    cmd = "rig run --session ses-001 --profile B3a"
    rid = new_run_identity("ses-001", 1, "B3a", 1, cmd, "1.0.0", "0.1.0")
    assert rid["cli_command_sha256"] == hashlib.sha256(cmd.encode()).hexdigest()


def test_run_identity_fields_stored_exactly():
    rid = new_run_identity("ses-x", 3, "full_aero", 2, "cmd", "2.1.0", "0.2.0")
    assert rid["session_id"]      == "ses-x"
    assert rid["run_ordinal"]     == 3
    assert rid["profile"]         == "full_aero"
    assert rid["attempt_ordinal"] == 2
    assert rid["fixture_semver"]  == "2.1.0"
    assert rid["rig_version"]     == "0.2.0"


def test_run_identity_schema_version_is_constant():
    rid = new_run_identity("s", 1, "B3a", 1, "c", "1.0.0", "0.1.0")
    assert rid["schema_version"] == RUN_IDENTITY_SCHEMA_VERSION


def test_run_identity_has_exactly_eight_fields():
    rid = new_run_identity("s", 1, "B3a", 1, "c", "1.0.0", "0.1.0")
    assert len(rid) == 8


# ── identity.schema.json: AC 2 — validates example tuples ────────────────────

def _valid():
    return new_run_identity("ses-001", 1, "B3a", 1, "rig run", "1.0.0", "0.1.0")


def test_schema_validates_generated_identity():
    """AC: identity.schema.json accepts what new_run_identity() produces."""
    validate("identity", _valid())


def test_schema_validates_all_benchmark_profiles():
    for profile in ("B2", "B3a", "B3b", "full_aero", "deliberate_failure"):
        validate("identity", new_run_identity("s", 1, profile, 1, "c", "1.0.0", "0.1.0"))


def test_schema_rejects_extra_field():
    with pytest.raises(SchemaValidationError):
        validate("identity", {**_valid(), "notes": "not allowed"})


def test_schema_rejects_missing_field():
    bad = _valid()
    del bad["run_ordinal"]
    with pytest.raises(SchemaValidationError):
        validate("identity", bad)


def test_schema_rejects_run_ordinal_zero():
    with pytest.raises(SchemaValidationError):
        validate("identity", {**_valid(), "run_ordinal": 0})


def test_schema_rejects_attempt_ordinal_zero():
    with pytest.raises(SchemaValidationError):
        validate("identity", {**_valid(), "attempt_ordinal": 0})


def test_schema_rejects_bad_sha256():
    with pytest.raises(SchemaValidationError):
        validate("identity", {**_valid(), "cli_command_sha256": "not-hex"})


def test_schema_rejects_wrong_schema_version():
    with pytest.raises(SchemaValidationError):
        validate("identity", {**_valid(), "schema_version": 2})


# ── validate_run_identity: AC 3 — unknown schema_version rejected ─────────────

def test_validate_accepts_correct_schema_version():
    rid = _valid()
    assert validate_run_identity(rid) is rid


def test_validate_rejects_wrong_schema_version():
    """AC: unknown schema_version raises UnknownSchemaVersionError."""
    bad = {**_valid(), "schema_version": 99}
    with pytest.raises(UnknownSchemaVersionError) as exc_info:
        validate_run_identity(bad)
    assert exc_info.value.got == 99
    assert exc_info.value.error_code == UNKNOWN_SCHEMA_VERSION


def test_validate_rejects_missing_schema_version():
    bad = _valid()
    del bad["schema_version"]
    with pytest.raises(UnknownSchemaVersionError) as exc_info:
        validate_run_identity(bad)
    assert exc_info.value.got is None


def test_validate_rejects_string_schema_version():
    with pytest.raises(UnknownSchemaVersionError):
        validate_run_identity({**_valid(), "schema_version": "1"})


def test_validate_rejects_non_dict():
    with pytest.raises(TypeError):
        validate_run_identity(["schema_version", 1])


def test_validate_error_code_is_named_string():
    assert UNKNOWN_SCHEMA_VERSION == "unknown_run_identity_schema_version"
    assert UNKNOWN_SCHEMA_VERSION != "unknown"


def test_validate_error_is_subclass_of_value_error():
    assert issubclass(UnknownSchemaVersionError, ValueError)


# ── run_record integration: run_identity satisfies T0.3b contract ─────────────

def test_run_identity_satisfies_run_record_schema():
    SHA64 = "a" * 64
    run_record = {
        "schema_version": 1,
        "run_identity": _valid(),
        "outcome_code": "success",
        "wallclock_sha256": SHA64,
        "raw_artifacts": {
            "stdout_sha256": SHA64,
            "stderr_sha256": SHA64,
            "wal_raw_sha256": SHA64,
        },
        "completion_proof_sha256": SHA64,
        "outputs_audit_sha256": SHA64,
        "classifier_reason": "success",
    }
    validate("run_record", run_record)


def test_run_record_rejects_run_identity_without_schema_version():
    SHA64 = "a" * 64
    rid = _valid()
    del rid["schema_version"]
    run_record = {
        "schema_version": 1,
        "run_identity": rid,
        "outcome_code": "success",
        "wallclock_sha256": SHA64,
        "raw_artifacts": {
            "stdout_sha256": SHA64,
            "stderr_sha256": SHA64,
            "wal_raw_sha256": SHA64,
        },
        "completion_proof_sha256": SHA64,
        "outputs_audit_sha256": SHA64,
        "classifier_reason": "success",
    }
    with pytest.raises(SchemaValidationError):
        validate("run_record", run_record)