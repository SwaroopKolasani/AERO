"""
Tests for rig/preflight.py — T2.3 through T2.6.

ACs tested:
  T2.3: synthetic boottime delta change detected; clock source change
        triggers timer_sanity_breach.
  T2.4: each of the six disqualifier codes has a dedicated triggering test;
        all-pass emits valid host_suitability.json.
  T2.5: raw + normalised artifacts emitted; floor breach detected.
  T2.6: corrupted input refused; corrupted output refused;
        stale cpython_version refused.

macOS adapter calls are mocked via MagicMock(spec=HostAdapter).
"""

import hashlib
import json
import platform
import shutil
import time
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from rig.host import HostAdapter
from rig.preflight import (
    CPU_GOVERNOR_CHANGED,
    DISK_FREE_BELOW_FLOOR,
    FAIL_INVALID_SESSION_CTX,
    FIXTURE_DRIFT_AT_PREFLIGHT,
    MEMORY_PRESSURE_HIGH,
    NETWORK_FLOOR_BREACH,
    POWER_NOT_AC,
    SLEEP_LID_EVENT,
    TIMER_SANITY_BREACH,
    VPN_ROUTE_CHANGED,
    FixtureDriftError,
    TimerSnapshot,
    check_timer_integrity,
    run_network_probe,
    run_suitability,
    take_timer_snapshot,
    verify_fixture,
    write_host_suitability,
    write_network_probe,
    write_timer_sanity,
)
from rig.schemas import SchemaValidationError, validate


SHA64 = "a" * 64

# ── helpers ───────────────────────────────────────────────────────────────────

def _mock_adapter(**overrides) -> MagicMock:
    adapter = MagicMock(spec=HostAdapter)
    defaults = {
        "power_state.return_value":       {"ac_power": True},
        "cpu_thermal_state.return_value": "NORMAL",
        "memory_pressure_level.return_value": "NORMAL",
        "vpn_route_hash.return_value":    SHA64,
    }
    defaults.update({k + ".return_value" if "." not in k else k: v
                     for k, v in overrides.items()})
    for attr, val in defaults.items():
        parts = attr.split(".")
        obj = adapter
        for p in parts[:-1]:
            obj = getattr(obj, p)
        setattr(obj, parts[-1], val)
    return adapter


def _snap(boottime_delta_ns: int = 0, clock_source: str = "tsc") -> TimerSnapshot:
    ns = time.monotonic_ns()
    return TimerSnapshot(
        monotonic_ns      = ns,
        continuous_ns     = ns + boottime_delta_ns,
        boottime_delta_ns = boottime_delta_ns,
        resolution_ns     = 1,
        clock_source      = clock_source,
    )


# =============================================================================
# T2.3 — Timer sanity
# =============================================================================

def test_take_timer_snapshot_returns_snapshot():
    snap = take_timer_snapshot()
    assert snap.monotonic_ns > 0
    assert isinstance(snap.clock_source, str)
    assert len(snap.clock_source) > 0


def test_timer_snapshot_monotonic_is_positive():
    snap = take_timer_snapshot()
    assert snap.monotonic_ns > 0


def test_boottime_delta_change_detected():
    """AC: synthetic boottime delta change → fail_invalid_session_context."""
    start = _snap(boottime_delta_ns=0)
    end   = _snap(boottime_delta_ns=200_000_000)  # 200ms increase → sleep detected
    breaches = check_timer_integrity(start, end)
    assert FAIL_INVALID_SESSION_CTX in breaches


def test_no_breach_on_stable_delta():
    start = _snap(boottime_delta_ns=1_000_000)
    end   = _snap(boottime_delta_ns=1_050_000)   # < 100ms increase
    breaches = check_timer_integrity(start, end)
    assert FAIL_INVALID_SESSION_CTX not in breaches


def test_clock_source_change_triggers_timer_sanity_breach():
    """AC: clock source change mid-session → timer_sanity_breach."""
    start = _snap(clock_source="tsc")
    end   = _snap(clock_source="hpet")
    breaches = check_timer_integrity(start, end)
    assert TIMER_SANITY_BREACH in breaches


def test_no_breach_when_clock_source_stable():
    start = _snap(clock_source="tsc")
    end   = _snap(clock_source="tsc")
    assert TIMER_SANITY_BREACH not in check_timer_integrity(start, end)


def test_write_timer_sanity_creates_valid_file(tmp_path):
    snap = take_timer_snapshot()
    out  = tmp_path / "timer_sanity.json"
    write_timer_sanity(snap, out)
    doc = json.loads(out.read_text())
    validate("timer_sanity", doc)


def test_write_timer_sanity_file_content(tmp_path):
    snap = _snap(boottime_delta_ns=12345, clock_source="tsc")
    out  = tmp_path / "timer_sanity.json"
    write_timer_sanity(snap, out)
    doc = json.loads(out.read_text())
    assert doc["boottime_delta_ns"] == 12345
    assert doc["clock_source"] == "tsc"


def test_timer_sanity_schema_valid():
    validate("timer_sanity", {
        "schema_version":    1,
        "monotonic_ns":      1000000,
        "continuous_ns":     1001000,
        "boottime_delta_ns": 1000,
        "resolution_ns":     1,
        "clock_source":      "tsc",
        "captured_at":       "2025-06-01T00:00:00Z",
    })


def test_timer_sanity_schema_rejects_missing_field():
    with pytest.raises(SchemaValidationError):
        validate("timer_sanity", {"schema_version": 1})


# =============================================================================
# T2.4 — Host suitability checks
# =============================================================================

def _suitability(adapter, **kwargs) -> dict:
    defaults = dict(
        baseline_vpn_hash="a" * 64,
        baseline_cpu_state="NORMAL",
        baseline_boottime_delta_ns=0,
    )
    defaults.update(kwargs)
    results = run_suitability(adapter, **defaults)
    return {r.check_name: r for r in results}


def test_power_not_ac_triggers(tmp_path):
    """AC: power_not_ac disqualifier fires when on battery."""
    adapter = _mock_adapter()
    adapter.power_state.return_value = {"ac_power": False}
    checks = _suitability(adapter)
    assert not checks["power_not_ac"].passed
    assert checks["power_not_ac"].causal_code == POWER_NOT_AC


def test_cpu_governor_changed_triggers(tmp_path):
    """AC: cpu_governor_changed fires when thermal state changes."""
    adapter = _mock_adapter()
    adapter.cpu_thermal_state.return_value = "CPU_THROTTLED"
    checks = _suitability(adapter, baseline_cpu_state="NORMAL")
    assert not checks["cpu_governor_changed"].passed
    assert checks["cpu_governor_changed"].causal_code == CPU_GOVERNOR_CHANGED


def test_memory_pressure_high_triggers():
    """AC: memory_pressure_high fires on WARN pressure."""
    adapter = _mock_adapter()
    adapter.memory_pressure_level.return_value = "WARN"
    checks = _suitability(adapter)
    assert not checks["memory_pressure_high"].passed
    assert checks["memory_pressure_high"].causal_code == MEMORY_PRESSURE_HIGH


def test_memory_pressure_critical_triggers():
    """CRITICAL level also triggers memory_pressure_high."""
    adapter = _mock_adapter()
    adapter.memory_pressure_level.return_value = "CRITICAL"
    checks = _suitability(adapter)
    assert not checks["memory_pressure_high"].passed
    assert checks["memory_pressure_high"].causal_code == MEMORY_PRESSURE_HIGH


def test_disk_free_below_floor_triggers(tmp_path):
    """AC: disk_free_below_floor fires when free% < floor."""
    adapter = _mock_adapter()
    fake_usage = shutil.disk_usage.__class__ if False else type(
        "Usage", (), {"free": 1, "total": 1000, "used": 999}
    )()
    with patch("rig.preflight.shutil.disk_usage", return_value=fake_usage):
        checks = _suitability(adapter, disk_floor_pct=0.05)
    assert not checks["disk_free_below_floor"].passed
    assert checks["disk_free_below_floor"].causal_code == DISK_FREE_BELOW_FLOOR


def test_sleep_lid_event_triggers():
    """AC: sleep_lid_event fires when boottime_delta increased > 100ms."""
    adapter = _mock_adapter()
    large_delta = 500_000_000   # 500ms baseline + current snap ≈ 500ms more
    # Patch take_timer_snapshot to return a snapshot with a large delta.
    fake_snap = _snap(boottime_delta_ns=large_delta + 200_000_000)
    with patch("rig.preflight.take_timer_snapshot", return_value=fake_snap):
        checks = _suitability(adapter, baseline_boottime_delta_ns=large_delta)
    assert not checks["sleep_lid_event"].passed
    assert checks["sleep_lid_event"].causal_code == SLEEP_LID_EVENT


def test_vpn_route_changed_triggers():
    """AC: vpn_route_changed fires when VPN route hash changes."""
    adapter = _mock_adapter()
    adapter.vpn_route_hash.return_value = "b" * 64
    checks = _suitability(adapter, baseline_vpn_hash="a" * 64)
    assert not checks["vpn_route_changed"].passed
    assert checks["vpn_route_changed"].causal_code == VPN_ROUTE_CHANGED


def test_all_checks_pass_emits_pass_true(tmp_path):
    """AC: passing all checks → host_suitability.json with pass=true."""
    adapter = _mock_adapter()
    # Ensure disk has plenty of space.
    fake_usage = type("U", (), {"free": 900, "total": 1000, "used": 100})()
    with patch("rig.preflight.shutil.disk_usage", return_value=fake_usage), \
         patch("rig.preflight.take_timer_snapshot", return_value=_snap()):
        results = run_suitability(adapter, baseline_vpn_hash=SHA64,
                                   baseline_cpu_state="NORMAL",
                                   baseline_boottime_delta_ns=0)

    out = tmp_path / "host_suitability.json"
    write_host_suitability(results, out)
    doc = json.loads(out.read_text())
    assert doc["pass"] is True
    validate("host_suitability", doc)


def test_suitability_all_checks_present_in_output(tmp_path):
    adapter = _mock_adapter()
    fake_usage = type("U", (), {"free": 900, "total": 1000, "used": 100})()
    with patch("rig.preflight.shutil.disk_usage", return_value=fake_usage), \
         patch("rig.preflight.take_timer_snapshot", return_value=_snap()):
        results = run_suitability(adapter, baseline_vpn_hash=SHA64,
                                   baseline_cpu_state="NORMAL",
                                   baseline_boottime_delta_ns=0)
    out = tmp_path / "host_suitability.json"
    write_host_suitability(results, out)
    doc = json.loads(out.read_text())
    for name in ("power_not_ac", "cpu_governor_changed", "memory_pressure_high",
                 "disk_free_below_floor", "sleep_lid_event", "vpn_route_changed"):
        assert name in doc["checks"], f"missing check: {name}"


def test_suitability_schema_valid_all_pass():
    validate("host_suitability", {
        "schema_version": 1,
        "pass": True,
        "checks": {n: {"pass": True, "causal_code": None} for n in (
            "power_not_ac", "cpu_governor_changed", "memory_pressure_high",
            "disk_free_below_floor", "sleep_lid_event", "vpn_route_changed",
        )},
    })


def test_suitability_schema_rejects_extra_check():
    with pytest.raises(SchemaValidationError):
        validate("host_suitability", {
            "schema_version": 1,
            "pass": True,
            "checks": {
                "power_not_ac": {"pass": True, "causal_code": None},
                "extra_check":  {"pass": True, "causal_code": None},
            },
        })


# =============================================================================
# T2.5 — Network probe
# =============================================================================

def _fake_urlopen(rtt_ns: int = 50_000_000, body: bytes = b""):
    """Return a context manager that simulates urlopen with given RTT."""
    import io
    from unittest.mock import MagicMock

    def open_fn(*args, **kwargs):
        ctx = MagicMock()
        ctx.__enter__ = lambda s: ctx
        ctx.__exit__  = MagicMock(return_value=False)
        ctx.read      = lambda: body
        return ctx

    # We need to actually advance time for RTT; just use a fake that returns immediately.
    return open_fn


def test_network_probe_emits_both_artifacts(tmp_path):
    """AC: probe writes raw and normalised artifacts."""
    out_n = tmp_path / "network_probe.json"
    out_r = tmp_path / "network_probe_raw.json"

    with patch("urllib.request.urlopen") as mock_open, \
         patch("rig.preflight._ec2_dry_run_latency_ns", return_value=None):
        ctx = MagicMock()
        ctx.__enter__ = lambda s: ctx
        ctx.__exit__  = MagicMock(return_value=False)
        ctx.read      = lambda: b""
        mock_open.return_value = ctx

        result = run_network_probe(count=2, include_ec2_dry_run=False)

    write_network_probe(result, [{"seq": 0}], out_n, out_r)
    assert out_n.exists()
    assert out_r.exists()
    validate("network_probe", json.loads(out_n.read_text()))
    validate("network_probe_raw", json.loads(out_r.read_text()))


def test_network_probe_floor_breach_detected(tmp_path):
    """AC: floor breach detected and produces floor_passed=False.

    PROBE_PAYLOAD_BYTES is nonzero (4096).  A 10-second RTT with a 4 KB
    payload yields ~410 B/s, well below a 1 MB/s floor → breach.
    """
    out_n = tmp_path / "network_probe.json"
    out_r = tmp_path / "network_probe_raw.json"

    def slow_probe(url, timeout_s=10.0):
        return 10_000_000_000, 4096   # 10 s RTT, 4 KB → 410 B/s

    with patch("rig.preflight._single_probe", side_effect=slow_probe), \
         patch("rig.preflight._ec2_dry_run_latency_ns", return_value=None):
        result = run_network_probe(
            count=1,
            floor_bytes_per_sec=1_000_000,
            include_ec2_dry_run=False,
        )

    assert result["floor_passed"] is False
    write_network_probe(result, [], out_n, out_r)
    doc = json.loads(out_n.read_text())
    assert doc["floor_passed"] is False


def test_network_probe_passing_floor(tmp_path):
    """Golden: passing probe has floor_passed=True."""
    with patch("urllib.request.urlopen") as mock_open, \
         patch("rig.preflight._ec2_dry_run_latency_ns", return_value=None):
        ctx = MagicMock()
        ctx.__enter__ = lambda s: ctx
        ctx.__exit__  = MagicMock(return_value=False)
        ctx.read      = lambda: b""
        mock_open.return_value = ctx
        result = run_network_probe(count=2, floor_bytes_per_sec=0,
                                    include_ec2_dry_run=False)
    assert result["floor_passed"] is True


def test_network_probe_schema_valid():
    validate("network_probe", {
        "schema_version":           1,
        "probe_url":                "https://s3.amazonaws.com/",
        "probe_count":              5,
        "probe_payload_bytes":      0,
        "rtt_samples_ns":           [50_000_000] * 5,
        "bytes_transferred_total":  0,
        "duration_ns":              300_000_000,
        "ec2_dry_run_latency_ns":   None,
        "floor_passed":             True,
        "floor_min_bytes_per_sec":  100_000,
    })


def test_network_probe_schema_rejects_extra_field():
    with pytest.raises(SchemaValidationError):
        validate("network_probe", {
            "schema_version": 1, "probe_url": "x", "probe_count": 1,
            "probe_payload_bytes": 0, "rtt_samples_ns": [], "bytes_transferred_total": 0,
            "duration_ns": 0, "ec2_dry_run_latency_ns": None,
            "floor_passed": True, "floor_min_bytes_per_sec": 0,
            "extra": "bad",
        })


# =============================================================================
# T2.6 — Fixture verify
# =============================================================================

def _make_fixture(tmp_path: Path, **overrides) -> Path:
    """Build a minimal valid fixture directory for testing."""
    fixture_dir = tmp_path / "fixture"
    inputs_dir  = fixture_dir / "inputs"
    outputs_dir = fixture_dir / "outputs"
    inputs_dir.mkdir(parents=True)
    outputs_dir.mkdir(parents=True)

    inp = inputs_dir / "data.bin"
    inp.write_bytes(b"hello input")
    out = outputs_dir / "result.bin"
    out.write_bytes(b"hello output")

    manifest = {
        "schema_version":    1,
        "fixture_semver":    "1.0.0",
        "cpython_version":   __import__("sys").version,   # full sys.version, not short form
        "stdlib_modules":    ["os"] * 20,
        "generator_version": "1.0",
        "generator_cmdline": "rig fixture regenerate",
        "seed":              "0xAER0",
        "inputs": [{
            "path":   "inputs/data.bin",
            "sha256": hashlib.sha256(b"hello input").hexdigest(),
            "size":   11,
        }],
        "script_sha256": "a" * 64,
        "expected_outputs": [{
            "path":   "outputs/result.bin",
            "sha256": hashlib.sha256(b"hello output").hexdigest(),
            "size":   12,
        }],
        "failure_oracle": {
            "expected_exit_code":               1,
            "expected_stderr_substring_sha256": "b" * 64,
        },
    }
    manifest.update(overrides)
    (fixture_dir / "MANIFEST.json").write_text(
        json.dumps(manifest), encoding="utf-8"
    )
    return fixture_dir


def test_fixture_verify_clean_returns_semver(tmp_path):
    fixture_dir = _make_fixture(tmp_path)
    semver = verify_fixture(fixture_dir)
    assert semver == "1.0.0"


def test_fixture_verify_corrupted_input_refused(tmp_path):
    """AC: corrupted input byte is refused."""
    fixture_dir = _make_fixture(tmp_path)
    (fixture_dir / "inputs" / "data.bin").write_bytes(b"corrupted")
    with pytest.raises(FixtureDriftError) as exc_info:
        verify_fixture(fixture_dir)
    assert exc_info.value.error_code == FIXTURE_DRIFT_AT_PREFLIGHT
    assert "sha256 mismatch" in exc_info.value.reason or "mismatch" in str(exc_info.value)


def test_fixture_verify_corrupted_output_refused(tmp_path):
    """AC: corrupted expected output is refused."""
    fixture_dir = _make_fixture(tmp_path)
    (fixture_dir / "outputs" / "result.bin").write_bytes(b"corrupted output")
    with pytest.raises(FixtureDriftError) as exc_info:
        verify_fixture(fixture_dir)
    assert exc_info.value.error_code == FIXTURE_DRIFT_AT_PREFLIGHT


def test_fixture_verify_stale_cpython_refused(tmp_path):
    """AC: stale cpython_version vs sys.version is refused.

    The manifest stores the full sys.version string.  A manifest containing
    only platform.python_version() (e.g. "3.12.3") must also be refused
    because it does not match sys.version exactly.
    """
    import sys as _sys
    # Full sys.version looks like "3.12.3 (main, Apr 11 2024, ...) [GCC ...]".
    # Use a deliberately wrong full string to trigger the mismatch.
    fixture_dir = _make_fixture(tmp_path,
                                cpython_version="3.0.0 (old build, Jan 1 2000)")
    with pytest.raises(FixtureDriftError) as exc_info:
        verify_fixture(fixture_dir)
    assert "cpython_version" in exc_info.value.reason
    assert exc_info.value.error_code == FIXTURE_DRIFT_AT_PREFLIGHT


def test_fixture_verify_platform_version_alone_refused(tmp_path):
    """platform.python_version() alone (e.g. '3.12.3') must be refused.

    The contract is sys.version, not just major.minor.patch.  A manifest
    storing only the short version string would silently accept a different
    interpreter build on the same 3.x.y release.
    """
    import platform as _platform
    short_version = _platform.python_version()   # e.g. "3.12.3"
    fixture_dir   = _make_fixture(tmp_path, cpython_version=short_version)
    with pytest.raises(FixtureDriftError) as exc_info:
        verify_fixture(fixture_dir)
    # This must fail because sys.version != platform.python_version().
    assert exc_info.value.error_code == FIXTURE_DRIFT_AT_PREFLIGHT


def test_fixture_verify_missing_manifest_refused(tmp_path):
    fixture_dir = tmp_path / "empty"
    fixture_dir.mkdir()
    with pytest.raises(FixtureDriftError):
        verify_fixture(fixture_dir)


def test_fixture_verify_missing_input_file_refused(tmp_path):
    fixture_dir = _make_fixture(tmp_path)
    (fixture_dir / "inputs" / "data.bin").unlink()
    with pytest.raises(FixtureDriftError):
        verify_fixture(fixture_dir)


def test_fixture_drift_error_code_is_named():
    err = FixtureDriftError("some reason")
    assert err.error_code == FIXTURE_DRIFT_AT_PREFLIGHT
    assert err.error_code != "unknown"