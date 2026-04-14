"""
rig/selftest/runner.py

Runs the T0.2 (test_atomic) and T0.3b (test_schemas) suites as a subprocess,
collects per-test outcomes from the JUnit XML report, and writes
self_test_result.json atomically.

Subprocess execution (not pytest.main()) is deliberate: it gives a clean
process boundary, prevents interpreter-state leakage between rig code and
the inner test run, and makes the harness authoritative rather than advisory.

All writes go through rig.atomic — never directly.
"""

import hashlib
import json
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from rig.atomic import atomic_write

_RIG_ROOT = Path(__file__).parent.parent
_REPO_ROOT = _RIG_ROOT.parent
_PINNED_RIG_HASH_FILE = _REPO_ROOT / "pinned" / "rig_self.sha256"

# Frozen suite definition (T0.2 and T0.3b).  Any addition or removal is a
# new suite version and must be treated as such for reproducibility.
DEFAULT_TEST_PATHS: list[Path] = [
    _REPO_ROOT / "tests" / "test_atomic.py",
    _REPO_ROOT / "tests" / "test_schemas.py",
]


# ── suite source hash ─────────────────────────────────────────────────────────

def compute_suite_hash(test_paths: list[Path]) -> str:
    """SHA-256 of test file contents in sorted resolved-path order.

    Frozen definition: each path is resolved to its absolute canonical form;
    the list is sorted lexicographically by that string; contents are
    concatenated in that order before hashing.  Changing the file set, sort
    order, or any file's content changes the hash.
    """
    h = hashlib.sha256()
    for p in sorted(test_paths, key=lambda x: str(x.resolve())):
        h.update(p.read_bytes())
    return h.hexdigest()


# ── rig identity ──────────────────────────────────────────────────────────────

def _read_pinned_hash() -> str | None:
    """Return content of pinned/rig_self.sha256, or None when absent/empty."""
    if not _PINNED_RIG_HASH_FILE.exists():
        return None
    content = _PINNED_RIG_HASH_FILE.read_text(encoding="utf-8").strip()
    return content if content else None


def _compute_source_tree_hash() -> str:
    """SHA-256 of all .py files under rig/, sorted by resolved path."""
    h = hashlib.sha256()
    for p in sorted(_RIG_ROOT.rglob("*.py"), key=lambda x: str(x.resolve())):
        h.update(p.read_bytes())
    return h.hexdigest()


def get_rig_self_hash() -> tuple[str, str]:
    """Return (rig_self_sha256, source).

    source is 'pinned'       when pinned/rig_self.sha256 is non-empty.
    source is 'source_tree'  when the pinned file is absent or empty
                             (development installs only).
    """
    pinned = _read_pinned_hash()
    if pinned is not None:
        return pinned, "pinned"
    return _compute_source_tree_hash(), "source_tree"


# ── JUnit XML result parser ───────────────────────────────────────────────────

def _parse_junit_xml(xml_path: str) -> list[dict[str, str]]:
    """Parse a pytest --junit-xml file into per-check records.

    Returns an empty list when the file is absent, empty, or malformed —
    all of which indicate a collection or infrastructure failure, which the
    non-zero exit code from pytest already captures.

    Each record: {"node_id": str, "outcome": str}
    outcome is one of: "passed", "failed", "error", "skipped"

    Pytest's JUnit classname format is dot-separated (e.g. "tests.test_atomic").
    We convert to path form ("tests/test_atomic.py") to match pytest node IDs.
    """
    try:
        tree = ET.parse(xml_path)
    except (ET.ParseError, FileNotFoundError, OSError):
        return []

    records: list[dict[str, str]] = []
    for tc in tree.getroot().iter("testcase"):
        classname = tc.get("classname", "")
        name = tc.get("name", "")
        module_path = classname.replace(".", "/") + ".py" if classname else ""
        node_id = f"{module_path}::{name}" if module_path else name

        if tc.find("failure") is not None:
            outcome = "failed"
        elif tc.find("error") is not None:
            outcome = "error"
        elif tc.find("skipped") is not None:
            outcome = "skipped"
        else:
            outcome = "passed"

        records.append({"node_id": node_id, "outcome": outcome})

    return records


# ── public entry point ────────────────────────────────────────────────────────

def run_selftest(
    output_path: Path,
    test_paths: list[Path] | None = None,
) -> int:
    """Run the selftest suite as a subprocess; write self_test_result.json.

    Returns 0 if all tests passed, 1 on any failure or collection error.
    The result file is always written even when tests fail, so the caller
    can inspect what went wrong.
    """
    if test_paths is None:
        test_paths = DEFAULT_TEST_PATHS

    suite_sha256 = compute_suite_hash(test_paths)
    # Record the exact files hashed so the suite_source_sha256 is auditable.
    suite_files = [
        str(p.resolve())
        for p in sorted(test_paths, key=lambda x: str(x.resolve()))
    ]
    rig_sha256, rig_sha256_source = get_rig_self_hash()
    timestamp = datetime.now(timezone.utc).isoformat()

    with tempfile.NamedTemporaryFile(suffix=".xml", delete=False) as tf:
        xml_path = tf.name

    cmd = [
        sys.executable, "-m", "pytest",
        "--tb=short", "-q", "--no-header",
        f"--junit-xml={xml_path}",
    ] + [str(p) for p in test_paths]

    proc = subprocess.run(cmd, capture_output=True)
    exit_code = proc.returncode

    per_check = _parse_junit_xml(xml_path)
    Path(xml_path).unlink(missing_ok=True)

    passed = (exit_code == 0)
    result: dict[str, Any] = {
        "schema_version": 1,
        "suite_source_sha256": suite_sha256,
        "suite_test_files": suite_files,
        "result": "passed" if passed else "failed",
        "per_check": per_check,
        "timestamp": timestamp,
        "rig_self_sha256": rig_sha256,
        "rig_self_sha256_source": rig_sha256_source,
    }

    atomic_write(str(output_path), json.dumps(result, indent=2))
    return 0 if passed else 1