import argparse
import subprocess
import sys

from rig import __version__


def _git_commit() -> str:
    """Return the short HEAD commit hash, or 'unknown' if not in a git repo."""
    try:
        return subprocess.check_output(
            ["git", "rev-parse", "--short", "HEAD"],
            stderr=subprocess.DEVNULL,
            text=True,
        ).strip()
    except (FileNotFoundError, subprocess.CalledProcessError):
        return "unknown"


def main() -> None:
    parser = argparse.ArgumentParser(prog="rig", description="AERO latency measurement rig")
    parser.add_argument("--version", action="store_true", help="Print version and git commit")
    args = parser.parse_args()

    if args.version:
        print(f"rig {__version__} ({_git_commit()})")
        sys.exit(0)

    parser.print_help(sys.stderr)
    sys.exit(1)