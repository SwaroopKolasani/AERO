"""Tests for rig.cli — T0.1 acceptance criterion only."""

import subprocess
import sys
from unittest.mock import patch

import pytest

from rig import __version__
from rig.cli import _git_commit, main


def test_git_commit_returns_string():
    result = _git_commit()
    assert isinstance(result, str)
    assert len(result) > 0


def test_git_commit_fallback_when_git_absent():
    with patch("subprocess.check_output", side_effect=FileNotFoundError):
        assert _git_commit() == "unknown"


def test_git_commit_fallback_on_nonzero_exit():
    with patch(
        "subprocess.check_output",
        side_effect=subprocess.CalledProcessError(128, "git"),
    ):
        assert _git_commit() == "unknown"


def test_version_output_format(capsys):
    """Output must be exactly 'rig <version> (<commit>)'."""
    with patch("rig.cli._git_commit", return_value="abc1234"):
        with pytest.raises(SystemExit) as exc:
            sys.argv = ["rig", "--version"]
            main()
    assert exc.value.code == 0
    assert capsys.readouterr().out.strip() == f"rig {__version__} (abc1234)"


def test_no_args_exits_nonzero():
    sys.argv = ["rig"]
    with pytest.raises(SystemExit) as exc:
        main()
    assert exc.value.code != 0


def test_installed_entrypoint_version():
    """Subprocess test: entry-point must be wired and print the right version."""
    result = subprocess.run(["rig", "--version"], capture_output=True, text=True)
    assert result.returncode == 0
    assert __version__ in result.stdout