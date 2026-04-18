"""
rig/allocation.py -- allocation registry (T3.1).

allocation_registry.jsonl records every cloud resource the rig observed during
a session.  The sweeper acts only on what appears here.

Confirmed entry shape (11 fields, all required, additionalProperties: false):

    schema_version          integer, const 1
    observation_ts_monotonic integer (nanoseconds)
    source_of_record        enum -- who reported this observation
    resource_type           enum -- what kind of AWS resource
    resource_id             string -- AWS resource identifier
    correlation_key         string -- links entry to a WAL record or run
    originating_run_id      string | null
    wal_event_ref           string | null -- null for non-WAL sources
    trust_level             enum -- how reliable is this observation
    prev_entry_sha256       hex64 -- "0"*64 sentinel for first entry
    entry_sha256            hex64 -- SHA-256 of all fields except entry_sha256

Cross-field constraints (enforced at write time):
    trust_level must match source_of_record (mapping table below)
    wal_event_ref must be null for sweep/boto3/head sources, non-null for wal/sidecar

Hash chain:
    entry_sha256 = sha256(canonical JSON of all fields except entry_sha256)
    prev_entry_sha256 is included in that canonical JSON, so the chain is
    bidirectionally verifiable.  The tail entry is self-authenticating.
"""

import hashlib
import json
from pathlib import Path
from typing import Any

from rig.atomic import atomic_append_line


# -- closed enumerations ------------------------------------------------------

SOURCE_WAL_EVENT               = "wal_event"
SOURCE_BOTO3_LAUNCH_RESPONSE   = "boto3_launch_response"
SOURCE_SIDECAR_OBSERVED        = "sidecar_observed"
SOURCE_SWEEP_PRE               = "sweep_pre"
SOURCE_SWEEP_POST              = "sweep_post"
SOURCE_SWEEP_DELAYED           = "sweep_delayed"
SOURCE_HEAD_OBJECT_INDEPENDENT = "head_object_independent"

KNOWN_SOURCES: frozenset[str] = frozenset({
    SOURCE_WAL_EVENT,
    SOURCE_BOTO3_LAUNCH_RESPONSE,
    SOURCE_SIDECAR_OBSERVED,
    SOURCE_SWEEP_PRE,
    SOURCE_SWEEP_POST,
    SOURCE_SWEEP_DELAYED,
    SOURCE_HEAD_OBJECT_INDEPENDENT,
})

RESOURCE_EC2_INSTANCE    = "ec2_instance"
RESOURCE_S3_OBJECT_VERSION = "s3_object_version"

KNOWN_RESOURCE_TYPES: frozenset[str] = frozenset({
    RESOURCE_EC2_INSTANCE,
    RESOURCE_S3_OBJECT_VERSION,
})

TRUST_AUTHORITATIVE = "authoritative"
TRUST_OBSERVED      = "observed"
TRUST_INFERRED      = "inferred"

KNOWN_TRUST_LEVELS: frozenset[str] = frozenset({
    TRUST_AUTHORITATIVE,
    TRUST_OBSERVED,
    TRUST_INFERRED,
})

# Mapping table (frozen): source_of_record -> required trust_level.
_REQUIRED_TRUST: dict[str, str] = {
    SOURCE_BOTO3_LAUNCH_RESPONSE:   TRUST_AUTHORITATIVE,
    SOURCE_HEAD_OBJECT_INDEPENDENT: TRUST_AUTHORITATIVE,
    SOURCE_WAL_EVENT:               TRUST_OBSERVED,
    SOURCE_SIDECAR_OBSERVED:        TRUST_OBSERVED,
    SOURCE_SWEEP_PRE:               TRUST_INFERRED,
    SOURCE_SWEEP_POST:              TRUST_INFERRED,
    SOURCE_SWEEP_DELAYED:           TRUST_INFERRED,
}

# Sources that must have a non-null wal_event_ref.
_WAL_EVENT_REF_REQUIRED: frozenset[str] = frozenset({
    SOURCE_WAL_EVENT,
    SOURCE_SIDECAR_OBSERVED,
})

# Sources that must have wal_event_ref = null.
_WAL_EVENT_REF_MUST_BE_NULL: frozenset[str] = frozenset({
    SOURCE_BOTO3_LAUNCH_RESPONSE,
    SOURCE_HEAD_OBJECT_INDEPENDENT,
    SOURCE_SWEEP_PRE,
    SOURCE_SWEEP_POST,
    SOURCE_SWEEP_DELAYED,
})

# Sentinel for the first entry (no predecessor).
FIRST_ENTRY_PREV_SHA256 = "0" * 64

SCHEMA_VERSION = 1


# -- exceptions ---------------------------------------------------------------

class AllocationChainError(Exception):
    """Raised when hash-chain integrity check fails."""


class AllocationTruncatedError(Exception):
    """Raised when the last line of the log is not valid JSON (crash signal)."""


class InvalidAllocationEntry(ValueError):
    """Raised when an entry field violates a closed enum or cross-field rule."""


# -- internal -----------------------------------------------------------------

def _canonical(obj: dict[str, Any]) -> bytes:
    return json.dumps(obj, sort_keys=True, separators=(",", ":")).encode()


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _compute_entry_sha256(entry_without_self_hash: dict[str, Any]) -> str:
    """SHA-256 over all fields except entry_sha256."""
    return _sha256(_canonical(entry_without_self_hash))


def _validate(
    source_of_record: str,
    resource_type: str,
    trust_level: str,
    wal_event_ref: "str | None",
) -> None:
    """Enforce all closed enums and both cross-field constraints."""
    if source_of_record not in KNOWN_SOURCES:
        raise InvalidAllocationEntry(
            f"unknown source_of_record: {source_of_record!r}"
        )
    if resource_type not in KNOWN_RESOURCE_TYPES:
        raise InvalidAllocationEntry(
            f"unknown resource_type: {resource_type!r}"
        )
    if trust_level not in KNOWN_TRUST_LEVELS:
        raise InvalidAllocationEntry(
            f"unknown trust_level: {trust_level!r}"
        )
    required_trust = _REQUIRED_TRUST[source_of_record]
    if trust_level != required_trust:
        raise InvalidAllocationEntry(
            f"trust_level {trust_level!r} is wrong for source_of_record "
            f"{source_of_record!r} (expected {required_trust!r})"
        )
    if source_of_record in _WAL_EVENT_REF_REQUIRED and wal_event_ref is None:
        raise InvalidAllocationEntry(
            f"source_of_record {source_of_record!r} requires a non-null wal_event_ref"
        )
    if source_of_record in _WAL_EVENT_REF_MUST_BE_NULL and wal_event_ref is not None:
        raise InvalidAllocationEntry(
            f"source_of_record {source_of_record!r} requires wal_event_ref=null, "
            f"got {wal_event_ref!r}"
        )


# -- public API ---------------------------------------------------------------

def append_entry(
    path: Path,
    observation_ts_monotonic: int,
    source_of_record: str,
    resource_type: str,
    resource_id: str,
    correlation_key: str,
    originating_run_id: "str | None",
    wal_event_ref: "str | None",
    trust_level: str,
    prev_entry_sha256: str,
) -> str:
    """Append one self-authenticating allocation entry; return its entry_sha256.

    For the first entry in a new log, pass prev_entry_sha256=FIRST_ENTRY_PREV_SHA256.
    The returned entry_sha256 is the correct prev_entry_sha256 for the next call.

    Raises InvalidAllocationEntry on enum or cross-field violations.
    Raises OSError on write failure (from atomic_append_line).
    """
    _validate(source_of_record, resource_type, trust_level, wal_event_ref)

    body: dict[str, Any] = {
        "schema_version":           SCHEMA_VERSION,
        "observation_ts_monotonic": observation_ts_monotonic,
        "source_of_record":         source_of_record,
        "resource_type":            resource_type,
        "resource_id":              resource_id,
        "correlation_key":          correlation_key,
        "originating_run_id":       originating_run_id,
        "wal_event_ref":            wal_event_ref,
        "trust_level":              trust_level,
        "prev_entry_sha256":        prev_entry_sha256,
    }
    sha = _compute_entry_sha256(body)
    record = {**body, "entry_sha256": sha}

    atomic_append_line(str(path), json.dumps(record, sort_keys=True, separators=(",", ":")))
    return sha


def read_entries(path: Path) -> list[dict[str, Any]]:
    """Read all entries in append order.

    Returns empty list when the file does not exist.
    Raises AllocationTruncatedError if the final line is not valid JSON.
    Raises AllocationChainError if any non-final line is not valid JSON.
    """
    if not path.exists():
        return []

    raw = [l for l in path.read_text(encoding="utf-8").splitlines() if l.strip()]
    if not raw:
        return []

    entries: list[dict[str, Any]] = []
    for i, line in enumerate(raw):
        is_last = (i == len(raw) - 1)
        try:
            entries.append(json.loads(line))
        except json.JSONDecodeError as exc:
            if is_last:
                raise AllocationTruncatedError(
                    f"line {i} is not valid JSON -- likely truncated by a crash"
                ) from exc
            raise AllocationChainError(
                f"line {i} is not valid JSON -- log is corrupted"
            ) from exc

    return entries


def verify_chain(entries: list[dict[str, Any]]) -> None:
    """Verify every entry including the tail.

    For each entry:
      - recompute entry_sha256 over all fields except entry_sha256; compare to stored.
      - verify prev_entry_sha256 equals the prior entry's entry_sha256 (or sentinel).

    Raises AllocationChainError on the first violation.
    An empty list passes trivially.
    """
    if not entries:
        return

    if entries[0].get("prev_entry_sha256") != FIRST_ENTRY_PREV_SHA256:
        raise AllocationChainError(
            "first entry must have prev_entry_sha256 equal to the sentinel "
            f"({FIRST_ENTRY_PREV_SHA256[:8]}...)"
        )

    for i, entry in enumerate(entries):
        stored = entry.get("entry_sha256")
        body   = {k: v for k, v in entry.items() if k != "entry_sha256"}
        if _compute_entry_sha256(body) != stored:
            raise AllocationChainError(
                f"entry index {i}: entry_sha256 mismatch -- entry has been tampered"
            )
        if i > 0:
            expected_prev = entries[i - 1]["entry_sha256"]
            if entry.get("prev_entry_sha256") != expected_prev:
                raise AllocationChainError(
                    f"entry index {i}: prev_entry_sha256 mismatch"
                )