"""
rig/envelope.py -- envelope recorders (T4.1).

Two distinct artifacts, two distinct schemas, no shared fields beyond
schema_version and session_id.  They must never be merged.

session_envelope.json
    Written once when the session transitions to OPEN.
    Records rig configuration, profile plan, reproducibility inputs, and
    AWS credential context.  Fields: schema_version, session_id,
    rig_version, rig_self_sha256, git_commit, fixture_manifest_sha256,
    fixture_semver, profile_plan, prng_seed, region, account_id_hash,
    sts_credential_remaining_s, captured_at.

invocation_envelope.json
    Written once per run before subprocess launch.
    Records the exact invocation parameters.  env must always be {} per
    ADR-002; a non-empty env is rejected before subprocess launch.
    Fields: schema_version, session_id, run_id, argv, env, cwd,
    stdin_policy, ulimits, timeout_s, parent_pid, captured_at.
"""

import hashlib
import json
import os
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from rig.atomic import atomic_write
from rig.schemas import validate


ENV_NOT_EMPTY = "env_not_empty"


class NonEmptyEnvError(ValueError):
    """Raised when a caller passes a non-empty env dict.

    ADR-002 requires the subprocess environment to be empty.  Any env
    variable present would make runs environment-dependent and non-reproducible.
    """
    error_code = ENV_NOT_EMPTY

    def __init__(self, keys: list[str]) -> None:
        super().__init__(
            f"env must be {{}} per ADR-002; received keys: {sorted(keys)}"
        )
        self.keys = keys


# -- session envelope ---------------------------------------------------------

def write_session_envelope(
    output_path: Path,
    session_id: str,
    rig_version: str,
    rig_self_sha256: str,
    git_commit: str,
    fixture_manifest_sha256: str,
    fixture_semver: str,
    profile_plan: list[str],
    prng_seed: str,
    region: str,
    account_id: str,
    sts_credential_remaining_s: int,
    run_schedule: list[str],
) -> dict[str, Any]:
    """Write session_envelope.json atomically; return the doc dict.

    account_id is hashed before storage so the raw AWS account number is
    never written to disk.  The caller passes the plaintext account ID and
    this function does the hashing.

    run_schedule: the PRNG-seeded ordered run list from driver.generate_schedule().
        Must be generated before calling this function -- the session envelope
        is written once with the schedule already committed.

    Raises OSError on write failure.
    """
    account_id_hash = hashlib.sha256(account_id.encode()).hexdigest()
    doc: dict[str, Any] = {
        "schema_version":             1,
        "session_id":                 session_id,
        "rig_version":                rig_version,
        "rig_self_sha256":            rig_self_sha256,
        "git_commit":                 git_commit,
        "fixture_manifest_sha256":    fixture_manifest_sha256,
        "fixture_semver":             fixture_semver,
        "profile_plan":               profile_plan,
        "prng_seed":                  prng_seed,
        "region":                     region,
        "account_id_hash":            account_id_hash,
        "sts_credential_remaining_s": sts_credential_remaining_s,
        "session_opened_at":           datetime.now(timezone.utc).isoformat(),
    }
    doc["run_schedule"] = run_schedule
    validate("session_envelope", doc)
    atomic_write(str(output_path), json.dumps(doc, indent=2))
    return doc


# -- invocation envelope ------------------------------------------------------

def write_invocation_envelope(
    output_path: Path,
    session_id: str,
    run_id: str,
    argv: list[str],
    env: dict[str, str],
    cwd: str,
    stdin_policy: str,
    ulimits: dict[str, Any],
    timeout_s: float,
) -> dict[str, Any]:
    """Write invocation_envelope.json atomically; return the doc dict.

    Raises NonEmptyEnvError if env is not empty (ADR-002).
    Raises OSError on write failure.
    """
    if env:
        raise NonEmptyEnvError(list(env.keys()))

    doc: dict[str, Any] = {
        "schema_version": 1,
        "session_id":     session_id,
        "run_id":         run_id,
        "argv":           argv,
        "env":            {},
        "cwd":            cwd,
        "stdin_policy":   stdin_policy,
        "ulimits":        ulimits,
        "timeout_s":      timeout_s,
        "parent_pid":     os.getpid(),
        "invocation_captured_at": datetime.now(timezone.utc).isoformat(),
    }
    validate("invocation_envelope", doc)
    atomic_write(str(output_path), json.dumps(doc, indent=2))
    return doc