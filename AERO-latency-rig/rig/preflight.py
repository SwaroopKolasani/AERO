"""
rig/preflight.py — preflight checks (T2.3 through T2.6).

T2.3  Timer sanity: monotonic + continuous clock snapshots; sleep/suspend
      detection via boottime_delta; clock-source-change detection.

T2.4  Host suitability: six independent named checks, each with a causal code.
      No aggregate scoring.

T2.5  Network probe: fixed HTTPS probe to pinned S3 endpoint; floor check
      per sample; raw + normalised artifact pair.

T2.6  Fixture verify: manifest loaded, schema-validated, every file hash
      checked; cpython_version vs sys.version checked.

All writes go through rig.atomic.  No wall-clock source in the gate path.
"""

import hashlib
import json
import platform
import shutil
import sys
import time
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from rig.atomic import atomic_write
from rig.host import HostAdapter
from rig.schemas import validate


# ── named error codes (every check must have exactly one) ─────────────────────

TIMER_SANITY_BREACH        = "timer_sanity_breach"
FAIL_INVALID_SESSION_CTX   = "fail_invalid_session_context"
POWER_NOT_AC               = "power_not_ac"
CPU_GOVERNOR_CHANGED       = "cpu_governor_changed"
MEMORY_PRESSURE_HIGH       = "memory_pressure_high"
DISK_FREE_BELOW_FLOOR      = "disk_free_below_floor"
SLEEP_LID_EVENT            = "sleep_lid_event"
VPN_ROUTE_CHANGED          = "vpn_route_changed"
NETWORK_FLOOR_BREACH       = "network_floor_breach"
FIXTURE_DRIFT_AT_PREFLIGHT = "fixture_drift_at_preflight"


# =============================================================================
# T2.3 — Timer sanity
# =============================================================================

# Increase in boottime_delta_ns beyond this threshold means suspend/resume.
SLEEP_TOLERANCE_NS = 100_000_000   # 100 ms

def _clock_pair() -> tuple[int, int, str]:
    """Return (uptime_clock_id, continuous_clock_id, continuous_clock_name).

    "uptime" does not advance during sleep.  "continuous" does.
    Their difference (boottime_delta_ns) increases when the host sleeps.

    Linux:  uptime = CLOCK_MONOTONIC,  continuous = CLOCK_BOOTTIME
    macOS:  uptime = CLOCK_UPTIME_RAW, continuous = CLOCK_MONOTONIC
            (on macOS, CLOCK_MONOTONIC continues during sleep)
    """
    if hasattr(time, "CLOCK_BOOTTIME"):
        return time.CLOCK_MONOTONIC, time.CLOCK_BOOTTIME, "CLOCK_BOOTTIME"
    return time.CLOCK_UPTIME_RAW, time.CLOCK_MONOTONIC, "CLOCK_MONOTONIC"


def _read_clock_source() -> str:
    """Return the name of the OS clocksource, used to detect mid-session changes.

    Linux: reads /sys/.../current_clocksource (e.g. 'tsc', 'hpet').
    macOS: always 'mach' (fixed).
    """
    if platform.system() == "Linux":
        sysfs = Path("/sys/devices/system/clocksource/clocksource0/current_clocksource")
        try:
            return sysfs.read_text(encoding="utf-8").strip()
        except OSError:
            return "unknown"
    return "mach"


@dataclass
class TimerSnapshot:
    monotonic_ns: int       # uptime clock (does not count sleep)
    continuous_ns: int      # continuous clock (counts sleep)
    boottime_delta_ns: int  # continuous - monotonic; increases on sleep
    resolution_ns: int      # CLOCK_MONOTONIC resolution
    clock_source: str       # OS-level clock source name


def take_timer_snapshot() -> TimerSnapshot:
    """Capture a timer snapshot using CLOCK_MONOTONIC-family clocks only."""
    uptime_id, continuous_id, _ = _clock_pair()
    mon_ns  = time.clock_gettime_ns(uptime_id)
    cont_ns = time.clock_gettime_ns(continuous_id)
    res_ns  = int(time.get_clock_info("monotonic").resolution * 1_000_000_000)
    return TimerSnapshot(
        monotonic_ns     = mon_ns,
        continuous_ns    = cont_ns,
        boottime_delta_ns= cont_ns - mon_ns,
        resolution_ns    = res_ns,
        clock_source     = _read_clock_source(),
    )


def check_timer_integrity(
    start: TimerSnapshot,
    end: TimerSnapshot,
    sleep_tolerance_ns: int = SLEEP_TOLERANCE_NS,
) -> list[str]:
    """Return the list of breach codes.  Empty list means clean.

    fail_invalid_session_context: boottime_delta increased (suspend/resume).
    timer_sanity_breach:          clock_source changed mid-session.
    """
    breaches: list[str] = []
    if end.boottime_delta_ns - start.boottime_delta_ns > sleep_tolerance_ns:
        breaches.append(FAIL_INVALID_SESSION_CTX)
    if end.clock_source != start.clock_source:
        breaches.append(TIMER_SANITY_BREACH)
    return breaches


def write_timer_sanity(snapshot: TimerSnapshot, output_path: Path) -> None:
    """Write timer_sanity.json atomically."""
    doc = {
        "schema_version":   1,
        "monotonic_ns":     snapshot.monotonic_ns,
        "continuous_ns":    snapshot.continuous_ns,
        "boottime_delta_ns":snapshot.boottime_delta_ns,
        "resolution_ns":    snapshot.resolution_ns,
        "clock_source":     snapshot.clock_source,
        "captured_at":      datetime.now(timezone.utc).isoformat(),
    }
    validate("timer_sanity", doc)
    atomic_write(str(output_path), json.dumps(doc, indent=2))


# =============================================================================
# T2.4 — Host suitability checks
# =============================================================================

@dataclass
class SuitabilityResult:
    check_name: str
    passed: bool
    causal_code: "str | None"   # None when passed


def run_suitability(
    adapter: HostAdapter,
    *,
    baseline_vpn_hash: str,
    baseline_cpu_state: str,
    baseline_boottime_delta_ns: int,
    disk_path: Path = Path("/"),
    disk_floor_pct: float = 0.10,
    memory_pressure_fail_levels: tuple[str, ...] = ("WARN", "CRITICAL"),
) -> list[SuitabilityResult]:
    """Run all six host suitability checks and return one result per check.

    Checks are independent; all run even if earlier ones fail.
    No aggregate scoring: each result carries its own named causal code.
    """
    results: list[SuitabilityResult] = []

    # power_not_ac
    ac = adapter.power_state().get("ac_power", False)
    results.append(SuitabilityResult(
        "power_not_ac", ac, None if ac else POWER_NOT_AC,
    ))

    # cpu_governor_changed
    current_cpu = adapter.cpu_thermal_state()
    cpu_ok = current_cpu == baseline_cpu_state
    results.append(SuitabilityResult(
        "cpu_governor_changed", cpu_ok, None if cpu_ok else CPU_GOVERNOR_CHANGED,
    ))

    # memory_pressure_high
    pressure = adapter.memory_pressure_level()
    mem_ok = pressure not in memory_pressure_fail_levels
    results.append(SuitabilityResult(
        "memory_pressure_high", mem_ok, None if mem_ok else MEMORY_PRESSURE_HIGH,
    ))

    # disk_free_below_floor
    usage = shutil.disk_usage(str(disk_path))
    disk_ok = (usage.free / usage.total) >= disk_floor_pct
    results.append(SuitabilityResult(
        "disk_free_below_floor", disk_ok, None if disk_ok else DISK_FREE_BELOW_FLOOR,
    ))

    # sleep_lid_event (reuses timer-sanity boottime delta logic)
    snap = take_timer_snapshot()
    sleep_ok = (snap.boottime_delta_ns - baseline_boottime_delta_ns) <= SLEEP_TOLERANCE_NS
    results.append(SuitabilityResult(
        "sleep_lid_event", sleep_ok, None if sleep_ok else SLEEP_LID_EVENT,
    ))

    # vpn_route_changed
    current_vpn = adapter.vpn_route_hash()
    vpn_ok = current_vpn == baseline_vpn_hash
    results.append(SuitabilityResult(
        "vpn_route_changed", vpn_ok, None if vpn_ok else VPN_ROUTE_CHANGED,
    ))

    return results


def write_host_suitability(
    results: list[SuitabilityResult], output_path: Path
) -> None:
    """Write host_suitability.json atomically."""
    all_passed = all(r.passed for r in results)
    doc: dict[str, Any] = {
        "schema_version": 1,
        "pass": all_passed,
        "checks": {
            r.check_name: {"pass": r.passed, "causal_code": r.causal_code}
            for r in results
        },
    }
    validate("host_suitability", doc)
    atomic_write(str(output_path), json.dumps(doc, indent=2))


# =============================================================================
# T2.5 — Network probe (fixed protocol)
# =============================================================================

# Pinned probe configuration.  Not iperf3; not configurable beyond these.
# PROBE_PAYLOAD_BYTES must remain nonzero: the floor check is a throughput gate
# and is inert if no bytes are transferred.  The probe performs a GET with a
# Range header to download exactly this many bytes from the pinned S3 endpoint.
PROBE_URL          = "https://s3.amazonaws.com/"
PROBE_COUNT        = 5
PROBE_PAYLOAD_BYTES = 4096    # 4 KB fixed payload via GET Range header
PROBE_TIMEOUT_S    = 10.0
PROBE_FLOOR_BYTES_PER_SEC = 100_000   # 100 KB/s minimum; configurable in tests


def _single_probe(url: str, timeout_s: float = PROBE_TIMEOUT_S) -> tuple[int, int]:
    """Return (rtt_ns, bytes_received) for one HTTPS GET request.

    Downloads exactly PROBE_PAYLOAD_BYTES via a Range header.  Using GET
    rather than HEAD ensures real bytes flow and makes the throughput floor
    check meaningful.  The Range header keeps the transfer bounded.
    """
    if PROBE_PAYLOAD_BYTES == 0:
        raise RuntimeError(
            "PROBE_PAYLOAD_BYTES is zero; the network floor check would be "
            "inert.  Set a nonzero payload before running the network probe."
        )
    req = urllib.request.Request(url)
    req.add_header("Range", f"bytes=0-{PROBE_PAYLOAD_BYTES - 1}")
    start_ns = time.monotonic_ns()
    with urllib.request.urlopen(req, timeout=timeout_s) as resp:
        body = resp.read()
    return time.monotonic_ns() - start_ns, len(body)


def _ec2_dry_run_latency_ns(region: str = "us-east-1") -> "int | None":
    """Time a boto3 RunInstances DryRun call; return latency_ns or None."""
    try:
        import boto3
        from botocore.exceptions import ClientError
        ec2 = boto3.client("ec2", region_name=region)
        start_ns = time.monotonic_ns()
        try:
            ec2.run_instances(MaxCount=1, MinCount=1, DryRun=True,
                              ImageId="ami-00000000")
        except ClientError:
            pass  # Expected: DryRunOperation or UnauthorizedOperation
        return time.monotonic_ns() - start_ns
    except Exception:
        return None


def run_network_probe(
    url: str = PROBE_URL,
    count: int = PROBE_COUNT,
    floor_bytes_per_sec: int = PROBE_FLOOR_BYTES_PER_SEC,
    include_ec2_dry_run: bool = True,
    ec2_region: str = "us-east-1",
) -> dict[str, Any]:
    """Run the network probe and return the normalised result dict."""
    rtt_samples_ns: list[int] = []
    bytes_total = 0
    raw_samples: list[dict] = []
    start_ns = time.monotonic_ns()

    for i in range(count):
        rtt_ns, n_bytes = _single_probe(url)
        rtt_samples_ns.append(rtt_ns)
        bytes_total += n_bytes
        raw_samples.append({"seq": i, "rtt_ns": rtt_ns, "bytes": n_bytes})

    duration_ns = time.monotonic_ns() - start_ns

    ec2_latency = _ec2_dry_run_latency_ns(ec2_region) if include_ec2_dry_run else None

    # Floor check: each sample's throughput (payload / rtt) must meet the floor.
    # PROBE_PAYLOAD_BYTES is guaranteed nonzero by _single_probe above.
    breach = False
    for rtt_ns in rtt_samples_ns:
        rtt_s = rtt_ns / 1_000_000_000
        if rtt_s > 0 and (PROBE_PAYLOAD_BYTES / rtt_s) < floor_bytes_per_sec:
            breach = True
            break

    return {
        "schema_version":           1,
        "probe_url":                url,
        "probe_count":              count,
        "probe_payload_bytes":      PROBE_PAYLOAD_BYTES,
        "rtt_samples_ns":           rtt_samples_ns,
        "bytes_transferred_total":  bytes_total,
        "duration_ns":              duration_ns,
        "ec2_dry_run_latency_ns":   ec2_latency,
        "floor_passed":             not breach,
        "floor_min_bytes_per_sec":  floor_bytes_per_sec,
    }


def write_network_probe(
    result: dict[str, Any],
    raw_samples: "list[dict] | None",
    output_normalized: Path,
    output_raw: Path,
) -> None:
    """Write both network probe artifacts atomically."""
    validate("network_probe", result)
    atomic_write(str(output_normalized), json.dumps(result, indent=2))

    raw_doc = {
        "schema_version": 1,
        "captured_at": datetime.now(timezone.utc).isoformat(),
        "samples": raw_samples or [],
    }
    validate("network_probe_raw", raw_doc)
    atomic_write(str(output_raw), json.dumps(raw_doc, indent=2))


# =============================================================================
# T2.6 — Fixture verify
# =============================================================================

class FixtureDriftError(Exception):
    """Raised when the fixture on disk diverges from the committed manifest."""
    error_code = FIXTURE_DRIFT_AT_PREFLIGHT

    def __init__(self, reason: str, path: "str | None" = None) -> None:
        super().__init__(f"fixture drift: {reason}" +
                         (f" ({path})" if path else ""))
        self.reason = reason
        self.drift_path = path


def _sha256_path(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


def verify_fixture(fixture_dir: Path) -> str:
    """Verify the fixture at fixture_dir against its MANIFEST.json.

    Returns the fixture_semver on success.
    Raises FixtureDriftError on any mismatch.

    Checks (in order):
    1. MANIFEST.json exists and validates against fixture_manifest schema.
    2. cpython_version in manifest matches platform.python_version().
    3. Every input file hash matches.
    4. Every expected_output file hash matches.
    """
    manifest_path = fixture_dir / "MANIFEST.json"
    if not manifest_path.exists():
        raise FixtureDriftError("MANIFEST.json missing")

    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        raise FixtureDriftError(f"MANIFEST.json invalid JSON: {exc}") from exc

    # Schema validation (uses the fixture_manifest schema from T0.3b).
    from rig.schemas import validate as _validate, SchemaValidationError
    try:
        _validate("fixture_manifest", manifest)
    except SchemaValidationError as exc:
        raise FixtureDriftError(f"manifest schema invalid: {exc}") from exc

    # cpython_version must match the running interpreter's sys.version exactly.
    # The fixture is pinned to a specific interpreter build, not just a version
    # number.  platform.python_version() is not sufficient: it only checks
    # major.minor.patch and would miss build-level differences.
    declared = manifest.get("cpython_version", "")
    running  = sys.version
    if declared != running:
        raise FixtureDriftError(
            f"cpython_version mismatch: manifest={declared!r} running={running!r}"
        )

    # Input file hashes.
    for entry in manifest.get("inputs", []):
        fpath = fixture_dir / entry["path"]
        if not fpath.exists():
            raise FixtureDriftError("input file missing", entry["path"])
        actual = _sha256_path(fpath)
        if actual != entry["sha256"]:
            raise FixtureDriftError("input sha256 mismatch", entry["path"])

    # Expected output file hashes.
    for entry in manifest.get("expected_outputs", []):
        fpath = fixture_dir / entry["path"]
        if not fpath.exists():
            raise FixtureDriftError("output file missing", entry["path"])
        actual = _sha256_path(fpath)
        if actual != entry["sha256"]:
            raise FixtureDriftError("output sha256 mismatch", entry["path"])

    return manifest["fixture_semver"]