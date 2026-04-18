"""
rig/allocation.py — allocation registry (T3.1).

allocation_registry.jsonl records every cloud resource that the rig allocates
or releases during a session.  It is the sole authoritative input for orphan
sweep and cleanup.  The sweeper never scans by tag or account; it acts only
on InstanceId and (key, VersionId) tuples recorded here.

Protocol
--------
Hash-chained, append-only.  Each event stores a SHA-256 of its own content
(event_sha256) and the SHA-256 of the preceding event (prev_event_sha256).
This makes every record self-authenticating and makes the tail tamper-evident
— no successor is needed to detect corruption of the last record.

Event types (closed set)
------------------------
vm_allocated        EC2 instance launched for a run.
vm_terminated       EC2 instance confirmed reached 'terminated' state.
s3_object_created   S3 object written (input archive, output archive, sidecar).
s3_object_deleted   S3 object explicitly deleted during cleanup.

All writes go through rig.atomic.  No direct file writes in this module.
"""

import hashlib
import json
import uuid
from pathlib import Path
from typing import Any

from rig.atomic import atomic_append_line


# ── closed event-type set ─────────────────────────────────────────────────────

KNOWN_EVENT_TYPES: frozenset[str] = frozenset({
    "vm_allocated",
    "vm_terminated",
    "s3_object_created",
    "s3_object_deleted",
})

# Exact payload fields per event type — all required, no extras permitted.
# Source: ADR-005 cleanup semantics and sweep correlation requirements.
_PAYLOAD_FIELDS: dict[str, frozenset[str]] = {
    "vm_allocated": frozenset({
        "instance_id",
        "ami_id",
        "instance_type",
        "session_id",
        "run_id",
    }),
    "vm_terminated": frozenset({
        "instance_id",
    }),
    "s3_object_created": frozenset({
        "key",
        "version_id",
        "sha256",
        "size_bytes",
        "purpose",       # "input_archive" | "output_archive" | "sidecar"
        "session_id",
        "run_id",
    }),
    "s3_object_deleted": frozenset({
        "key",
        "version_id",
    }),
}

# Closed set of S3 object purposes (from ADR-003).
_S3_PURPOSES: frozenset[str] = frozenset({
    "input_archive",
    "output_archive",
    "sidecar",
})


# ── exceptions ────────────────────────────────────────────────────────────────

class AllocationChainError(Exception):
    """Raised when hash-chain integrity check fails."""


class AllocationTruncatedError(Exception):
    """Raised when the last line of the log is not valid JSON (crash signal).

    The session should be treated as INCOMPLETE-eligible.
    """


class UnknownAllocationEventType(ValueError):
    """Raised when event_type is not in the closed set."""


class InvalidAllocationPayload(ValueError):
    """Raised when payload violates the closed shape for its event type."""


# ── internal ──────────────────────────────────────────────────────────────────

def _canonical(obj: dict[str, Any]) -> bytes:
    return json.dumps(obj, sort_keys=True, separators=(",", ":")).encode()


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _event_sha256(body: dict[str, Any]) -> str:
    """SHA-256 of the six body fields (everything except event_sha256)."""
    return _sha256(_canonical(body))


def _validate_payload(event_type: str, payload: dict[str, Any]) -> None:
    allowed  = _PAYLOAD_FIELDS[event_type]
    extra    = set(payload) - allowed
    missing  = allowed - set(payload)
    if extra:
        raise InvalidAllocationPayload(
            f"{event_type}: unexpected payload fields: {sorted(extra)}"
        )
    if missing:
        raise InvalidAllocationPayload(
            f"{event_type}: missing payload fields: {sorted(missing)}"
        )
    if event_type == "s3_object_created":
        purpose = payload.get("purpose", "")
        if purpose not in _S3_PURPOSES:
            raise InvalidAllocationPayload(
                f"s3_object_created: unknown purpose {purpose!r}"
            )


# ── public API ────────────────────────────────────────────────────────────────

def append_event(
    path: Path,
    event_type: str,
    payload: dict[str, Any],
    prev_sha256: "str | None",
    ts_monotonic_ns: int,
    ts_wall: str,
) -> str:
    """Append one self-authenticating allocation event; return its event_sha256.

    Raises UnknownAllocationEventType for unrecognised event_type.
    Raises InvalidAllocationPayload if payload violates the closed shape.
    Raises OSError on write failure (from atomic_append_line).
    """
    if event_type not in KNOWN_EVENT_TYPES:
        raise UnknownAllocationEventType(
            f"{event_type!r} is not a known allocation event type"
        )
    _validate_payload(event_type, payload)

    body: dict[str, Any] = {
        "event_id":          str(uuid.uuid4()),
        "ts_monotonic_ns":   ts_monotonic_ns,
        "ts_wall":           ts_wall,
        "event_type":        event_type,
        "payload":           payload,
        "prev_event_sha256": prev_sha256,
    }
    sha = _event_sha256(body)
    record = {**body, "event_sha256": sha}

    atomic_append_line(str(path), json.dumps(record, sort_keys=True, separators=(",", ":")))
    return sha


def read_events(path: Path) -> list[dict[str, Any]]:
    """Read all events in append order.

    Returns empty list when the file does not exist.
    Raises AllocationTruncatedError if the final line is not valid JSON.
    Raises AllocationChainError if any non-final line is not valid JSON.
    """
    if not path.exists():
        return []

    raw = [l for l in path.read_text(encoding="utf-8").splitlines() if l.strip()]
    if not raw:
        return []

    events: list[dict[str, Any]] = []
    for i, line in enumerate(raw):
        is_last = (i == len(raw) - 1)
        try:
            events.append(json.loads(line))
        except json.JSONDecodeError as exc:
            if is_last:
                raise AllocationTruncatedError(
                    f"line {i} is not valid JSON — likely truncated by a crash"
                ) from exc
            raise AllocationChainError(
                f"line {i} is not valid JSON — log is corrupted"
            ) from exc

    return events


def verify_chain(events: list[dict[str, Any]]) -> None:
    """Verify every event including the tail.

    Raises AllocationChainError on the first integrity violation.
    An empty list passes trivially.
    """
    if not events:
        return

    if events[0].get("prev_event_sha256") is not None:
        raise AllocationChainError(
            f"first event {events[0].get('event_id')!r} "
            "must have prev_event_sha256=null"
        )

    for i, event in enumerate(events):
        stored = event.get("event_sha256")
        body   = {k: v for k, v in event.items() if k != "event_sha256"}
        if _event_sha256(body) != stored:
            raise AllocationChainError(
                f"event {event.get('event_id')!r} (index {i}): "
                "event_sha256 mismatch — event has been tampered"
            )
        if i > 0:
            expected = events[i - 1]["event_sha256"]
            if event.get("prev_event_sha256") != expected:
                raise AllocationChainError(
                    f"event {event.get('event_id')!r} (index {i}): "
                    "prev_event_sha256 mismatch"
                )


def active_vms(events: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Return the payloads of all VMs that have been allocated but not yet terminated.

    Used by the sweeper to identify candidates for orphan detection.
    """
    allocated: dict[str, dict[str, Any]] = {}
    for event in events:
        if event["event_type"] == "vm_allocated":
            iid = event["payload"]["instance_id"]
            allocated[iid] = event["payload"]
        elif event["event_type"] == "vm_terminated":
            iid = event["payload"]["instance_id"]
            allocated.pop(iid, None)
    return list(allocated.values())


def active_s3_objects(events: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Return the payloads of all S3 objects created but not yet deleted.

    The (key, version_id) pair is the unique identifier per ADR-005.
    Used by the sweeper for orphan detection and cleanup gating.
    """
    created: dict[tuple[str, str], dict[str, Any]] = {}
    for event in events:
        if event["event_type"] == "s3_object_created":
            p = event["payload"]
            created[(p["key"], p["version_id"])] = p
        elif event["event_type"] == "s3_object_deleted":
            p = event["payload"]
            created.pop((p["key"], p["version_id"]), None)
    return list(created.values())