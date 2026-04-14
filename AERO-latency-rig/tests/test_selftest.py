"""
Tests for rig/selftest — T0.4 (revised).

Sections:
  1. compute_suite_hash — determinism, order independence, content sensitivity
  2. get_rig_self_hash  — pinned path vs source_tree fallback, labeling
  3. _parse_junit_xml   — correct parsing of pass/fail/error/skipped/absent
  4. run_selftest       — direct call with tmp test files (subprocess isolated)
  5. rig selftest CLI   — subprocess invocation of the real suite
"""

import json
import subprocess
import sys
import tempfile
from pathlib import Path
from unittest.mock import patch

import pytest

from rig.selftest.runner import (
    _parse_junit_xml,
    compute_suite_hash,
    get_rig_self_hash,
    run_selftest,
    _PINNED_RIG_HASH_FILE,
    _compute_source_tree_hash,
)


# ── compute_suite_hash ────────────────────────────────────────────────────────

def test_suite_hash_returns_64_lowercase_hex(tmp_path):
    f = tmp_path / "test_x.py"
    f.write_bytes(b"def test_x(): pass\n")
    h = compute_suite_hash([f])
    assert len(h) == 64
    assert h == h.lower()
    assert all(c in "0123456789abcdef" for c in h)


def test_suite_hash_is_deterministic(tmp_path):
    f = tmp_path / "test_x.py"
    f.write_bytes(b"content")
    assert compute_suite_hash([f]) == compute_suite_hash([f])


def test_suite_hash_changes_when_content_changes(tmp_path):
    f = tmp_path / "test_x.py"
    f.write_bytes(b"version 1")
    h1 = compute_suite_hash([f])
    f.write_bytes(b"version 2")
    assert compute_suite_hash([f]) != h1


def test_suite_hash_is_order_independent(tmp_path):
    a = tmp_path / "aaa.py"
    b = tmp_path / "bbb.py"
    a.write_bytes(b"# a")
    b.write_bytes(b"# b")
    assert compute_suite_hash([a, b]) == compute_suite_hash([b, a])


# ── get_rig_self_hash ─────────────────────────────────────────────────────────

def test_get_rig_self_hash_source_tree_when_pinned_empty(tmp_path):
    fake_pinned = tmp_path / "rig_self.sha256"
    fake_pinned.write_text("", encoding="utf-8")
    with patch("rig.selftest.runner._PINNED_RIG_HASH_FILE", fake_pinned):
        h, source = get_rig_self_hash()
    assert source == "source_tree"
    assert len(h) == 64


def test_get_rig_self_hash_source_tree_when_pinned_absent(tmp_path):
    absent = tmp_path / "nonexistent.sha256"
    with patch("rig.selftest.runner._PINNED_RIG_HASH_FILE", absent):
        h, source = get_rig_self_hash()
    assert source == "source_tree"
    assert len(h) == 64


def test_get_rig_self_hash_pinned_when_file_has_content(tmp_path):
    pinned_hash = "a" * 64
    fake_pinned = tmp_path / "rig_self.sha256"
    fake_pinned.write_text(pinned_hash + "\n", encoding="utf-8")
    with patch("rig.selftest.runner._PINNED_RIG_HASH_FILE", fake_pinned):
        h, source = get_rig_self_hash()
    assert h == pinned_hash
    assert source == "pinned"


def test_source_tree_hash_is_64_hex():
    h = _compute_source_tree_hash()
    assert len(h) == 64
    assert all(c in "0123456789abcdef" for c in h)


def test_source_tree_hash_is_deterministic():
    assert _compute_source_tree_hash() == _compute_source_tree_hash()


# ── _parse_junit_xml ──────────────────────────────────────────────────────────

def _write_junit(path: str, testcases: list[dict]) -> None:
    """Write a minimal JUnit XML with the given testcase dicts."""
    cases_xml = ""
    for tc in testcases:
        inner = tc.get("inner", "")
        cases_xml += (
            f'<testcase classname="{tc["classname"]}" name="{tc["name"]}">'
            f"{inner}</testcase>\n"
        )
    xml = f'<?xml version="1.0"?><testsuites><testsuite>{cases_xml}</testsuite></testsuites>'
    Path(path).write_text(xml, encoding="utf-8")


def test_parse_junit_absent_file_returns_empty():
    assert _parse_junit_xml("/tmp/does_not_exist_aero_rig.xml") == []


def test_parse_junit_malformed_xml_returns_empty(tmp_path):
    p = str(tmp_path / "bad.xml")
    Path(p).write_text("THIS IS NOT XML", encoding="utf-8")
    assert _parse_junit_xml(p) == []


def test_parse_junit_passing_test(tmp_path):
    p = str(tmp_path / "r.xml")
    _write_junit(p, [{"classname": "tests.test_atomic", "name": "test_foo"}])
    records = _parse_junit_xml(p)
    assert len(records) == 1
    assert records[0]["node_id"] == "tests/test_atomic.py::test_foo"
    assert records[0]["outcome"] == "passed"


def test_parse_junit_failing_test(tmp_path):
    p = str(tmp_path / "r.xml")
    _write_junit(p, [{"classname": "tests.test_atomic", "name": "test_foo",
                      "inner": "<failure>assert False</failure>"}])
    records = _parse_junit_xml(p)
    assert records[0]["outcome"] == "failed"


def test_parse_junit_errored_test(tmp_path):
    p = str(tmp_path / "r.xml")
    _write_junit(p, [{"classname": "tests.test_schemas", "name": "test_bar",
                      "inner": "<error>import error</error>"}])
    records = _parse_junit_xml(p)
    assert records[0]["outcome"] == "error"


def test_parse_junit_skipped_test(tmp_path):
    p = str(tmp_path / "r.xml")
    _write_junit(p, [{"classname": "tests.test_schemas", "name": "test_baz",
                      "inner": "<skipped/>"}])
    records = _parse_junit_xml(p)
    assert records[0]["outcome"] == "skipped"


def test_parse_junit_converts_classname_to_path(tmp_path):
    p = str(tmp_path / "r.xml")
    _write_junit(p, [{"classname": "tests.test_atomic", "name": "test_x"}])
    records = _parse_junit_xml(p)
    assert records[0]["node_id"] == "tests/test_atomic.py::test_x"


def test_parse_junit_multiple_tests(tmp_path):
    p = str(tmp_path / "r.xml")
    _write_junit(p, [
        {"classname": "tests.test_atomic", "name": "test_a"},
        {"classname": "tests.test_atomic", "name": "test_b",
         "inner": "<failure/>"},
    ])
    records = _parse_junit_xml(p)
    assert len(records) == 2
    assert records[0]["outcome"] == "passed"
    assert records[1]["outcome"] == "failed"


# ── run_selftest ──────────────────────────────────────────────────────────────

def _passing_test(tmp_path: Path) -> Path:
    f = tmp_path / "test_pass.py"
    f.write_bytes(b"def test_ok(): assert True\n")
    return f


def _failing_test(tmp_path: Path) -> Path:
    f = tmp_path / "test_fail.py"
    f.write_bytes(b"def test_bad(): assert False\n")
    return f


def test_run_selftest_passing_returns_zero(tmp_path):
    code = run_selftest(tmp_path / "r.json", [_passing_test(tmp_path)])
    assert code == 0


def test_run_selftest_failing_returns_one(tmp_path):
    code = run_selftest(tmp_path / "r.json", [_failing_test(tmp_path)])
    assert code == 1


def test_run_selftest_writes_file_on_pass(tmp_path):
    out = tmp_path / "r.json"
    run_selftest(out, [_passing_test(tmp_path)])
    assert out.exists()


def test_run_selftest_writes_file_on_fail(tmp_path):
    out = tmp_path / "r.json"
    run_selftest(out, [_failing_test(tmp_path)])
    assert out.exists()


def test_run_selftest_result_passed(tmp_path):
    out = tmp_path / "r.json"
    run_selftest(out, [_passing_test(tmp_path)])
    assert json.loads(out.read_text())["result"] == "passed"


def test_run_selftest_result_failed(tmp_path):
    out = tmp_path / "r.json"
    run_selftest(out, [_failing_test(tmp_path)])
    assert json.loads(out.read_text())["result"] == "failed"


def test_run_selftest_has_all_required_keys(tmp_path):
    out = tmp_path / "r.json"
    run_selftest(out, [_passing_test(tmp_path)])
    data = json.loads(out.read_text())
    for key in ("schema_version", "suite_source_sha256", "suite_test_files",
                "result", "per_check", "timestamp",
                "rig_self_sha256", "rig_self_sha256_source"):
        assert key in data, f"missing key: {key}"


def test_run_selftest_suite_sha256_matches(tmp_path):
    f = _passing_test(tmp_path)
    out = tmp_path / "r.json"
    run_selftest(out, [f])
    data = json.loads(out.read_text())
    assert data["suite_source_sha256"] == compute_suite_hash([f])


def test_run_selftest_suite_files_contains_resolved_path(tmp_path):
    f = _passing_test(tmp_path)
    out = tmp_path / "r.json"
    run_selftest(out, [f])
    data = json.loads(out.read_text())
    assert str(f.resolve()) in data["suite_test_files"]


def test_run_selftest_rig_sha256_source_labeled(tmp_path):
    out = tmp_path / "r.json"
    run_selftest(out, [_passing_test(tmp_path)])
    data = json.loads(out.read_text())
    assert data["rig_self_sha256_source"] in ("pinned", "source_tree")


def test_run_selftest_sha256_fields_are_64_hex(tmp_path):
    out = tmp_path / "r.json"
    run_selftest(out, [_passing_test(tmp_path)])
    data = json.loads(out.read_text())
    for field in ("suite_source_sha256", "rig_self_sha256"):
        h = data[field]
        assert len(h) == 64, f"{field} not 64 chars"
        assert all(c in "0123456789abcdef" for c in h), f"{field} not hex"


def test_run_selftest_per_check_outcomes_bounded(tmp_path):
    out = tmp_path / "r.json"
    run_selftest(out, [_passing_test(tmp_path)])
    data = json.loads(out.read_text())
    valid = {"passed", "failed", "error", "skipped"}
    for record in data["per_check"]:
        assert record["outcome"] in valid


# ── rig selftest CLI (real suite, subprocess) ─────────────────────────────────

def test_cli_selftest_exits_zero(tmp_path):
    r = subprocess.run(
        ["rig", "selftest", "--output", str(tmp_path / "r.json")],
        capture_output=True,
    )
    assert r.returncode == 0


def test_cli_selftest_writes_file(tmp_path):
    out = tmp_path / "r.json"
    subprocess.run(["rig", "selftest", "--output", str(out)],
                   capture_output=True, check=True)
    assert out.exists()


def test_cli_selftest_result_is_valid_json(tmp_path):
    out = tmp_path / "r.json"
    subprocess.run(["rig", "selftest", "--output", str(out)],
                   capture_output=True, check=True)
    json.loads(out.read_text())


def test_cli_selftest_result_says_passed(tmp_path):
    out = tmp_path / "r.json"
    subprocess.run(["rig", "selftest", "--output", str(out)],
                   capture_output=True, check=True)
    assert json.loads(out.read_text())["result"] == "passed"


def test_cli_selftest_per_check_nonempty(tmp_path):
    out = tmp_path / "r.json"
    subprocess.run(["rig", "selftest", "--output", str(out)],
                   capture_output=True, check=True)
    assert len(json.loads(out.read_text())["per_check"]) > 0


def test_cli_selftest_help_exits_zero():
    r = subprocess.run(["rig", "selftest", "--help"], capture_output=True)
    assert r.returncode == 0