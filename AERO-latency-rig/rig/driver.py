"""
rig/driver.py -- randomized run schedule (T4.2).

Produces a PRNG-seeded, interleaved run schedule of 300 entries:
6 profiles × 50 runs each.  The schedule guarantees no two adjacent
runs share the same profile (strict no-contiguous constraint, confirmed spec).

Profiles (6 total, confirmed):
    B1_local, B2_storage, B3a_cold_orch, B3b_compute, full, deliberate_failure

Note: breakeven is a fixture variant used by B3b_compute and full, not a
separate profile.  The schedule always has exactly PROFILE_COUNT × RUNS_PER_PROFILE
entries.

Algorithm (deterministic):
    At each position, pick uniformly at random (weighted by remaining count)
    from all profiles that have remaining runs AND differ from the last-placed
    profile.

    Edge case: if the only profile with remaining runs IS the last-placed one
    (mathematically possible near the end of the sequence), insert it at the
    most-recent interior position where it would not create a conflict, then
    continue.  This keeps the algorithm correct and deterministic.

    The insertion step is needed because with equal counts (50 each) the greedy
    weighted-random sampler can exhaust all non-forbidden options before the
    last slot.  Insertion resolves the conflict without changing the multiset of
    items (each profile still appears exactly 50 times).

Reproducibility:
    Same seed → same schedule.  random.Random(seed) is used throughout;
    the sorted profile order in the allowed-set loop ensures no dict-ordering
    dependence.
"""

import random

PROFILES: tuple[str, ...] = (
    "B1_local",
    "B2_storage",
    "B3a_cold_orch",
    "B3b_compute",
    "full",
    "deliberate_failure",
)

PROFILE_COUNT    = len(PROFILES)
RUNS_PER_PROFILE = 50
TOTAL_RUNS       = PROFILE_COUNT * RUNS_PER_PROFILE   # 300

SCHEDULE_GENERATION_FAILED = "schedule_generation_failed"

# Canonical sort order used in allowed-set iteration so determinism does not
# depend on Python's dict ordering.
_SORTED_PROFILES: tuple[str, ...] = tuple(sorted(PROFILES))


class ScheduleGenerationError(RuntimeError):
    """Raised when the algorithm cannot produce a valid schedule.

    This should not occur for 6 × 50 runs, but the guard exists so the session
    refuses to start rather than silently violating the no-contiguous constraint.
    """
    error_code = SCHEDULE_GENERATION_FAILED

    def __init__(self, pos: int) -> None:
        super().__init__(
            f"schedule generation failed at position {pos}: "
            "no valid placement found"
        )
        self.position = pos


def _repair_pass(schedule: list[str]) -> list[str]:
    """Exposed for unit testing of the ScheduleGenerationError path.

    Applies a simple forward-scan repair on an already-built list.
    Raises ScheduleGenerationError if any conflict cannot be resolved by
    forward swap (which CAN happen for degenerate inputs).
    """
    n = len(schedule)
    for i in range(1, n):
        if schedule[i] == schedule[i - 1]:
            swapped = False
            for j in range(i + 1, n):
                if schedule[j] != schedule[i - 1]:
                    schedule[i], schedule[j] = schedule[j], schedule[i]
                    swapped = True
                    break
            if not swapped:
                raise ScheduleGenerationError(i)
    return schedule


def generate_schedule(prng_seed: str) -> list[str]:
    """Return a 300-entry run schedule seeded from prng_seed.

    The schedule is a list of profile name strings.  Same seed → same list.
    Adjacent entries are always distinct (no profile runs twice in a row).

    The seed string is used as-is with random.Random(); it is typically the
    session's prng_seed value from session_envelope.json.
    """
    rng = random.Random(prng_seed)
    counts: dict[str, int] = {p: RUNS_PER_PROFILE for p in PROFILES}
    schedule: list[str] = []
    last: "str | None" = None

    for pos in range(TOTAL_RUNS):
        # Build the sorted allowed list so iteration order is deterministic.
        allowed = [(p, counts[p]) for p in _SORTED_PROFILES
                   if counts[p] > 0 and p != last]

        if not allowed:
            # Only the forbidden profile has remaining runs.  Find the most
            # recent interior gap where it can be inserted without conflict.
            forbidden = last  # type: ignore[assignment]
            inserted = False
            for back in range(len(schedule) - 1, 0, -1):
                if schedule[back - 1] != forbidden and schedule[back] != forbidden:
                    schedule.insert(back, forbidden)
                    counts[forbidden] -= 1  # type: ignore[index]
                    inserted = True
                    break
            if not inserted:
                # Try the very front.
                if len(schedule) == 0 or schedule[0] != forbidden:
                    schedule.insert(0, forbidden)
                    counts[forbidden] -= 1  # type: ignore[index]
                    inserted = True
            if not inserted:
                raise ScheduleGenerationError(pos)
            last = schedule[-1]
            continue

        total_weight = sum(c for _, c in allowed)
        r = rng.randint(1, total_weight)
        cumulative = 0
        chosen = allowed[0][0]
        for p, w in allowed:
            cumulative += w
            if r <= cumulative:
                chosen = p
                break

        schedule.append(chosen)
        counts[chosen] -= 1
        last = chosen

    return schedule