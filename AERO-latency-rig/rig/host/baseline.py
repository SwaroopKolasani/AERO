"""
rig/host/baseline.py — baseline capture for T2.2.

capture_baseline() is the only code path that writes host_baseline_diff.json.
It does not write the baseline fingerprint itself; the operator reviews the
diff and commits a new baselines/host_baseline.json manually.
"""

import hashlib
import json
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from rig.atomic import atomic_write
from rig.host import HostAdapter, compare_fingerprints
from rig.schemas import validate


def _fingerprint_sha256(fp: dict[str, Any]) -> str:
    canonical = json.dumps(fp, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(canonical.encode()).hexdigest()


def capture_baseline(
    adapter: HostAdapter,
    baseline_path: Path,
    output_path: Path,
) -> dict[str, Any]:
    """Capture the current fingerprint and emit host_baseline_diff.json.

    If a prior baseline exists at baseline_path, the diff records which
    fields changed.  If no prior baseline exists, prior_baseline_sha256 is
    null and changed_fields lists all fields (the entire fingerprint is new).

    The returned dict is also written atomically to output_path.
    Raises OSError if output_path's parent directory does not exist.
    """
    current_fp = adapter.fingerprint()
    current_sha = _fingerprint_sha256(current_fp)

    if baseline_path.exists():
        prior = json.loads(baseline_path.read_text(encoding="utf-8"))
        prior_sha: "str | None" = _fingerprint_sha256(prior)
        changed = compare_fingerprints(prior, current_fp)
    else:
        prior_sha = None
        changed = sorted(current_fp.keys())

    diff: dict[str, Any] = {
        "schema_version":             1,
        "captured_at":                datetime.now(timezone.utc).isoformat(),
        "current_fingerprint_sha256": current_sha,
        "prior_baseline_sha256":      prior_sha,
        "matches":                    len(changed) == 0,
        "changed_fields":             changed,
    }

    validate("host_baseline_diff", diff)
    atomic_write(str(output_path), json.dumps(diff, indent=2))
    return diff