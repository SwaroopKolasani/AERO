"""
rig/manifest.py — manifest event log (T1.1).

manifest_events.jsonl is the ground-truth append-only record for a session.
Each line is one self-authenticating JSON event.  Events form a hash chain:
every event stores both its own SHA-256 (event_sha256) and the SHA-256 of
its predecessor (prev_event_sha256), so any tampering — including to the
tail event — is detectable on read.

Event schema (7 fields per record):
    event_id            UUID4 string
    ts_monotonic_ns     CLOCK_MONOTONIC nanoseconds (int)
    ts_wall             ISO 8601 wall-clock string
    event_type          one of KNOWN_EVENT_TYPES (closed)
    payload             type-specific object (closed per-type shape)
    prev_event_sha256   hex64 string | null (null for first event)
    event_sha256        hex64 string (SHA-256 of the other six fields)

Closed event-type set and their exact payload shapes are defined below and
sourced from the confirmed T1.1 payload specification document.

All writes go through rig.atomic.atomic_append_line — never directly.
T1.2 (projection writer), T1.3 (lock), T1.4 (state validator) are later tasks.
"""

import hashlib
import json
import uuid
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from rig.atomic import atomic_append_line


# ── closed vocabularies ───────────────────────────────────────────────────────

KNOWN_EVENT_TYPES: frozenset[str] = frozenset({
    "session_opened",
    "state_transition",
    "artifact_created",
    "artifact_verified",
    "session_finalizing",
    "session_closed",
    "session_invalidated",
    "session_incomplete",
    "lock_adopted",
    "state_transition_illegal",
})

# Closed enum: verdict values (must match decision_report.schema.json).
_VERDICT_VALUES: frozenset[str] = frozenset({
    "GO", "GO_WITH_NARROWING", "INCONCLUSIVE",
    "REDESIGN", "STOP", "INVALID", "INCOMPLETE",
})

# Closed enum: causal invalidation codes (§6/§8 of the architecture).
_INVALIDATION_REASON_CODES: frozenset[str] = frozenset({
    "host_suitability_breach",
    "network_floor_breach",
    "thermal_event",
    "timer_sanity_breach",
    "fixture_drift",
    "aws_api_hard_failure",
    "state_transition_illegal",
    "cloud_weather_drift",
})

# Closed enum: crash classification codes (T7.6).
_CRASH_CLASSIFICATIONS: frozenset[str] = frozenset({
    "crash_in_preflight",
    "crash_in_run",
    "crash_in_finalizing",
    "crash_in_closure",
})

# Required (= allowed) payload fields per event type.
# All fields are required; none are optional.  Extra keys are rejected.
# Source: confirmed T1.1 payload specification.
_PAYLOAD_FIELDS: dict[str, frozenset[str]] = {
    "session_opened":           frozenset({"session_id", "rig_version",
                                           "rig_self_sha256", "git_commit"}),
    "state_transition":         frozenset({"from_state", "to_state"}),
    "artifact_created":         frozenset({"artifact_type", "path",
                                           "sha256", "artifact_schema_version"}),
    "artifact_verified":        frozenset({"artifact_type", "path",
                                           "expected_sha256", "observed_sha256", "ok"}),
    "session_finalizing":       frozenset({"completeness_sha256",
                                           "reconciliation_sha256"}),
    "session_closed":           frozenset({"verdict", "decision_report_sha256",
                                           "closure_sha256"}),
    "session_invalidated":      frozenset({"reason_codes",
                                           "invalidation_artifact_sha256"}),
    "session_incomplete":       frozenset({"crash_classification",
                                           "last_completed_run_ordinal"}),
    "lock_adopted":             frozenset({"lock_path", "old_lock"}),
    "state_transition_illegal": frozenset({"from_state", "attempted_to_state"}),
}

# Required fields inside lock_adopted.old_lock (from T1.3 lock file shape).
_OLD_LOCK_FIELDS: frozenset[str] = frozenset({
    "pid", "start_monotonic", "rig_self_sha256", "hostname_hash"
})


# ── exceptions ────────────────────────────────────────────────────────────────

class ChainError(Exception):
    """Raised when hash-chain integrity check fails."""


class TruncatedLogError(Exception):
    """Raised when the last line of the log is not valid JSON (crash signal).

    The caller should treat the session as INCOMPLETE-eligible.
    The partial line is not returned; the events before it are not read
    either, because the caller cannot safely use a partial read.
    """


class UnknownEventTypeError(ValueError):
    """Raised when event_type is not in the closed set."""


class InvalidPayloadError(ValueError):
    """Raised when a payload violates the closed per-type shape."""


# ── internal ──────────────────────────────────────────────────────────────────

def _canonical(obj: dict[str, Any]) -> bytes:
    return json.dumps(obj, sort_keys=True, separators=(",", ":")).encode()


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _compute_event_sha256(body: dict[str, Any]) -> str:
    """SHA-256 of the six body fields (everything except event_sha256)."""
    return _sha256(_canonical(body))


def _require_exact(payload: dict[str, Any],
                   fields: frozenset[str],
                   context: str) -> None:
    """Check payload has exactly the declared fields — no extras, no missing."""
    extra = set(payload) - fields
    if extra:
        raise InvalidPayloadError(
            f"{context}: unexpected payload fields: {sorted(extra)}"
        )
    missing = fields - set(payload)
    if missing:
        raise InvalidPayloadError(
            f"{context}: missing payload fields: {sorted(missing)}"
        )


def _validate_payload(event_type: str, payload: dict[str, Any]) -> None:
    """Dispatch to the per-type closed-shape validator."""
    _PAYLOAD_VALIDATORS[event_type](payload)


# Per-type validators — each function checks exactly what the spec mandates.

def _v_session_opened(p: dict[str, Any]) -> None:
    _require_exact(p, _PAYLOAD_FIELDS["session_opened"], "session_opened")


def _v_state_transition(p: dict[str, Any]) -> None:
    _require_exact(p, _PAYLOAD_FIELDS["state_transition"], "state_transition")


def _v_artifact_created(p: dict[str, Any]) -> None:
    _require_exact(p, _PAYLOAD_FIELDS["artifact_created"], "artifact_created")
    # artifact_type must match a registered schema key at write time.
    from rig.schemas import _HERE as _SCHEMA_DIR  # noqa: PLC0415
    if not (_SCHEMA_DIR / f"{p['artifact_type']}.schema.json").is_file():
        raise InvalidPayloadError(
            f"artifact_created: artifact_type {p['artifact_type']!r} "
            "has no registered schema"
        )


def _v_artifact_verified(p: dict[str, Any]) -> None:
    _require_exact(p, _PAYLOAD_FIELDS["artifact_verified"], "artifact_verified")
    if not isinstance(p["ok"], bool):
        raise InvalidPayloadError("artifact_verified: 'ok' must be a boolean")


def _v_session_finalizing(p: dict[str, Any]) -> None:
    _require_exact(p, _PAYLOAD_FIELDS["session_finalizing"], "session_finalizing")


def _v_session_closed(p: dict[str, Any]) -> None:
    _require_exact(p, _PAYLOAD_FIELDS["session_closed"], "session_closed")
    if p["verdict"] not in _VERDICT_VALUES:
        raise InvalidPayloadError(
            f"session_closed: verdict {p['verdict']!r} is not in the closed enum"
        )


def _v_session_invalidated(p: dict[str, Any]) -> None:
    _require_exact(p, _PAYLOAD_FIELDS["session_invalidated"], "session_invalidated")
    codes = p["reason_codes"]
    if not isinstance(codes, list) or len(codes) == 0:
        raise InvalidPayloadError(
            "session_invalidated: reason_codes must be a non-empty list"
        )
    unknown = [c for c in codes if c not in _INVALIDATION_REASON_CODES]
    if unknown:
        raise InvalidPayloadError(
            f"session_invalidated: unknown reason codes: {unknown}"
        )


def _v_session_incomplete(p: dict[str, Any]) -> None:
    _require_exact(p, _PAYLOAD_FIELDS["session_incomplete"], "session_incomplete")
    if p["crash_classification"] not in _CRASH_CLASSIFICATIONS:
        raise InvalidPayloadError(
            f"session_incomplete: crash_classification "
            f"{p['crash_classification']!r} is not in the closed enum"
        )
    ordinal = p["last_completed_run_ordinal"]
    if ordinal is not None and not isinstance(ordinal, int):
        raise InvalidPayloadError(
            "session_incomplete: last_completed_run_ordinal must be int or null"
        )


def _v_lock_adopted(p: dict[str, Any]) -> None:
    _require_exact(p, _PAYLOAD_FIELDS["lock_adopted"], "lock_adopted")
    old = p["old_lock"]
    if not isinstance(old, dict):
        raise InvalidPayloadError("lock_adopted: old_lock must be an object")
    _require_exact(old, _OLD_LOCK_FIELDS, "lock_adopted.old_lock")


def _v_state_transition_illegal(p: dict[str, Any]) -> None:
    _require_exact(p, _PAYLOAD_FIELDS["state_transition_illegal"],
                   "state_transition_illegal")


_PAYLOAD_VALIDATORS: dict[str, Any] = {
    "session_opened":           _v_session_opened,
    "state_transition":         _v_state_transition,
    "artifact_created":         _v_artifact_created,
    "artifact_verified":        _v_artifact_verified,
    "session_finalizing":       _v_session_finalizing,
    "session_closed":           _v_session_closed,
    "session_invalidated":      _v_session_invalidated,
    "session_incomplete":       _v_session_incomplete,
    "lock_adopted":             _v_lock_adopted,
    "state_transition_illegal": _v_state_transition_illegal,
}


# ── public API ────────────────────────────────────────────────────────────────

def append_event(
    path: Path,
    event_type: str,
    payload: dict[str, Any],
    prev_sha256: "str | None",
    ts_monotonic_ns: int,
    ts_wall: str,
) -> str:
    """Append one self-authenticating event; return its event_sha256.

    The returned SHA-256 must be passed as prev_sha256 for the next call.
    Pass prev_sha256=None for the first event in a new log.

    Raises UnknownEventTypeError for unrecognised event_type.
    Raises InvalidPayloadError if payload violates the closed per-type shape.
    Raises OSError on write failure (from atomic_append_line).
    """
    if event_type not in KNOWN_EVENT_TYPES:
        raise UnknownEventTypeError(f"{event_type!r} is not a known event type")
    _validate_payload(event_type, payload)

    body: dict[str, Any] = {
        "event_id":          str(uuid.uuid4()),
        "ts_monotonic_ns":   ts_monotonic_ns,
        "ts_wall":           ts_wall,
        "event_type":        event_type,
        "payload":           payload,
        "prev_event_sha256": prev_sha256,
    }
    sha = _compute_event_sha256(body)
    record = {**body, "event_sha256": sha}

    atomic_append_line(
        str(path),
        json.dumps(record, sort_keys=True, separators=(",", ":")),
    )
    return sha


def read_events(path: Path) -> list[dict[str, Any]]:
    """Read all events in append order.

    Returns an empty list when the file does not exist.

    Raises TruncatedLogError if the final line is not valid JSON — the
    expected crash-write signal.  The truncated line is not returned.

    Raises ChainError if any non-final line is not valid JSON — corruption.
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
                raise TruncatedLogError(
                    f"line {i} is not valid JSON — likely truncated by a crash"
                ) from exc
            raise ChainError(
                f"line {i} is not valid JSON — log is corrupted"
            ) from exc

    return events


def verify_chain(events: list[dict[str, Any]]) -> None:
    """Verify every event including the tail.

    For each event: recompute SHA-256 of its six body fields; compare to
    stored event_sha256.  For each event after the first: check
    prev_event_sha256 equals the preceding event's event_sha256.
    The first event must have prev_event_sha256 = null.

    Raises ChainError on any violation, naming the event_id.
    """
    if not events:
        return

    if events[0].get("prev_event_sha256") is not None:
        raise ChainError(
            f"first event {events[0].get('event_id')!r} "
            "must have prev_event_sha256=null"
        )

    for i, event in enumerate(events):
        stored = event.get("event_sha256")
        body = {k: v for k, v in event.items() if k != "event_sha256"}
        computed = _compute_event_sha256(body)
        if computed != stored:
            raise ChainError(
                f"event {event.get('event_id')!r} (index {i}): "
                "event_sha256 mismatch — event has been tampered"
            )
        if i > 0:
            expected_prev = events[i - 1]["event_sha256"]
            if event.get("prev_event_sha256") != expected_prev:
                raise ChainError(
                    f"event {event.get('event_id')!r} (index {i}): "
                    f"prev_event_sha256 mismatch"
                )


# ── minimal chain projection ──────────────────────────────────────────────────

@dataclass
class ChainProjection:
    """Chain-tip state derived from replaying any prefix of the event log.

    Only chain-level fields.  Session state and artifact lists belong to T1.2.
    """
    events_count: int = 0
    last_event_sha256: str = ""  # pass as prev_sha256 to continue the chain


def replay(events: list[dict[str, Any]]) -> ChainProjection:
    """Return the minimal chain-tip projection from any prefix of the log.

    Pure function: same input → same result.  Does not read from disk.
    """
    proj = ChainProjection()
    for event in events:
        proj.events_count += 1
        proj.last_event_sha256 = event.get("event_sha256", "")
    return proj