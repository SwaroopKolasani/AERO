"""
Tests for rig/manifest.py T1.4: state transition validator.

AC:
  1. Property test enumerates every (from, to) pair; exactly the legal ones succeed.
  2. Illegal transition attempt is persisted (not silently dropped).
  3. State machine never advances from INCOMPLETE to CLOSED.

Additional:
  - Attempts from CLOSED raise ImmutableSessionError and write nothing (architecture
    rule: no mutation of closed sessions).
  - Attempts from INVALID and INCOMPLETE also raise ImmutableSessionError (sealed
    permanent records are not mutated).
"""

import itertools
import time
from pathlib import Path

import pytest

from rig.manifest import (
    ALL_SESSION_STATES,
    IMMUTABLE_STATES,
    LEGAL_TRANSITIONS,
    SESSION_IMMUTABLE,
    ImmutableSessionError,
    read_events,
    validate_transition,
    verify_chain,
)


def _ts():
    return time.monotonic_ns(), "2025-06-01T12:00:00Z"


# ── AC 1: property test ───────────────────────────────────────────────────────

def test_property_exactly_legal_transitions_succeed(tmp_path):
    """Property test over all 36 (from, to) pairs across ALL_SESSION_STATES.

    Three outcomes:
      - from_state in IMMUTABLE_STATES → raises ImmutableSessionError, no file
      - (from, to) in LEGAL_TRANSITIONS → returns (to_state, sha)
      - otherwise (illegal from non-terminal) → returns ("INVALID", sha)
    """
    for i, (from_state, to_state) in enumerate(
        itertools.product(sorted(ALL_SESSION_STATES), sorted(ALL_SESSION_STATES))
    ):
        p = tmp_path / f"e_{i}.jsonl"
        ns, wall = _ts()

        if from_state in IMMUTABLE_STATES:
            with pytest.raises(ImmutableSessionError):
                validate_transition(p, from_state, to_state, None, ns, wall)
            assert not p.exists(), (
                f"immutable state {from_state!r} → {to_state!r} wrote to disk"
            )
        elif (from_state, to_state) in LEGAL_TRANSITIONS:
            state, sha = validate_transition(p, from_state, to_state, None, ns, wall)
            assert state == to_state, (
                f"legal {from_state!r} → {to_state!r}: got {state!r}"
            )
        else:
            state, sha = validate_transition(p, from_state, to_state, None, ns, wall)
            assert state == "INVALID", (
                f"illegal {from_state!r} → {to_state!r}: got {state!r}"
            )


def test_property_legal_count_is_seven():
    """Regression guard: §8 defines exactly 7 legal transitions."""
    assert len(LEGAL_TRANSITIONS) == 7


def test_property_state_count_is_six():
    assert len(ALL_SESSION_STATES) == 6


def test_property_immutable_count_is_three():
    assert IMMUTABLE_STATES == frozenset({"CLOSED", "INVALID", "INCOMPLETE"})


# ── AC 2: illegal transitions are persisted ───────────────────────────────────

def test_illegal_transition_appends_event_to_disk(tmp_path):
    """The state_transition_illegal event must reach disk, not be dropped."""
    p = tmp_path / "events.jsonl"
    ns, wall = _ts()
    validate_transition(p, "OPEN", "CLOSED", None, ns, wall)
    events = read_events(p)
    assert len(events) == 1
    assert events[0]["event_type"] == "state_transition_illegal"


def test_illegal_transition_payload_correct(tmp_path):
    p = tmp_path / "events.jsonl"
    ns, wall = _ts()
    validate_transition(p, "OPEN", "CLOSED", None, ns, wall)
    event = read_events(p)[0]
    assert event["payload"]["from_state"] == "OPEN"
    assert event["payload"]["attempted_to_state"] == "CLOSED"


def test_legal_transition_appends_state_transition_event(tmp_path):
    p = tmp_path / "events.jsonl"
    ns, wall = _ts()
    validate_transition(p, "OPEN", "RUNNING", None, ns, wall)
    event = read_events(p)[0]
    assert event["event_type"] == "state_transition"
    assert event["payload"]["from_state"] == "OPEN"
    assert event["payload"]["to_state"] == "RUNNING"


def test_illegal_event_chain_is_verifiable(tmp_path):
    p = tmp_path / "events.jsonl"
    ns, wall = _ts()
    validate_transition(p, "RUNNING", "OPEN", None, ns, wall)
    verify_chain(read_events(p))


def test_legal_event_chain_is_verifiable(tmp_path):
    p = tmp_path / "events.jsonl"
    ns, wall = _ts()
    validate_transition(p, "OPEN", "RUNNING", None, ns, wall)
    verify_chain(read_events(p))


def test_sequential_transitions_chain_intact(tmp_path):
    p = tmp_path / "events.jsonl"
    ns, wall = _ts()
    _, sha = validate_transition(p, "OPEN", "RUNNING", None, ns, wall)
    _, sha = validate_transition(p, "RUNNING", "FINALIZING", sha, ns, wall)
    _, sha = validate_transition(p, "FINALIZING", "CLOSED", sha, ns, wall)
    verify_chain(read_events(p))


def test_illegal_then_legal_chain_intact(tmp_path):
    p = tmp_path / "events.jsonl"
    ns, wall = _ts()
    _, sha = validate_transition(p, "OPEN", "RUNNING", None, ns, wall)
    _, sha = validate_transition(p, "RUNNING", "CLOSED", sha, ns, wall)   # illegal
    _, sha = validate_transition(p, "RUNNING", "FINALIZING", sha, ns, wall)  # legal
    verify_chain(read_events(p))


# ── AC 3: INCOMPLETE → CLOSED is illegal ─────────────────────────────────────

def test_incomplete_to_closed_raises_immutable(tmp_path):
    """AC: state machine never advances from INCOMPLETE to CLOSED.

    INCOMPLETE is a terminal state; any transition attempt raises
    ImmutableSessionError and writes nothing.
    """
    p = tmp_path / "events.jsonl"
    ns, wall = _ts()
    with pytest.raises(ImmutableSessionError):
        validate_transition(p, "INCOMPLETE", "CLOSED", None, ns, wall)
    assert not p.exists()


def test_incomplete_to_closed_writes_nothing(tmp_path):
    p = tmp_path / "events.jsonl"
    ns, wall = _ts()
    try:
        validate_transition(p, "INCOMPLETE", "CLOSED", None, ns, wall)
    except ImmutableSessionError:
        pass
    assert not p.exists()


# ── CLOSED immutability (primary architecture rule) ───────────────────────────

def test_closed_raises_immutable_error(tmp_path):
    """Architecture rule: no mutation of closed sessions."""
    p = tmp_path / "events.jsonl"
    ns, wall = _ts()
    with pytest.raises(ImmutableSessionError) as exc_info:
        validate_transition(p, "CLOSED", "OPEN", None, ns, wall)
    assert exc_info.value.error_code == SESSION_IMMUTABLE
    assert exc_info.value.from_state == "CLOSED"


def test_closed_writes_nothing(tmp_path):
    p = tmp_path / "events.jsonl"
    ns, wall = _ts()
    with pytest.raises(ImmutableSessionError):
        validate_transition(p, "CLOSED", "RUNNING", None, ns, wall)
    assert not p.exists()


def test_closed_to_any_state_writes_nothing(tmp_path):
    ns, wall = _ts()
    for i, to_state in enumerate(sorted(ALL_SESSION_STATES)):
        p = tmp_path / f"e_{i}.jsonl"
        with pytest.raises(ImmutableSessionError):
            validate_transition(p, "CLOSED", to_state, None, ns, wall)
        assert not p.exists(), f"CLOSED → {to_state} wrote to disk"


def test_invalid_raises_immutable_error(tmp_path):
    p = tmp_path / "events.jsonl"
    ns, wall = _ts()
    with pytest.raises(ImmutableSessionError) as exc_info:
        validate_transition(p, "INVALID", "OPEN", None, ns, wall)
    assert exc_info.value.from_state == "INVALID"
    assert not p.exists()


def test_incomplete_raises_immutable_error(tmp_path):
    p = tmp_path / "events.jsonl"
    ns, wall = _ts()
    with pytest.raises(ImmutableSessionError) as exc_info:
        validate_transition(p, "INCOMPLETE", "RUNNING", None, ns, wall)
    assert exc_info.value.from_state == "INCOMPLETE"
    assert not p.exists()


def test_immutable_error_code_is_named_string():
    assert SESSION_IMMUTABLE == "session_immutable"
    assert SESSION_IMMUTABLE != "unknown"


def test_immutable_error_is_not_subclass_of_value_error():
    # ImmutableSessionError is a distinct exception type.
    assert not issubclass(ImmutableSessionError, ValueError)


# ── specific transition checks ────────────────────────────────────────────────

def test_open_to_running_is_legal(tmp_path):
    ns, wall = _ts()
    state, _ = validate_transition(tmp_path / "e.jsonl", "OPEN", "RUNNING", None, ns, wall)
    assert state == "RUNNING"


def test_running_to_invalid_is_legal(tmp_path):
    ns, wall = _ts()
    state, _ = validate_transition(tmp_path / "e.jsonl", "RUNNING", "INVALID", None, ns, wall)
    assert state == "INVALID"


def test_open_to_invalid_is_illegal(tmp_path):
    """OPEN → INVALID is not in the legal graph; only RUNNING → INVALID is."""
    ns, wall = _ts()
    state, _ = validate_transition(tmp_path / "e.jsonl", "OPEN", "INVALID", None, ns, wall)
    assert state == "INVALID"
    assert read_events(tmp_path / "e.jsonl")[0]["event_type"] == "state_transition_illegal"


def test_open_to_closed_is_illegal(tmp_path):
    ns, wall = _ts()
    state, _ = validate_transition(tmp_path / "e.jsonl", "OPEN", "CLOSED", None, ns, wall)
    assert state == "INVALID"


def test_returned_sha_is_usable_as_next_prev(tmp_path):
    p = tmp_path / "events.jsonl"
    ns, wall = _ts()
    _, sha = validate_transition(p, "OPEN", "RUNNING", None, ns, wall)
    _, sha = validate_transition(p, "RUNNING", "FINALIZING", sha, ns, wall)
    verify_chain(read_events(p))