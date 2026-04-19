"""
Tests for rig/envelope.py -- T4.1.

AC:
  1. Two schemas, no shared fields except schema_version and session_id.
     Timestamp fields have distinct names: session_opened_at (session) vs
     invocation_captured_at (invocation).
  2. Attempting to pass a non-empty env is rejected before subprocess launch.
"""

import hashlib
import json
import os
from pathlib import Path

import pytest

from rig.driver import PROFILES, generate_schedule
from rig.envelope import (
    ENV_NOT_EMPTY,
    NonEmptyEnvError,
    write_invocation_envelope,
    write_session_envelope,
)
from rig.schemas import SchemaValidationError, validate


SHA64 = "a" * 64


# -- helpers ------------------------------------------------------------------

def _session_defaults() -> dict:
    return dict(
        session_id                = "ses-001",
        rig_version               = "0.1.0",
        rig_self_sha256           = SHA64,
        git_commit                = "abc1234",
        fixture_manifest_sha256   = SHA64,
        fixture_semver            = "1.0.0",
        profile_plan              = list(PROFILES),
        prng_seed                 = "0xAER0",
        region                    = "us-east-1",
        account_id                = "123456789012",
        sts_credential_remaining_s= 3600,
        run_schedule              = generate_schedule("0xAER0"),
    )


def _write_session(tmp_path: Path, **overrides) -> dict:
    kwargs = _session_defaults()
    kwargs.update(overrides)
    return write_session_envelope(tmp_path / "session_envelope.json", **kwargs)


def _invocation_defaults() -> dict:
    return dict(
        session_id   = "ses-001",
        run_id       = "run-001",
        argv         = ["rig", "run", "--profile", "B2"],
        env          = {},
        cwd          = "/sessions/ses-001",
        stdin_policy = "/dev/null",
        ulimits      = {},
        timeout_s    = 300.0,
    )


def _write_invocation(tmp_path: Path, **overrides) -> dict:
    kwargs = _invocation_defaults()
    kwargs.update(overrides)
    return write_invocation_envelope(tmp_path / "invocation_envelope.json", **kwargs)


# -- AC 1: no shared fields except schema_version and session_id --------------

def test_session_envelope_fields(tmp_path):
    doc = _write_session(tmp_path)
    expected = {
        "schema_version", "session_id", "rig_version", "rig_self_sha256",
        "git_commit", "fixture_manifest_sha256", "fixture_semver",
        "profile_plan", "prng_seed", "region", "account_id_hash",
        "sts_credential_remaining_s", "session_opened_at", "run_schedule",
    }
    assert set(doc.keys()) == expected


def test_invocation_envelope_fields(tmp_path):
    doc = _write_invocation(tmp_path)
    expected = {
        "schema_version", "session_id", "run_id", "argv", "env",
        "cwd", "stdin_policy", "ulimits", "timeout_s",
        "parent_pid", "invocation_captured_at",
    }
    assert set(doc.keys()) == expected


def test_no_shared_fields_beyond_allowed(tmp_path):
    """AC 1: the only shared fields are schema_version and session_id."""
    (tmp_path / "s").mkdir()
    (tmp_path / "i").mkdir()
    session_doc    = _write_session(tmp_path / "s")
    invocation_doc = _write_invocation(tmp_path / "i")
    shared = set(session_doc.keys()) & set(invocation_doc.keys())
    assert shared == {"schema_version", "session_id"}, (
        f"unexpected shared fields: {shared - {'schema_version', 'session_id'}}"
    )


def test_timestamp_fields_have_distinct_names(tmp_path):
    """session_opened_at and invocation_captured_at are different field names."""
    (tmp_path / "s").mkdir()
    (tmp_path / "i").mkdir()
    sess = _write_session(tmp_path / "s")
    inv  = _write_invocation(tmp_path / "i")
    assert "session_opened_at"      in sess
    assert "invocation_captured_at" in inv
    assert "captured_at"            not in sess
    assert "captured_at"            not in inv


# -- AC 2: non-empty env rejected before subprocess launch --------------------

def test_non_empty_env_raises_before_write(tmp_path):
    out = tmp_path / "invocation_envelope.json"
    with pytest.raises(NonEmptyEnvError) as exc_info:
        write_invocation_envelope(
            out, "ses-001", "run-001",
            ["rig", "run"], {"HOME": "/home/user"},
            "/sessions", "/dev/null", {}, 300.0,
        )
    assert exc_info.value.error_code == ENV_NOT_EMPTY
    assert not out.exists()


def test_non_empty_env_error_names_the_keys(tmp_path):
    out = tmp_path / "invocation_envelope.json"
    with pytest.raises(NonEmptyEnvError) as exc_info:
        write_invocation_envelope(
            out, "ses-001", "run-001",
            ["rig", "run"], {"HOME": "/home", "PATH": "/usr/bin"},
            "/sessions", "/dev/null", {}, 300.0,
        )
    assert "HOME" in exc_info.value.keys or "PATH" in exc_info.value.keys


def test_empty_env_accepted(tmp_path):
    doc = _write_invocation(tmp_path, env={})
    assert doc["env"] == {}


# -- session_envelope field correctness ---------------------------------------

def test_account_id_is_hashed(tmp_path):
    doc = _write_session(tmp_path, account_id="123456789012")
    assert "account_id" not in doc
    expected = hashlib.sha256("123456789012".encode()).hexdigest()
    assert doc["account_id_hash"] == expected


def test_session_envelope_stores_profile_plan(tmp_path):
    doc = _write_session(tmp_path, profile_plan=["B2", "B3a", "B3b"])
    assert doc["profile_plan"] == ["B2", "B3a", "B3b"]


def test_session_envelope_stores_prng_seed(tmp_path):
    doc = _write_session(tmp_path, prng_seed="0xDEADBEEF")
    assert doc["prng_seed"] == "0xDEADBEEF"


def test_session_envelope_stores_region(tmp_path):
    doc = _write_session(tmp_path, region="eu-west-1")
    assert doc["region"] == "eu-west-1"


def test_session_envelope_stores_sts_remaining(tmp_path):
    doc = _write_session(tmp_path, sts_credential_remaining_s=7200)
    assert doc["sts_credential_remaining_s"] == 7200


def test_session_opened_at_is_present(tmp_path):
    doc = _write_session(tmp_path)
    assert "session_opened_at" in doc
    assert doc["session_opened_at"]


# -- invocation_envelope field correctness ------------------------------------

def test_invocation_stores_argv(tmp_path):
    doc = _write_invocation(tmp_path, argv=["rig", "run", "--profile", "B3a"])
    assert doc["argv"] == ["rig", "run", "--profile", "B3a"]


def test_invocation_stores_cwd(tmp_path):
    doc = _write_invocation(tmp_path, cwd="/sessions/ses-001")
    assert doc["cwd"] == "/sessions/ses-001"


def test_invocation_stores_stdin_policy(tmp_path):
    doc = _write_invocation(tmp_path, stdin_policy="/dev/null")
    assert doc["stdin_policy"] == "/dev/null"


def test_invocation_stores_timeout(tmp_path):
    doc = _write_invocation(tmp_path, timeout_s=600.0)
    assert doc["timeout_s"] == 600.0


def test_invocation_stores_parent_pid(tmp_path):
    doc = _write_invocation(tmp_path)
    assert doc["parent_pid"] == os.getpid()


def test_invocation_stores_ulimits(tmp_path):
    ulimits = {"nofile": {"soft": 1024, "hard": 4096}}
    doc = _write_invocation(tmp_path, ulimits=ulimits)
    assert doc["ulimits"] == ulimits


def test_invocation_captured_at_is_present(tmp_path):
    doc = _write_invocation(tmp_path)
    assert "invocation_captured_at" in doc
    assert doc["invocation_captured_at"]


def test_env_stored_as_empty_dict(tmp_path):
    doc = _write_invocation(tmp_path)
    assert doc["env"] == {}


# -- file output and atomic write ---------------------------------------------

def test_session_envelope_creates_file(tmp_path):
    out = tmp_path / "session_envelope.json"
    assert not out.exists()
    write_session_envelope(out, **_session_defaults())
    assert out.exists()


def test_invocation_envelope_creates_file(tmp_path):
    out = tmp_path / "invocation_envelope.json"
    assert not out.exists()
    write_invocation_envelope(out, **_invocation_defaults())
    assert out.exists()


def test_session_envelope_is_valid_json(tmp_path):
    out = tmp_path / "session_envelope.json"
    write_session_envelope(out, **_session_defaults())
    json.loads(out.read_text())


def test_invocation_envelope_is_valid_json(tmp_path):
    out = tmp_path / "invocation_envelope.json"
    write_invocation_envelope(out, **_invocation_defaults())
    json.loads(out.read_text())


def test_session_envelope_uses_atomic_write(tmp_path, monkeypatch):
    import rig.envelope as _mod
    calls: list[str] = []
    original = _mod.atomic_write

    def spy(path, data):
        calls.append(path)
        return original(path, data)

    monkeypatch.setattr(_mod, "atomic_write", spy)
    out = tmp_path / "session_envelope.json"
    write_session_envelope(out, **_session_defaults())
    assert str(out) in calls


def test_invocation_envelope_uses_atomic_write(tmp_path, monkeypatch):
    import rig.envelope as _mod
    calls: list[str] = []
    original = _mod.atomic_write

    def spy(path, data):
        calls.append(path)
        return original(path, data)

    monkeypatch.setattr(_mod, "atomic_write", spy)
    out = tmp_path / "invocation_envelope.json"
    write_invocation_envelope(out, **_invocation_defaults())
    assert str(out) in calls


# -- schema validation --------------------------------------------------------

def test_session_envelope_validates_against_schema(tmp_path):
    doc = _write_session(tmp_path)
    validate("session_envelope", doc)


def test_invocation_envelope_validates_against_schema(tmp_path):
    doc = _write_invocation(tmp_path)
    validate("invocation_envelope", doc)


def test_session_schema_rejects_extra_field():
    with pytest.raises(SchemaValidationError):
        validate("session_envelope", {
            "schema_version": 1, "session_id": "s", "rig_version": "0.1",
            "rig_self_sha256": SHA64, "git_commit": "abc",
            "fixture_manifest_sha256": SHA64, "fixture_semver": "1.0",
            "profile_plan": list(PROFILES), "prng_seed": "0x1",
            "region": "us-east-1", "account_id_hash": SHA64,
            "sts_credential_remaining_s": 0, "session_opened_at": "2025-01-01",
            "run_schedule": generate_schedule("0x1"),
            "extra": "bad",
        })


def test_invocation_schema_rejects_nonempty_env():
    """The schema itself enforces empty env (maxProperties: 0)."""
    with pytest.raises(SchemaValidationError):
        validate("invocation_envelope", {
            "schema_version": 1, "session_id": "s", "run_id": "r",
            "argv": ["rig"], "env": {"HOME": "/home"},
            "cwd": "/", "stdin_policy": "/dev/null",
            "ulimits": {}, "timeout_s": 1.0,
            "parent_pid": 1, "invocation_captured_at": "2025-01-01",
        })


def test_session_schema_rejects_missing_field():
    with pytest.raises(SchemaValidationError):
        validate("session_envelope", {"schema_version": 1, "session_id": "s"})


def test_invocation_schema_rejects_missing_field():
    with pytest.raises(SchemaValidationError):
        validate("invocation_envelope", {"schema_version": 1, "session_id": "s"})