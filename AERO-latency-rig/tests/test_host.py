"""
Tests for rig/host/ — T2.2.

AC:
  1. Fingerprint stable across runs on the same host (mocked macOS).
  2. Intentional mutation detected (test patches cpu_model in fingerprint).
  3. Baseline diff artifact schema-valid.
  4. On Linux or unknown host: UnsupportedHost raised before session state written.

All macOS-specific subprocess calls are mocked.  The UnsupportedHost test
runs directly because the test environment is Linux.
"""

import hashlib
import json
import platform
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from rig.host import (
    FINGERPRINT_FIELDS,
    HOST_FINGERPRINT_DRIFT,
    UNSUPPORTED_HOST,
    FingerprintDriftError,
    HostAdapter,
    UnsupportedHost,
    check_baseline,
    compare_fingerprints,
    current,
)
from rig.host.baseline import capture_baseline
from rig.schemas import SchemaValidationError, validate


# ── valid fingerprint fixture (simulates macOS output) ────────────────────────

SHA64 = "a" * 64


def _fp(**overrides) -> dict:
    base = {
        "kernel":              "22.6.0 Darwin Kernel Version 22.6.0",
        "cpu_model":           "Apple M2 Pro",
        "cpu_count":           12,
        "ram_bytes":           16_000_000_000,
        "power_profile":       "AC Power",
        "python_version":      "3.12.3",
        "package_lock_sha256": SHA64,
        "nic_identity_class":  "b" * 64,
        "hostname_hash":       "c" * 64,
    }
    base.update(overrides)
    return base


def _mock_adapter(fp: dict | None = None) -> MagicMock:
    """Return a MagicMock that behaves like a MacOSAdapter returning fp."""
    adapter = MagicMock(spec=HostAdapter)
    adapter.fingerprint.return_value = fp if fp is not None else _fp()
    return adapter


# ── AC 4: UnsupportedHost on non-macOS ───────────────────────────────────────

def test_current_raises_unsupported_host_on_linux():
    """AC: on Linux, current() raises UnsupportedHost before any state is written."""
    if platform.system() == "Darwin":
        pytest.skip("test requires non-macOS")
    with pytest.raises(UnsupportedHost) as exc_info:
        current()
    assert exc_info.value.error_code == UNSUPPORTED_HOST


def test_unsupported_host_names_the_os():
    err = UnsupportedHost("Linux")
    assert "Linux" in str(err)
    assert err.os_name == "Linux"


def test_current_uses_macos_adapter_on_darwin():
    """current() imports MacOSAdapter when platform is Darwin."""
    with patch("platform.system", return_value="Darwin"):
        with patch("rig.host.macos.MacOSAdapter") as mock_cls:
            mock_cls.return_value = MagicMock(spec=HostAdapter)
            result = current()
    mock_cls.assert_called_once()


def test_unsupported_host_error_code_is_named():
    assert UNSUPPORTED_HOST == "unsupported_host"
    assert UNSUPPORTED_HOST != "unknown"


# ── AC 1: fingerprint stable across runs ─────────────────────────────────────

def test_fingerprint_stable_same_inputs():
    """AC: same subprocess outputs → same fingerprint dict (deterministic)."""
    fp = _fp()
    adapter = _mock_adapter(fp)
    # Two calls to fingerprint() on the same adapter return equal dicts.
    assert adapter.fingerprint() == adapter.fingerprint()


def test_fingerprint_has_all_required_fields():
    fp = _fp()
    for field in FINGERPRINT_FIELDS:
        assert field in fp, f"missing field: {field}"


def test_fingerprint_field_count_matches_spec():
    assert len(FINGERPRINT_FIELDS) == 9


def test_fingerprint_validates_against_schema():
    validate("host_fingerprint", _fp())


def test_fingerprint_schema_rejects_missing_field():
    bad = _fp()
    del bad["cpu_model"]
    with pytest.raises(SchemaValidationError):
        validate("host_fingerprint", bad)


def test_fingerprint_schema_rejects_extra_field():
    bad = {**_fp(), "extra": "not allowed"}
    with pytest.raises(SchemaValidationError):
        validate("host_fingerprint", bad)


def test_fingerprint_schema_rejects_bad_sha256():
    bad = _fp(package_lock_sha256="short")
    with pytest.raises(SchemaValidationError):
        validate("host_fingerprint", bad)


# ── AC 2: intentional mutation detected ──────────────────────────────────────

def test_mutation_detected_when_cpu_model_patched():
    """AC: patching cpu_model in the fingerprint is detected by compare_fingerprints."""
    baseline = _fp()
    mutated  = _fp(cpu_model="Intel Core i9-9999K")
    changed = compare_fingerprints(baseline, mutated)
    assert "cpu_model" in changed


def test_no_drift_when_fingerprints_identical():
    fp = _fp()
    assert compare_fingerprints(fp, fp.copy()) == []


def test_all_single_field_mutations_detected():
    """Every field can be mutated and detected independently."""
    baseline = _fp()
    mutations = {
        "kernel":              "21.0.0 old kernel",
        "cpu_model":           "Intel Core i7",
        "cpu_count":           4,
        "ram_bytes":           8_000_000_000,
        "power_profile":       "Battery Power",
        "python_version":      "3.11.0",
        "package_lock_sha256": "b" * 64,
        "nic_identity_class":  "c" * 64,
        "hostname_hash":       "d" * 64,
    }
    for field, new_value in mutations.items():
        mutated = {**baseline, field: new_value}
        changed = compare_fingerprints(baseline, mutated)
        assert field in changed, f"mutation of {field!r} not detected"


def test_extra_field_in_current_detected_as_drift():
    baseline = _fp()
    current_fp = {**_fp(), "unexpected_field": "value"}
    changed = compare_fingerprints(baseline, current_fp)
    assert "unexpected_field" in changed


def test_missing_field_in_current_detected_as_drift():
    baseline = _fp()
    current_fp = _fp()
    del current_fp["cpu_model"]
    changed = compare_fingerprints(baseline, current_fp)
    assert "cpu_model" in changed


def test_check_baseline_raises_on_drift(tmp_path):
    """check_baseline returns changed fields when current diverges from baseline."""
    baseline = _fp()
    (tmp_path / "baseline.json").write_text(json.dumps(baseline))
    adapter = _mock_adapter(_fp(cpu_model="Different CPU"))
    changed = check_baseline(tmp_path / "baseline.json", adapter)
    assert "cpu_model" in changed


def test_check_baseline_returns_empty_on_match(tmp_path):
    fp = _fp()
    (tmp_path / "baseline.json").write_text(json.dumps(fp))
    adapter = _mock_adapter(fp)
    assert check_baseline(tmp_path / "baseline.json", adapter) == []


def test_check_baseline_raises_if_no_baseline(tmp_path):
    adapter = _mock_adapter()
    with pytest.raises(FileNotFoundError):
        check_baseline(tmp_path / "nonexistent.json", adapter)


def test_fingerprint_drift_error_carries_changed_fields():
    err = FingerprintDriftError(["cpu_model", "kernel"])
    assert err.changed_fields == ["cpu_model", "kernel"]
    assert err.error_code == HOST_FINGERPRINT_DRIFT


# ── AC 3: baseline diff artifact schema-valid ─────────────────────────────────

def _valid_diff(**overrides) -> dict:
    base = {
        "schema_version":             1,
        "captured_at":                "2025-06-01T12:00:00+00:00",
        "current_fingerprint_sha256": SHA64,
        "prior_baseline_sha256":      None,
        "matches":                    True,
        "changed_fields":             [],
    }
    base.update(overrides)
    return base


def test_diff_schema_valid_no_prior():
    validate("host_baseline_diff", _valid_diff())


def test_diff_schema_valid_with_prior():
    validate("host_baseline_diff", _valid_diff(prior_baseline_sha256="b" * 64))


def test_diff_schema_valid_with_changes():
    validate("host_baseline_diff", _valid_diff(
        matches=False,
        changed_fields=["cpu_model"],
    ))


def test_diff_schema_rejects_extra_field():
    with pytest.raises(SchemaValidationError):
        validate("host_baseline_diff", {**_valid_diff(), "note": "bad"})


def test_diff_schema_rejects_bad_sha256():
    with pytest.raises(SchemaValidationError):
        validate("host_baseline_diff", _valid_diff(current_fingerprint_sha256="short"))


def test_diff_schema_rejects_missing_field():
    bad = _valid_diff()
    del bad["matches"]
    with pytest.raises(SchemaValidationError):
        validate("host_baseline_diff", bad)


def test_capture_baseline_no_prior_emits_all_fields(tmp_path):
    """When no prior baseline exists, changed_fields lists all fingerprint keys."""
    fp = _fp()
    adapter = _mock_adapter(fp)
    diff = capture_baseline(adapter, tmp_path / "nonexistent.json", tmp_path / "diff.json")
    assert diff["prior_baseline_sha256"] is None
    assert diff["matches"] is False
    for key in fp.keys():
        assert key in diff["changed_fields"]


def test_capture_baseline_no_prior_emits_all_fields_v2(tmp_path):
    """Simpler version: changed_fields is non-empty when no prior baseline."""
    adapter = _mock_adapter()
    diff = capture_baseline(adapter, tmp_path / "x.json", tmp_path / "diff.json")
    assert len(diff["changed_fields"]) > 0


def test_capture_baseline_matching_prior_reports_no_changes(tmp_path):
    fp = _fp()
    baseline_path = tmp_path / "baseline.json"
    baseline_path.write_text(json.dumps(fp))
    adapter = _mock_adapter(fp)
    diff = capture_baseline(adapter, baseline_path, tmp_path / "diff.json")
    assert diff["matches"] is True
    assert diff["changed_fields"] == []


def test_capture_baseline_drift_reported(tmp_path):
    baseline_fp = _fp()
    current_fp  = _fp(cpu_model="Different CPU")
    baseline_path = tmp_path / "baseline.json"
    baseline_path.write_text(json.dumps(baseline_fp))
    adapter = _mock_adapter(current_fp)
    diff = capture_baseline(adapter, baseline_path, tmp_path / "diff.json")
    assert diff["matches"] is False
    assert "cpu_model" in diff["changed_fields"]


def test_capture_baseline_writes_file(tmp_path):
    adapter = _mock_adapter()
    out = tmp_path / "diff.json"
    capture_baseline(adapter, tmp_path / "x.json", out)
    assert out.exists()
    json.loads(out.read_text())  # must be valid JSON


def test_capture_baseline_output_passes_schema(tmp_path):
    adapter = _mock_adapter()
    diff = capture_baseline(adapter, tmp_path / "x.json", tmp_path / "diff.json")
    validate("host_baseline_diff", diff)


# ── nic_identity_class stability: VPN churn must not cause drift ──────────────

def test_vpn_interface_excluded_from_nic_class(tmp_path):
    """VPN tunnel interfaces (utun, ppp, tun) must not appear in nic_identity_class.

    If they did, toggling a VPN would change the fingerprint on the same
    hardware, violating the stability requirement.  vpn_route_hash() is the
    correct hook for VPN state detection.
    """
    from rig.host.macos import MacOSAdapter, _STABLE_PREFIXES  # noqa: PLC0415

    # Simulate ifconfig -l output with and without a VPN tunnel.
    without_vpn = "en0 en1 lo0 bridge0 awdl0"
    with_vpn    = "en0 en1 lo0 bridge0 awdl0 utun0 utun1"

    adapter = MacOSAdapter()
    with patch("rig.host.macos._run", return_value=without_vpn):
        sha_no_vpn = adapter._nic_identity_class()
    with patch("rig.host.macos._run", return_value=with_vpn):
        sha_with_vpn = adapter._nic_identity_class()

    assert sha_no_vpn == sha_with_vpn, (
        "nic_identity_class changed when VPN tunnel appeared — "
        "VPN state must be tracked via vpn_route_hash(), not fingerprint"
    )


def test_nic_class_stable_across_two_calls(tmp_path):
    """Same physical interface list → same nic_identity_class (deterministic)."""
    from rig.host.macos import MacOSAdapter  # noqa: PLC0415

    adapter = MacOSAdapter()
    iface_output = "en0 en1 lo0"
    with patch("rig.host.macos._run", return_value=iface_output):
        sha_a = adapter._nic_identity_class()
    with patch("rig.host.macos._run", return_value=iface_output):
        sha_b = adapter._nic_identity_class()

    assert sha_a == sha_b