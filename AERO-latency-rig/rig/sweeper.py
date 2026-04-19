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


# =============================================================================
# T3.4 -- Sweep correlation
# =============================================================================
#
# Inputs:
#   allocation_registry entries      (from T3.1)
#   normalized inventory snapshots   (pre, post, delayed -- from T3.2)
#
# Known set: registry entries where trust_level is "authoritative" or
#   "observed".  Inferred entries (sweep sources) do not count -- that would
#   make the sweep self-validating.
#
# Classification rules (closed set, confirmed spec):
#
#   correlated    resource_id in known set
#   pre_existing  resource_id in inventory_pre but NOT in known set
#                 (existed before this session; not an orphan)
#   orphan        resource_id appears in post or delayed, NOT in pre, NOT in
#                 known set -- appeared during/after session with no creation
#                 record. Any orphan → gate_failed=True.
#   anomaly       resource_id in known set but in unexpected cloud state
#                 (e.g. stopped EC2).  Triggers gate failure same as orphan.
#   vanished      resource_id in known set but absent from all inventory
#                 snapshots -- expected for short-lived instances; recorded
#                 for completeness, not flagged as an error.
#
#   delete_markers_observed  S3 delete markers: recorded, never correlated or
#                            orphaned.  Excluded from all classification logic.
#
# Gate failure (evaluator T7.3 reads this output and applies the rule):
#   orphan_* categories → instant gate failure (zero tolerance, ADR-006)
#   cloud_state_anomaly → reported in anomalies[], surfaced in cause_bundle,
#                         NOT an automatic gate failure (ADR-005: never auto-acted on)
# This correlator does not emit a gate_failed verdict -- that belongs to T7.3.
#
# The inputs dict records SHA-256 of each input artifact so the correlation
# report is self-describing and auditable.

import hashlib as _hashlib  # aliased to avoid shadowing the module-level name

_TRUST_LEVELS_FOR_KNOWN_SET: frozenset[str] = frozenset({
    "authoritative",
    "observed",
})

# ADR-005 orphan categories.
ORPHAN_VM_KNOWN                    = "orphan_vm_known"
ORPHAN_S3_RECORDED_CURRENT_VERSION = "orphan_s3_recorded_current_version"

# Anomaly type.
CLOUD_STATE_ANOMALY = "cloud_state_anomaly"

# EC2 states that a session-created instance is expected to be in.
_EXPECTED_EC2_STATES: frozenset[str] = frozenset({"running", "pending"})


def _file_sha256(path: Path) -> str:
    h = _hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


def _known_resource_ids(registry_entries: list[dict[str, Any]]) -> set[str]:
    """Return resource_ids from authoritative or observed registry entries only."""
    return {
        e["resource_id"]
        for e in registry_entries
        if e.get("trust_level") in _TRUST_LEVELS_FOR_KNOWN_SET
    }


def _s3_composite_id(bucket: str, key: str, version_id: str) -> str:
    """Build S3 composite resource_id matching T3.1's allocation entry format."""
    return f"{bucket}/{key}@{version_id}"


def _partition_s3(snap: dict[str, Any], bucket: str) -> tuple[
    list[dict[str, Any]],  # regular versions (is_delete_marker=False)
    list[dict[str, Any]],  # delete markers
]:
    """Split S3 inventory entries into regular versions and delete markers."""
    versions:       list[dict[str, Any]] = []
    delete_markers: list[dict[str, Any]] = []
    for ver in snap.get("s3_object_versions", []):
        cid = _s3_composite_id(bucket, ver["key"], ver["version_id"])
        entry = {"resource_id": cid, "key": ver["key"], "version_id": ver["version_id"]}
        if ver.get("is_delete_marker", False):
            delete_markers.append(entry)
        else:
            versions.append(entry)
    return versions, delete_markers


def _collect_by_phase(
    pre: dict[str, Any],
    post: dict[str, Any],
    delayed: dict[str, Any],
) -> tuple[
    dict[str, str],   # ec2: instance_id -> last-seen state across post+delayed
    dict[str, str],   # ec2: instance_id -> last-seen state in pre only
    dict[str, str],   # s3: composite_id -> key (post+delayed non-DM versions)
    dict[str, str],   # s3: composite_id -> key (pre non-DM versions)
    list[dict],       # all delete markers across all phases
]:
    # EC2 seen in pre (instance_id -> state)
    ec2_pre: dict[str, str] = {
        inst["instance_id"]: inst.get("state", "")
        for inst in pre.get("ec2_instances", [])
    }
    # EC2 seen in post or delayed (last-seen state wins)
    ec2_post_delayed: dict[str, str] = {}
    for snap in (post, delayed):
        for inst in snap.get("ec2_instances", []):
            ec2_post_delayed[inst["instance_id"]] = inst.get("state", "")

    # S3 (non-delete-marker versions) and delete markers
    pre_bucket     = pre.get("s3_bucket", "")
    post_bucket    = post.get("s3_bucket", "")
    delayed_bucket = delayed.get("s3_bucket", "")

    s3_pre_versions, dm_pre   = _partition_s3(pre, pre_bucket)
    s3_post_versions, dm_post = _partition_s3(post, post_bucket)
    s3_del_versions, dm_del   = _partition_s3(delayed, delayed_bucket)

    s3_pre: dict[str, str] = {v["resource_id"]: v["key"] for v in s3_pre_versions}
    s3_post_delayed: dict[str, str] = {}
    for v in s3_post_versions + s3_del_versions:
        s3_post_delayed[v["resource_id"]] = v["key"]

    all_delete_markers = dm_pre + dm_post + dm_del

    return ec2_post_delayed, ec2_pre, s3_post_delayed, s3_pre, all_delete_markers


def correlate(
    registry_entries: list[dict[str, Any]],
    pre:     dict[str, Any],
    post:    dict[str, Any],
    delayed: dict[str, Any],
    registry_sha256: str = "",
    pre_sha256:      str = "",
    post_sha256:     str = "",
    delayed_sha256:  str = "",
) -> dict[str, Any]:
    """Run sweep correlation; return the correlation result dict.

    Args:
        registry_entries: all entries from allocation_registry.jsonl
        pre, post, delayed: normalized inventory snapshot dicts
        *_sha256: optional SHA-256 of each input file for the inputs record

    Returns a dict matching sweep_correlation.schema.json.
    Gate logic: gate_failed=True if any orphan OR any anomaly is found.
    """
    known = _known_resource_ids(registry_entries)

    ec2_post_del, ec2_pre, s3_post_del, s3_pre, delete_markers = \
        _collect_by_phase(pre, post, delayed)

    # All resource_ids seen in any inventory snapshot (EC2 + S3 objects).
    all_ec2 = dict(ec2_pre)
    all_ec2.update(ec2_post_del)   # post/delayed state overwrites pre if same ID
    all_s3  = dict(s3_pre)
    all_s3.update(s3_post_del)

    correlated:  list[dict] = []
    pre_existing: list[dict] = []
    orphans:     list[dict] = []
    anomalies:   list[dict] = []
    vanished:    list[dict] = []

    # -- EC2 correlation --

    for iid, state in all_ec2.items():
        if iid in known:
            correlated.append({"resource_id": iid, "resource_type": "ec2_instance"})
            if state not in _EXPECTED_EC2_STATES:
                anomalies.append({
                    "resource_id":  iid,
                    "resource_type": "ec2_instance",
                    "anomaly_type": CLOUD_STATE_ANOMALY,
                    "detail":       f"state={state!r}",
                })
        elif iid in ec2_pre:
            # In pre but not in known set → existed before the session.
            pre_existing.append({"resource_id": iid, "resource_type": "ec2_instance"})
        else:
            # Appeared in post/delayed but not pre and not known → orphan.
            orphans.append({
                "resource_id":     iid,
                "resource_type":   "ec2_instance",
                "orphan_category": ORPHAN_VM_KNOWN,
            })

    # -- S3 correlation --

    for cid in all_s3:
        if cid in known:
            correlated.append({"resource_id": cid, "resource_type": "s3_object_version"})
        elif cid in s3_pre:
            pre_existing.append({"resource_id": cid, "resource_type": "s3_object_version"})
        else:
            orphans.append({
                "resource_id":     cid,
                "resource_type":   "s3_object_version",
                "orphan_category": ORPHAN_S3_RECORDED_CURRENT_VERSION,
            })

    # -- Vanished: in known set but absent from all inventory snapshots --

    seen_all_ids = set(all_ec2) | set(all_s3)
    for entry in registry_entries:
        if entry.get("trust_level") not in _TRUST_LEVELS_FOR_KNOWN_SET:
            continue
        rid = entry["resource_id"]
        if rid not in seen_all_ids:
            vanished.append({
                "resource_id":  rid,
                "resource_type": entry.get("resource_type", ""),
            })

    result: dict[str, Any] = {
        "schema_version":          1,
        "correlated":              correlated,
        "pre_existing":            pre_existing,
        "orphans":                 orphans,
        "anomalies":               anomalies,
        "vanished":                vanished,
        "delete_markers_observed": delete_markers,
        "inputs": {
            "registry_sha256": registry_sha256,
            "pre_sha256":      pre_sha256,
            "post_sha256":     post_sha256,
            "delayed_sha256":  delayed_sha256,
        },
    }
    validate("sweep_correlation", result)
    return result


def write_correlation(result: dict[str, Any], output_path: Path) -> None:
    """Write correlation.json atomically."""
    atomic_write(str(output_path), json.dumps(result, indent=2).encode())