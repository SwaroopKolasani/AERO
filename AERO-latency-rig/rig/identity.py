"""
rig/identity.py — session and run identity (T2.1).

Session ID: <ISO8601>-<uuid4>
    Unique, not deterministic.  Uses current wall-clock time plus uuid4()
    to guarantee uniqueness.  The ISO 8601 prefix makes IDs sortable by
    creation time without parsing the UUID.  There is no "session ID given
    inputs" concept; each new session gets a new ID.

Run identity: a closed dict with eight fields.
    Deterministic given inputs: calling new_run_identity() twice with the
    same arguments always produces the same dict.  The dict is embedded in
    run_record.json as run_identity.
"""

import hashlib
import uuid
from datetime import datetime, timezone


# The only schema_version this module produces and accepts.
RUN_IDENTITY_SCHEMA_VERSION = 1

UNKNOWN_SCHEMA_VERSION = "unknown_run_identity_schema_version"


class UnknownSchemaVersionError(ValueError):
    """Raised when a run identity blob carries an unrecognised schema_version."""
    error_code = UNKNOWN_SCHEMA_VERSION

    def __init__(self, got: object) -> None:
        super().__init__(
            f"run identity schema_version {got!r} is not supported "
            f"(expected {RUN_IDENTITY_SCHEMA_VERSION})"
        )
        self.got = got


def new_session_id() -> str:
    """Return a new session ID: <ISO8601>-<uuid4>.

    Example: 2025-06-01T12:00:00.000000+00:00-550e8400-e29b-41d4-a716-446655440000

    The ISO 8601 timestamp uses UTC and microsecond precision, which makes
    the IDs lexicographically sortable by creation time.
    """
    ts = datetime.now(timezone.utc).isoformat()
    return f"{ts}-{uuid.uuid4()}"


def new_run_identity(
    session_id: str,
    run_ordinal: int,
    profile: str,
    attempt_ordinal: int,
    cli_command: str,
    fixture_semver: str,
    rig_version: str,
) -> dict:
    """Build a run identity dict from the caller-supplied inputs.

    cli_command_sha256 is computed here so the caller does not have to
    handle hashing.  All other fields are stored as-is.

    Returns a dict that is deterministic: calling this function twice with
    the same arguments produces equal dicts.
    """
    return {
        "schema_version":     RUN_IDENTITY_SCHEMA_VERSION,
        "session_id":         session_id,
        "run_ordinal":        run_ordinal,
        "profile":            profile,
        "attempt_ordinal":    attempt_ordinal,
        "cli_command_sha256": hashlib.sha256(cli_command.encode()).hexdigest(),
        "fixture_semver":     fixture_semver,
        "rig_version":        rig_version,
    }


def validate_run_identity(blob: object) -> dict:
    """Validate that blob is a run identity dict with a supported schema_version.

    Returns blob (unchanged) if valid.
    Raises UnknownSchemaVersionError if schema_version is absent or unsupported.
    Raises TypeError if blob is not a dict.
    """
    if not isinstance(blob, dict):
        raise TypeError(f"run identity must be a dict, got {type(blob).__name__!r}")
    got = blob.get("schema_version")
    if got != RUN_IDENTITY_SCHEMA_VERSION:
        raise UnknownSchemaVersionError(got)
    return blob