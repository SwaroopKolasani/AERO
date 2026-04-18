"""
rig/host/__init__.py — host adapter interface (T2.2).

The HostAdapter ABC declares what every host adapter must provide.
current() returns the correct concrete adapter for the running OS, or
raises UnsupportedHost if none exists.

v1 supports macOS only.  Adding a Linux adapter later means adding
rig/host/linux.py and one branch in current(); nothing else changes.
"""

import json
import platform
from abc import ABC, abstractmethod
from pathlib import Path
from typing import Any


# Named error codes (every environmental check must have one).
UNSUPPORTED_HOST       = "unsupported_host"
HOST_FINGERPRINT_DRIFT = "host_fingerprint_drift"

# Fingerprint fields defined in §6.  All are required; none are optional.
FINGERPRINT_FIELDS: tuple[str, ...] = (
    "kernel",
    "cpu_model",
    "cpu_count",
    "ram_bytes",
    "power_profile",
    "python_version",
    "package_lock_sha256",
    "nic_identity_class",
    "hostname_hash",
)


class UnsupportedHost(Exception):
    """Raised when no adapter exists for the running OS."""
    error_code = UNSUPPORTED_HOST

    def __init__(self, os_name: str) -> None:
        super().__init__(f"no host adapter for OS {os_name!r}")
        self.os_name = os_name


class FingerprintDriftError(Exception):
    """Raised when the current fingerprint differs from the committed baseline."""
    error_code = HOST_FINGERPRINT_DRIFT

    def __init__(self, changed_fields: list[str]) -> None:
        super().__init__(
            f"host fingerprint drift detected in fields: {changed_fields}"
        )
        self.changed_fields = changed_fields


class HostAdapter(ABC):
    """Abstract interface all host adapters must implement.

    Every method returns a plain dict or str.  Tolerant comparison modes
    are prohibited; all fingerprint fields are exact.
    """

    @abstractmethod
    def fingerprint(self) -> dict[str, Any]:
        """Return the host fingerprint dict (9 fields, all required).

        Calling this twice on the same host with unchanged hardware must
        produce equal dicts.
        """

    @abstractmethod
    def suitability(self) -> dict[str, Any]:
        """Return a suitability assessment for measurement.

        If the host is not suitable, the dict contains a named disqualifier
        code that the preflight layer uses to refuse session start.
        """

    @abstractmethod
    def thermal_sample(self) -> dict[str, Any]:
        """Return a thermal-state snapshot."""

    @abstractmethod
    def power_state(self) -> dict[str, Any]:
        """Return current power / battery state."""

    @abstractmethod
    def clock_info(self) -> dict[str, Any]:
        """Return clock source and monotonic timer metadata."""

    @abstractmethod
    def vpn_route_hash(self) -> str:
        """Return SHA-256 of the current routing table (hex64).

        A VPN changes the routing table; hash change mid-session invalidates.
        """


    @abstractmethod
    def memory_pressure_level(self) -> str:
        """Return memory pressure level: 'NORMAL', 'WARN', or 'CRITICAL'."""

    @abstractmethod
    def cpu_thermal_state(self) -> str:
        """Return current CPU thermal/throttle state string.

        Used to detect CPU governor changes between baseline and mid-session
        checks.  Any change in the returned value triggers cpu_governor_changed.
        """



def compare_fingerprints(baseline: dict[str, Any],
                         current: dict[str, Any]) -> list[str]:
    """Return field names that differ between baseline and current.

    Empty list means identical.  Comparison is exact: no tolerance, no
    optional fields.  Keys present in one dict but absent in the other
    count as changed.
    """
    changed: list[str] = []
    for key in sorted(set(baseline) | set(current)):
        if baseline.get(key) != current.get(key):
            changed.append(key)
    return changed


def check_baseline(baseline_path: Path, adapter: HostAdapter) -> list[str]:
    """Compare the current fingerprint to the committed baseline.

    Returns empty list on match, or the list of changed field names.
    Raises FileNotFoundError if no baseline has been committed.
    """
    baseline = json.loads(baseline_path.read_text(encoding="utf-8"))
    return compare_fingerprints(baseline, adapter.fingerprint())


def current() -> HostAdapter:
    """Return the host adapter for the running OS.

    Raises UnsupportedHost on any OS other than macOS.
    This check happens before any session state is written.
    """
    os_name = platform.system()
    if os_name == "Darwin":
        from rig.host.macos import MacOSAdapter
        return MacOSAdapter()
    raise UnsupportedHost(os_name)