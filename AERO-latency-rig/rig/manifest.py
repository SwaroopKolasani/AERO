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
from dataclasses import dataclass, field
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




# =============================================================================
# T1.2 -- session manifest projection, write, and reconciliation
# =============================================================================

from rig.atomic import atomic_write as _atomic_write  # noqa: E402


# ── named reconciliation error codes (complete closed set) ────────────────────

REC_MANIFEST_MISSING          = "manifest_missing"
REC_EVENT_LOG_MISSING         = "event_log_missing"
REC_EVENT_LOG_TRUNCATED       = "event_log_truncated"
REC_EVENT_CHAIN_BROKEN        = "event_chain_broken"
REC_MANIFEST_EVENTS_MISMATCH  = "manifest_events_mismatch"
REC_ARTIFACT_MISSING          = "artifact_missing"
REC_ARTIFACT_HASH_MISMATCH    = "artifact_hash_mismatch"
REC_DISK_ORPHAN               = "disk_orphan"

# Files always present in a session directory that are not tracked artifacts.
# Everything else found on disk must appear in the manifest projection.
_OPERATIONAL_FILENAMES: frozenset[str] = frozenset({
    "session_manifest.json",
    "manifest_events.jsonl",
})


@dataclass
class Finding:
    """One named issue found during reconciliation."""
    error_code: str
    detail: dict


@dataclass
class ReconciliationReport:
    """Structured reconciliation result.

    ok=True only when findings is empty.
    error_code mirrors findings[0].error_code for callers that need a single
    primary code; findings carries the full list for structured audit use.
    Structural failures (manifest missing, log truncated, chain broken, etc.)
    always produce exactly one finding and return immediately because later
    checks cannot proceed without a valid foundation.
    Artifact-level checks collect all issues before returning.
    """
    ok: bool
    error_code: "str | None" = None
    findings: list = field(default_factory=list)  # list[Finding]

    def as_dict(self) -> dict:
        return {
            "schema_version": 1,
            "ok": self.ok,
            "error_code": self.error_code,
            "findings": [
                {"error_code": f.error_code, "detail": f.detail}
                for f in self.findings
            ],
        }


# ── helpers ───────────────────────────────────────────────────────────────────

def _sha256_file(path: Path) -> str:
    """SHA-256 of a file's full contents."""
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


def _scan_session_files(session_dir: Path) -> set[str]:
    """Return absolute paths of all non-operational files in the session tree.

    Excludes session_manifest.json and manifest_events.jsonl (operational).
    Excludes atomic-write temp files (contain '.tmp-' in their name).
    """
    found: set[str] = set()
    for p in session_dir.rglob("*"):
        if not p.is_file():
            continue
        if p.name in _OPERATIONAL_FILENAMES:
            continue
        if ".tmp-" in p.name:
            continue
        found.add(str(p))
    return found


def _fail_structural(code: str, detail: "dict | None" = None) -> ReconciliationReport:
    """Produce a fail-fast structural finding and return immediately."""
    f = Finding(error_code=code, detail=detail or {})
    return ReconciliationReport(ok=False, error_code=code, findings=[f])


# ── projection ────────────────────────────────────────────────────────────────

def project_manifest(events: list) -> dict:
    """Build session_manifest.json content from an ordered event list.

    Pure function: same events in the same order produce the same result.
    closure_ref is null until a session_closed event is processed.

    Assumptions (surfaced):
    - run_count_expected/observed are inferred from artifact_created /
      artifact_verified events where artifact_type == "run_record".
    - artifact_verified events with ok=False are not added to
      observed_artifacts.
    """
    proj: dict = {
        "schema_version": 1,
        "session_id": "",
        "rig_version": "",
        "rig_self_sha256": "",
        "git_commit": "",
        "state": "OPEN",
        "expected_artifacts": [],
        "observed_artifacts": [],
        "run_count_expected": 0,
        "run_count_observed": 0,
        "opened_at": "",
        "last_event_id": "",
        "closure_ref": None,
    }

    for event in events:
        etype = event.get("event_type", "")
        payload = event.get("payload", {})
        proj["last_event_id"] = event.get("event_id", "")

        if etype == "session_opened":
            proj["session_id"]      = payload.get("session_id", "")
            proj["rig_version"]     = payload.get("rig_version", "")
            proj["rig_self_sha256"] = payload.get("rig_self_sha256", "")
            proj["git_commit"]      = payload.get("git_commit", "")
            proj["opened_at"]       = event.get("ts_wall", "")
            proj["state"]           = "OPEN"

        elif etype == "state_transition":
            proj["state"] = payload.get("to_state", proj["state"])

        elif etype == "artifact_created":
            proj["expected_artifacts"].append({
                "artifact_type":          payload.get("artifact_type"),
                "path":                   payload.get("path"),
                "sha256":                 payload.get("sha256"),
                "artifact_schema_version": payload.get("artifact_schema_version"),
            })
            if payload.get("artifact_type") == "run_record":
                proj["run_count_expected"] += 1

        elif etype == "artifact_verified":
            if payload.get("ok") is True:
                # artifact_verified does not carry artifact_schema_version;
                # look it up from the corresponding expected_artifact entry.
                schema_ver = None
                for exp in proj["expected_artifacts"]:
                    if exp["path"] == payload.get("path"):
                        schema_ver = exp.get("artifact_schema_version")
                        break
                proj["observed_artifacts"].append({
                    "path":           payload.get("path"),
                    "sha256":         payload.get("observed_sha256"),
                    "schema_version": schema_ver,
                })
                if payload.get("artifact_type") == "run_record":
                    proj["run_count_observed"] += 1

        elif etype == "session_finalizing":
            proj["state"] = "FINALIZING"

        elif etype == "session_closed":
            proj["state"]       = "CLOSED"
            proj["closure_ref"] = payload.get("closure_sha256")

        elif etype == "session_invalidated":
            proj["state"] = "INVALID"

        elif etype == "session_incomplete":
            proj["state"] = "INCOMPLETE"

        elif etype == "state_transition_illegal":
            proj["state"] = "INVALID"

    return proj


# ── orchestrator write path ───────────────────────────────────────────────────

def write_manifest(session_dir: Path) -> None:
    """Atomically write session_manifest.json from the current event log.

    This is the only permitted code path that writes session_manifest.json.
    The orchestrator calls this after each event batch.
    """
    events = read_events(session_dir / "manifest_events.jsonl")
    proj   = project_manifest(events)
    _atomic_write(
        str(session_dir / "session_manifest.json"),
        json.dumps(proj, indent=2),
    )


# ── manifest vs events comparison ────────────────────────────────────────────

def _manifests_agree(stored: dict, projected: dict) -> "tuple[bool, str]":
    """Compare key fields of stored vs projected manifest.

    Returns (True, "") if they agree.
    Returns (False, field_name) on first disagreement.
    """
    scalar_fields = (
        "session_id", "state", "last_event_id", "closure_ref",
        "run_count_expected", "run_count_observed",
    )
    for f in scalar_fields:
        if stored.get(f) != projected.get(f):
            return False, f

    stored_exp = {(a["path"], a["sha256"]) for a in stored.get("expected_artifacts", [])}
    proj_exp   = {(a["path"], a["sha256"]) for a in projected.get("expected_artifacts", [])}
    if stored_exp != proj_exp:
        return False, "expected_artifacts"

    stored_obs = {(a["path"], a["sha256"]) for a in stored.get("observed_artifacts", [])}
    proj_obs   = {(a["path"], a["sha256"]) for a in projected.get("observed_artifacts", [])}
    if stored_obs != proj_obs:
        return False, "observed_artifacts"

    return True, ""


# ── reconcile ─────────────────────────────────────────────────────────────────

def reconcile(session_dir: Path) -> ReconciliationReport:
    """Verify manifest <-> disk <-> events triple consistency.

    Structural checks fail-fast (a missing or corrupt foundation makes
    further checks impossible).  Artifact-level checks collect all findings.

    Structural (fail-fast):
        manifest_missing          session_manifest.json absent
        event_log_missing         manifest_events.jsonl absent
        event_log_truncated       last line not valid JSON (crash signal)
        event_chain_broken        hash chain integrity failure
        manifest_events_mismatch  stored manifest diverges from projection

    Artifact (all collected):
        artifact_missing          observed artifact absent from disk
        artifact_hash_mismatch    observed artifact hash differs on disk
        disk_orphan               file on disk absent from manifest projection
    """
    manifest_path = session_dir / "session_manifest.json"
    events_path   = session_dir / "manifest_events.jsonl"

    if not manifest_path.exists():
        return _fail_structural(REC_MANIFEST_MISSING)

    if not events_path.exists():
        return _fail_structural(REC_EVENT_LOG_MISSING)

    try:
        events = read_events(events_path)
    except TruncatedLogError:
        return _fail_structural(REC_EVENT_LOG_TRUNCATED)
    except ChainError:
        return _fail_structural(REC_EVENT_CHAIN_BROKEN)

    try:
        verify_chain(events)
    except ChainError:
        return _fail_structural(REC_EVENT_CHAIN_BROKEN)

    stored    = json.loads(manifest_path.read_text(encoding="utf-8"))
    projected = project_manifest(events)

    agrees, mismatch_field = _manifests_agree(stored, projected)
    if not agrees:
        return _fail_structural(
            REC_MANIFEST_EVENTS_MISMATCH,
            {"field": mismatch_field},
        )

    findings: list[Finding] = []

    # -- artifact-level checks (collect all) --

    for artifact in stored.get("observed_artifacts", []):
        p = Path(artifact["path"])
        if not p.exists():
            findings.append(Finding(REC_ARTIFACT_MISSING, {"path": artifact["path"]}))
        elif _sha256_file(p) != artifact["sha256"]:
            findings.append(Finding(REC_ARTIFACT_HASH_MISMATCH, {"path": artifact["path"]}))

    # Disk orphan: file on disk NOT in either artifact list.
    tracked = (
        {a["path"] for a in stored.get("expected_artifacts", [])}
        | {a["path"] for a in stored.get("observed_artifacts", [])}
    )
    for disk_path in _scan_session_files(session_dir):
        if disk_path not in tracked:
            findings.append(Finding(REC_DISK_ORPHAN, {"path": disk_path}))

    if findings:
        return ReconciliationReport(
            ok=False,
            error_code=findings[0].error_code,
            findings=findings,
        )

    return ReconciliationReport(ok=True)