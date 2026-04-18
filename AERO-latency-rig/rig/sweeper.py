"""
rig/sweeper.py -- AWS inventory snapshots (T3.2).

T3.2 produces three point-in-time inventory snapshot pairs:

    inventory_pre      at preflight
    inventory_post     at postflight
    inventory_delayed  after cooldown window (default 300 s)

The sweep correlator (T3.4) consumes four inputs total:
    1. allocation_registry.jsonl     -- from T3.1 (the "during" observation)
    2. inventory_pre.json            -- from T3.2
    3. inventory_post.json           -- from T3.2
    4. inventory_delayed.json        -- from T3.2

The "four-phase sweep" described in the architecture refers to these four
inputs.  T3.2 produces three of them.  The fourth is the allocation registry
(an append-only log of every resource observed during the session), which is
a structurally different artifact built by T3.1.  There is no
inventory_during_registry artifact -- "during" is the allocation registry.

Phase publication model ("written together" guarantee)
-------------------------------------------------------
Each phase writes three artifacts in this order:
    1. <phase>_raw.jsonl   -- all boto3 pages in one atomic write
    2. <phase>.json        -- normalized snapshot, one atomic write
    3. <phase>.complete    -- completion marker, one atomic write (last)

A phase pair is committed iff <phase>.complete exists.  Raw or normalized
present without a marker means the phase crashed between writes and the
pair is non-authoritative.  T7.6 may re-run the phase on crash recovery.

Delayed sweep interface (T7.6 call site)
-----------------------------------------
run_delayed_sweep() is the public function T7.6 calls.  It wraps
take_inventory(PHASE_DELAYED, ...) and returns the three artifact paths.
T7.6 passes skip_sleep=True when the cooldown has already elapsed by the
time crash recovery runs.

T3.2 does not implement crash detection, event log replay, or session state
inspection.  T7.6 owns those.  T3.2's job is to provide a callable that
produces valid delayed inventory artifacts on both normal and recovery paths.

Scope:
    EC2 -- describe_instances in the configured region, bounded by IAM.
           No cross-region; no account-wide sweep.
    S3  -- list_object_versions on the named staging bucket only.

No retry logic.  No parallelism.  All writes go through rig.atomic.
"""

import json
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from rig.atomic import atomic_append_line, atomic_write
from rig.schemas import validate

PHASE_PRE     = "inventory_pre"
PHASE_POST    = "inventory_post"
PHASE_DELAYED = "inventory_delayed"

KNOWN_PHASES: frozenset[str] = frozenset({PHASE_PRE, PHASE_POST, PHASE_DELAYED})

DEFAULT_COOLDOWN_S = 300


# -- phase completion check ---------------------------------------------------

def phase_is_complete(output_dir: Path, phase: str) -> bool:
    """Return True if the phase pair is committed (completion marker exists).

    False means either the phase has not run, or it crashed between writes
    and the pair is non-authoritative.
    """
    return (output_dir / f"{phase}.complete").exists()


def run_delayed_sweep(
    output_dir: Path,
    ec2_client: Any,
    s3_client: Any,
    s3_bucket: str,
    cooldown_s: int = DEFAULT_COOLDOWN_S,
    skip_sleep: bool = False,
) -> tuple[Path, Path, Path]:
    """Run the delayed inventory phase; return (raw_path, norm_path, marker_path).

    This is the named public entry point for T7.6 (crash-recovery adopter).

    Normal session path:   run_delayed_sweep(..., skip_sleep=False)
                           sleeps for cooldown_s before querying AWS.

    Crash-recovery path:   run_delayed_sweep(..., skip_sleep=True)
                           skips the sleep (cooldown elapsed before crash);
                           called by T7.6 before sealing the session INCOMPLETE.

    T3.2 does not detect crashes, replay event logs, or check session state.
    T7.6 decides when to call this function and which path to use.
    """
    take_inventory(PHASE_DELAYED, output_dir, ec2_client, s3_client,
                   s3_bucket, cooldown_s=cooldown_s, skip_sleep=skip_sleep)
    return (
        output_dir / "inventory_delayed_raw.jsonl",
        output_dir / "inventory_delayed.json",
        output_dir / "inventory_delayed.complete",
    )


# -- EC2 inventory ------------------------------------------------------------

def _describe_ec2_pages(ec2_client: Any) -> list[dict[str, Any]]:
    """Collect all DescribeInstances pages into memory before writing anything.

    No retry: if a page call fails, the exception propagates and neither
    artifact is written.
    """
    pages: list[dict[str, Any]] = []
    for page in ec2_client.get_paginator("describe_instances").paginate():
        pages.append(page)
    return pages


def _normalize_ec2(pages: list[dict[str, Any]]) -> list[dict[str, Any]]:
    instances: list[dict[str, Any]] = []
    for page in pages:
        for reservation in page.get("Reservations", []):
            for raw in reservation.get("Instances", []):
                instances.append({
                    "instance_id":   raw.get("InstanceId", ""),
                    "state":         raw.get("State", {}).get("Name", ""),
                    "instance_type": raw.get("InstanceType", ""),
                    "launch_time":   _isoformat(raw.get("LaunchTime")),
                    "tags":          raw.get("Tags", []),
                })
    return instances


# -- S3 inventory -------------------------------------------------------------

def _list_s3_version_pages(s3_client: Any, bucket: str) -> list[dict[str, Any]]:
    """Collect all ListObjectVersions pages for the named bucket into memory.

    Scoped to one bucket only.  No retry.
    """
    pages: list[dict[str, Any]] = []
    for page in s3_client.get_paginator("list_object_versions").paginate(Bucket=bucket):
        pages.append(page)
    return pages


def _normalize_s3(pages: list[dict[str, Any]]) -> list[dict[str, Any]]:
    versions: list[dict[str, Any]] = []
    for page in pages:
        for v in page.get("Versions", []):
            versions.append({
                "key":              v.get("Key", ""),
                "version_id":       v.get("VersionId", ""),
                "size":             v.get("Size", 0),
                "last_modified":    _isoformat(v.get("LastModified")),
                "is_delete_marker": False,
            })
        for dm in page.get("DeleteMarkers", []):
            versions.append({
                "key":              dm.get("Key", ""),
                "version_id":       dm.get("VersionId", ""),
                "size":             0,
                "last_modified":    _isoformat(dm.get("LastModified")),
                "is_delete_marker": True,
            })
    return versions


# -- helpers ------------------------------------------------------------------

def _isoformat(value: Any) -> str:
    if isinstance(value, datetime):
        if value.tzinfo is None:
            return value.replace(tzinfo=timezone.utc).isoformat()
        return value.isoformat()
    if value is None:
        return ""
    return str(value)


def _request_ids(pages: list[dict[str, Any]]) -> list[str]:
    return [
        page["ResponseMetadata"]["RequestId"]
        for page in pages
        if page.get("ResponseMetadata", {}).get("RequestId")
    ]


def _raw_bytes(pages: list[dict[str, Any]]) -> bytes:
    """Serialize all pages as JSONL in a single bytes object.

    One object per line.  Written in one atomic_write call so the raw
    artifact is never in a partial state.
    """
    lines = [json.dumps(p, default=str, separators=(",", ":")) for p in pages]
    return ("\n".join(lines) + "\n").encode()


# -- main entry point ---------------------------------------------------------

def take_inventory(
    phase: str,
    output_dir: Path,
    ec2_client: Any,
    s3_client: Any,
    s3_bucket: str,
    cooldown_s: int = DEFAULT_COOLDOWN_S,
    skip_sleep: bool = False,
) -> dict[str, Any]:
    """Run one inventory phase and write raw + normalized + completion marker.

    Publication sequence (all via atomic_write):
        1. <phase>_raw.jsonl   -- all boto3 pages
        2. <phase>.json        -- normalized snapshot
        3. <phase>.complete    -- completion marker (pair is committed)

    Pagination happens before any write.  A boto3 error during pagination
    leaves none of the three files on disk.  A crash after step 1 or 2 but
    before step 3 leaves the phase non-authoritative; phase_is_complete()
    returns False and T7.6 may re-run.

    skip_sleep: T7.6 passes True when invoking the delayed phase on crash-
        recovery paths.  The cooldown had already elapsed (or T7.6 manages
        its own timing).  This is the T7.6 interface hook; crash-recovery
        orchestration is T7.6's responsibility, not T3.2's.

    Raises ValueError for unknown phase names.
    Raises any boto3/botocore exception if AWS calls fail (no retry).
    Returns the normalized snapshot dict.
    """
    if phase not in KNOWN_PHASES:
        raise ValueError(f"unknown inventory phase: {phase!r}")

    if phase == PHASE_DELAYED and not skip_sleep:
        time.sleep(cooldown_s)

    captured_mono = time.monotonic_ns()
    captured_wall = datetime.now(timezone.utc).isoformat()

    # Collect all pages before touching the filesystem.
    ec2_pages = _describe_ec2_pages(ec2_client)
    s3_pages  = _list_s3_version_pages(s3_client, s3_bucket)

    raw_path    = output_dir / f"{phase}_raw.jsonl"
    norm_path   = output_dir / f"{phase}.json"
    marker_path = output_dir / f"{phase}.complete"

    doc: dict[str, Any] = {
        "schema_version":      1,
        "phase":               phase,
        "captured_at_mono_ns": captured_mono,
        "captured_at_wall":    captured_wall,
        "s3_bucket":           s3_bucket,
        "ec2_instances":       _normalize_ec2(ec2_pages),
        "s3_object_versions":  _normalize_s3(s3_pages),
        "ec2_request_ids":     _request_ids(ec2_pages),
        "s3_request_ids":      _request_ids(s3_pages),
    }

    # Validate before writing so a schema violation leaves no files on disk.
    validate("inventory_snapshot", doc)

    atomic_write(str(raw_path), _raw_bytes(ec2_pages + s3_pages))
    atomic_write(str(norm_path), json.dumps(doc, indent=2).encode())
    # The marker is the commit point.  Only after this write is the pair
    # considered authoritative by phase_is_complete().
    atomic_write(str(marker_path), b"1")

    return doc


# =============================================================================
# T3.3 -- Independent HeadObject anchor
# =============================================================================
#
# record_head_object() is the sole entry point for this feature.
#
# Recorded fields (task spec):
#     version_id, etag, content_length, last_modified, request_id, ts_monotonic
# Plus the full ResponseMetadata envelope, which the architecture explicitly
# requires for all raw artifacts ("full boto3 response envelopes").
# No additional fields are added.
#
# Assumption (surfaced): dropping run_id/bucket/key means a reader cannot
# attribute a JSONL entry to a specific run without external context.
# If attribution is required by T5.3, add those fields at that point.
#
# Import isolation: this function only uses stdlib (json, time) plus
# rig.atomic (already imported at module level).  It does not import from
# rig.completion, and rig.completion must not import from this section.
# The module-level imports of sweeper.py are the complete dependency
# boundary: rig.atomic + rig.schemas only.

def record_head_object(
    s3_client: Any,
    bucket: str,
    key: str,
    version_id: str,
    output_path: Path,
) -> dict[str, Any]:
    """Call HeadObject out-of-band; append one line to head_object_raw.jsonl.

    Records exactly the six task-specified fields plus the full
    ResponseMetadata envelope (required by the architecture for raw evidence).

    A VersionId mismatch between what the WAL recorded and what this call
    observes is preserved in the record exactly as received.  T5.3 compares
    the record against WAL evidence to detect and classify the mismatch.
    T3.3 does not interpret or validate the values -- it records faithfully.

    Returns the recorded dict so callers can inspect it without re-reading.
    Raises any botocore ClientError from the HeadObject call.  No retry.
    """
    ts_mono = time.monotonic_ns()

    response = s3_client.head_object(Bucket=bucket, Key=key, VersionId=version_id)

    meta = response.get("ResponseMetadata", {})
    record: dict[str, Any] = {
        "version_id":        version_id,
        "etag":              response.get("ETag", ""),
        "content_length":    response.get("ContentLength", 0),
        "last_modified":     _isoformat(response.get("LastModified")),
        "request_id":        meta.get("RequestId", ""),
        "ts_monotonic":      ts_mono,
        "response_metadata": meta,
    }

    atomic_append_line(str(output_path), json.dumps(record, default=str, separators=(",", ":")))
    return record