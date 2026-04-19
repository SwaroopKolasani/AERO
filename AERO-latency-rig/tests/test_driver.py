"""
Tests for rig/driver.py -- T4.2.

AC:
  1. Same seed produces the same schedule (determinism).
  2. Schedule validates against session_envelope.schema.json when stored.
  3. Property test: no profile is blocked contiguously (adjacent entries differ).

Additional:
  - All 6 profiles appear exactly 50 times.
  - Total length is 300.
  - Different seeds produce different schedules.
  - ScheduleGenerationError on pathological input.
"""

import json
from pathlib import Path

import pytest

from rig.driver import (
    PROFILE_COUNT,
    PROFILES,
    RUNS_PER_PROFILE,
    SCHEDULE_GENERATION_FAILED,
    TOTAL_RUNS,
    ScheduleGenerationError,
    _repair_pass,
    generate_schedule,
)
from rig.envelope import write_session_envelope
from rig.schemas import validate


# -- AC 1: same seed → same schedule ------------------------------------------

def test_same_seed_same_schedule():
    """AC 1: deterministic given the seed."""
    a = generate_schedule("0xAER0")
    b = generate_schedule("0xAER0")
    assert a == b


def test_same_seed_same_schedule_repeated():
    for _ in range(5):
        assert generate_schedule("seed-xyz") == generate_schedule("seed-xyz")


def test_different_seeds_different_schedules():
    a = generate_schedule("seed-A")
    b = generate_schedule("seed-B")
    assert a != b, "different seeds should produce different schedules (probabilistic)"


# -- AC 3 (property): no adjacent duplicates ----------------------------------

def test_no_adjacent_duplicates_property():
    """AC 3: for all i > 0, schedule[i] != schedule[i-1]."""
    schedule = generate_schedule("0xAER0")
    for i in range(1, len(schedule)):
        assert schedule[i] != schedule[i - 1], (
            f"adjacent duplicate at positions {i-1} and {i}: {schedule[i]!r}"
        )


def test_no_adjacent_duplicates_multiple_seeds():
    for seed in ("seed-1", "seed-2", "seed-3", "0xDEAD", "hello"):
        schedule = generate_schedule(seed)
        for i in range(1, len(schedule)):
            assert schedule[i] != schedule[i - 1], (
                f"seed={seed!r}: adjacent duplicate at {i}"
            )


# -- Schedule structure -------------------------------------------------------

def test_total_length_is_300():
    assert len(generate_schedule("0xAER0")) == TOTAL_RUNS == 300


def test_each_profile_appears_exactly_50_times():
    schedule = generate_schedule("0xAER0")
    for profile in PROFILES:
        count = schedule.count(profile)
        assert count == RUNS_PER_PROFILE, (
            f"{profile!r} appears {count} times, expected {RUNS_PER_PROFILE}"
        )


def test_only_known_profiles_in_schedule():
    schedule = generate_schedule("0xAER0")
    assert set(schedule) == set(PROFILES)


def test_profile_count_is_six():
    assert PROFILE_COUNT == 6
    assert len(PROFILES) == 6


def test_runs_per_profile_is_50():
    assert RUNS_PER_PROFILE == 50


def test_all_six_profiles_present():
    for name in ("B1_local", "B2_storage", "B3a_cold_orch",
                 "B3b_compute", "full", "deliberate_failure"):
        assert name in PROFILES


# -- AC 2: schedule validates against session_envelope.schema.json ------------

def test_schedule_in_session_envelope_validates(tmp_path):
    """AC 2: session_envelope.json containing run_schedule validates against schema."""
    SHA64 = "a" * 64
    schedule = generate_schedule("0xAER0")
    doc = write_session_envelope(
        tmp_path / "session_envelope.json",
        session_id                = "ses-001",
        rig_version               = "0.1.0",
        rig_self_sha256           = SHA64,
        git_commit                = "abc1234",
        fixture_manifest_sha256   = SHA64,
        fixture_semver            = "1.0.0",
        profile_plan              = list(PROFILES),
        prng_seed                 = "0xAER0",
        region                    = "us-east-1",
        account_id                = "123456789012",
        sts_credential_remaining_s= 3600,
        run_schedule              = schedule,
    )
    validate("session_envelope", doc)
    assert doc["run_schedule"] == schedule


def test_schedule_stored_in_file(tmp_path):
    SHA64 = "a" * 64
    schedule = generate_schedule("0xAER0")
    write_session_envelope(
        tmp_path / "session_envelope.json",
        session_id                = "ses-001",
        rig_version               = "0.1.0",
        rig_self_sha256           = SHA64,
        git_commit                = "abc1234",
        fixture_manifest_sha256   = SHA64,
        fixture_semver            = "1.0.0",
        profile_plan              = list(PROFILES),
        prng_seed                 = "0xAER0",
        region                    = "us-east-1",
        account_id                = "123456789012",
        sts_credential_remaining_s= 3600,
        run_schedule              = schedule,
    )
    stored = json.loads((tmp_path / "session_envelope.json").read_text())
    assert stored["run_schedule"] == schedule
    assert len(stored["run_schedule"]) == 300


def test_schema_rejects_schedule_with_unknown_profile(tmp_path):
    """run_schedule must contain only valid profile names."""
    from rig.schemas import SchemaValidationError
    SHA64 = "a" * 64
    bad_schedule = ["unknown_profile"] * 300
    with pytest.raises(SchemaValidationError):
        write_session_envelope(
            tmp_path / "session_envelope.json",
            session_id                = "ses-001",
            rig_version               = "0.1.0",
            rig_self_sha256           = SHA64,
            git_commit                = "abc1234",
            fixture_manifest_sha256   = SHA64,
            fixture_semver            = "1.0.0",
            profile_plan              = list(PROFILES),
            prng_seed                 = "0xAER0",
            region                    = "us-east-1",
            account_id                = "123456789012",
            sts_credential_remaining_s= 3600,
            run_schedule              = bad_schedule,
        )


def test_schema_rejects_schedule_wrong_length(tmp_path):
    """run_schedule must be exactly 300 entries."""
    from rig.schemas import SchemaValidationError
    SHA64 = "a" * 64
    short_schedule = list(PROFILES) * 10  # only 60 entries
    with pytest.raises(SchemaValidationError):
        write_session_envelope(
            tmp_path / "session_envelope.json",
            session_id                = "ses-001",
            rig_version               = "0.1.0",
            rig_self_sha256           = SHA64,
            git_commit                = "abc1234",
            fixture_manifest_sha256   = SHA64,
            fixture_semver            = "1.0.0",
            profile_plan              = list(PROFILES),
            prng_seed                 = "0xAER0",
            region                    = "us-east-1",
            account_id                = "123456789012",
            sts_credential_remaining_s= 3600,
            run_schedule              = short_schedule,
        )


def test_session_envelope_requires_schedule(tmp_path):
    """run_schedule is required -- the envelope is written once with schedule committed."""
    SHA64 = "a" * 64
    schedule = generate_schedule("0xAER0")
    doc = write_session_envelope(
        tmp_path / "session_envelope.json",
        session_id                = "ses-001",
        rig_version               = "0.1.0",
        rig_self_sha256           = SHA64,
        git_commit                = "abc1234",
        fixture_manifest_sha256   = SHA64,
        fixture_semver            = "1.0.0",
        profile_plan              = list(PROFILES),
        prng_seed                 = "0xAER0",
        region                    = "us-east-1",
        account_id                = "123456789012",
        sts_credential_remaining_s= 3600,
        run_schedule              = schedule,
    )
    validate("session_envelope", doc)
    assert "run_schedule" in doc
    assert len(doc["run_schedule"]) == 300


# -- repair pass unit tests ---------------------------------------------------

def test_repair_pass_resolves_adjacent_duplicate():
    sched = ["A", "A", "B", "C"]
    _repair_pass(sched)
    for i in range(1, len(sched)):
        assert sched[i] != sched[i - 1]


def test_repair_pass_noop_on_clean_schedule():
    sched = ["A", "B", "C", "A"]
    original = sched[:]
    _repair_pass(sched)
    assert sched == original


def test_repair_pass_raises_when_impossible():
    """Pathological case: only one profile available for remaining positions."""
    # ["A", "A", "A"] -- no valid swap partner exists
    with pytest.raises(ScheduleGenerationError) as exc_info:
        _repair_pass(["A", "A", "A"])
    assert exc_info.value.error_code == SCHEDULE_GENERATION_FAILED


def test_schedule_generation_error_carries_position():
    with pytest.raises(ScheduleGenerationError) as exc_info:
        _repair_pass(["X", "X", "X"])
    assert isinstance(exc_info.value.position, int)


# -- no fixed-order fallback --------------------------------------------------

def test_schedule_is_not_sorted():
    """Schedule must not be in sorted (fixed) order."""
    schedule = generate_schedule("0xAER0")
    assert schedule != sorted(schedule), "schedule must not be fixed-order"


def test_schedule_varies_across_seeds():
    schedules = {generate_schedule(f"seed-{i}")[0] for i in range(20)}
    # With 6 profiles and 20 different seeds, first element varies.
    assert len(schedules) > 1