"""
rig/host/macos.py — macOS host adapter (T2.2, canonical v1 implementation).

Uses macOS-specific commands:
    sysctl   — kernel, cpu model, cpu count, RAM
    pmset    — AC/battery power state
    ifconfig — NIC interface class
    netstat  — routing table for VPN detection

No Linux stubs; this module is only loaded when platform.system() == "Darwin".
"""

import hashlib
import subprocess
import sys
from pathlib import Path
from typing import Any

from rig.host import FINGERPRINT_FIELDS, HostAdapter

_REPO_ROOT = Path(__file__).parent.parent.parent
_PACKAGE_LOCK = _REPO_ROOT / "pinned" / "rig_env.lock"

# Suitability disqualifier codes.
SUITABILITY_BATTERY_POWER = "fail_invalid_session_context"


def _run(args: list[str]) -> str:
    """Run a command and return its stdout stripped.  Raises on non-zero exit."""
    result = subprocess.run(args, capture_output=True, text=True, check=True)
    return result.stdout.strip()


def _sysctl(name: str) -> str:
    return _run(["sysctl", "-n", name])


# Interface name prefixes that represent stable physical hardware on macOS.
# Excluded: utun/ppp/tun (VPN tunnels), gif/stf (tunnels), vmnet (virtual).
# VPN state is captured separately via vpn_route_hash().
_STABLE_PREFIXES: frozenset[str] = frozenset({
    "en",     # Ethernet and Wi-Fi
    "lo",     # loopback
    "bridge", # bridge adapters
    "awdl",   # Apple Wireless Direct Link (AirDrop/Handoff)
    "llw",    # low-latency WLAN
})


class MacOSAdapter(HostAdapter):
    """macOS-specific implementation of HostAdapter.

    All fingerprint data comes from macOS system calls or commands.
    Methods are pure readers; they do not write to disk.
    """

    def fingerprint(self) -> dict[str, Any]:
        """Gather all nine fingerprint fields from the live macOS host."""
        return {
            "kernel":              self._kernel(),
            "cpu_model":           self._cpu_model(),
            "cpu_count":           self._cpu_count(),
            "ram_bytes":           self._ram_bytes(),
            "power_profile":       self._power_profile(),
            "python_version":      sys.version,
            "package_lock_sha256": self._package_lock_sha256(),
            "nic_identity_class":  self._nic_identity_class(),
            "hostname_hash":       self._hostname_hash(),
        }

    def suitability(self) -> dict[str, Any]:
        """Return a suitability assessment dict.

        suitable=False carries a named disqualifier code.
        """
        power = self.power_state()
        if not power.get("ac_power", False):
            return {
                "suitable": False,
                "disqualifier": SUITABILITY_BATTERY_POWER,
                "reason": "AC power required; host is on battery",
            }
        return {"suitable": True, "disqualifier": None, "reason": None}

    def thermal_sample(self) -> dict[str, Any]:
        """Return raw thermal state from pmset."""
        try:
            raw = _run(["pmset", "-g", "therm"])
        except subprocess.CalledProcessError:
            raw = ""
        return {"raw": raw}

    def power_state(self) -> dict[str, Any]:
        """Return AC/battery state from pmset."""
        try:
            raw = _run(["pmset", "-g", "batt"])
        except subprocess.CalledProcessError:
            raw = ""
        ac_power = "AC Power" in raw
        return {"ac_power": ac_power, "raw": raw}

    def clock_info(self) -> dict[str, Any]:
        """Return clock source and resolution."""
        try:
            resolution_ns = _sysctl("kern.clockrate")
        except subprocess.CalledProcessError:
            resolution_ns = "unknown"
        return {
            "source": "CLOCK_MONOTONIC",
            "resolution_raw": resolution_ns,
        }

    def vpn_route_hash(self) -> str:
        """Return SHA-256 of the current routing table."""
        try:
            table = _run(["netstat", "-rn"])
        except subprocess.CalledProcessError:
            table = ""
        return hashlib.sha256(table.encode()).hexdigest()

    # ── private field collectors ───────────────────────────────────────────

    def _kernel(self) -> str:
        try:
            return _sysctl("kern.osrelease") + " " + _sysctl("kern.version")
        except subprocess.CalledProcessError:
            return "unknown"

    def _cpu_model(self) -> str:
        try:
            return _sysctl("machdep.cpu.brand_string")
        except subprocess.CalledProcessError:
            return "unknown"

    def _cpu_count(self) -> int:
        try:
            return int(_sysctl("hw.logicalcpu"))
        except (subprocess.CalledProcessError, ValueError):
            return 0

    def _ram_bytes(self) -> int:
        try:
            return int(_sysctl("hw.memsize"))
        except (subprocess.CalledProcessError, ValueError):
            return 0

    def _power_profile(self) -> str:
        """Return "AC Power" or "Battery Power" from pmset."""
        try:
            raw = _run(["pmset", "-g", "batt"])
            if "AC Power" in raw:
                return "AC Power"
            return "Battery Power"
        except subprocess.CalledProcessError:
            return "unknown"

    def _package_lock_sha256(self) -> str:
        """SHA-256 of pinned/rig_env.lock; SHA-256 of empty bytes if absent."""
        try:
            data = _PACKAGE_LOCK.read_bytes()
        except FileNotFoundError:
            data = b""
        return hashlib.sha256(data).hexdigest()

    def _nic_identity_class(self) -> str:
        """SHA-256 of stable NIC class identity.

        Only physical and loopback interfaces are included.  Transient
        interfaces (VPN tunnels, virtual adapters) are excluded because
        their presence depends on runtime state — vpn_route_hash() is the
        correct place to track VPN/route changes.
        """
        try:
            names_raw = _run(["ifconfig", "-l"])
            names = names_raw.split()
        except subprocess.CalledProcessError:
            names = []
        stable = sorted({n for n in names
                         if any(n.startswith(p) for p in _STABLE_PREFIXES)})
        return hashlib.sha256(" ".join(stable).encode()).hexdigest()

    def _hostname_hash(self) -> str:
        import socket
        return hashlib.sha256(socket.gethostname().encode()).hexdigest()