# AERO Latency Rig — Canonical Architecture Specification

### 1. Purpose

The latency rig exists to produce a defensible go/no-go decision on whether the frozen AERO v1 architecture clears the latency gate defined in the product-side ADR-006, before any product implementation begins.

**What it proves, if it passes:** that on a residential-class network from a macOS laptop, a randomly-ordered, cryptographically-pinned workload mix against real AWS, with atomic integrity evidence, zero-tolerance orphan sweeping, and an independent completion anchor, meets the p50/p90 wall-clock thresholds and the 50/50 completion rule for each profile — including B3a as a hard sub-gate and the deliberate-failure ceiling.

**What business risk it tests:** that the AERO v1 cold-orchestration tax, end-to-end payload-on-boot path, and teardown-to-terminated latency are compatible with interactive developer experience.

**What it is not:** a performance tuning tool, a regression harness, a competitive benchmark, a debugging aid for the product, a CI gate, or the start of a product-side observability layer. It is a one-purpose auditor whose output is a sealed session directory and a contentless decision report.

### 2. Scope

**In scope:**

- Driving the AERO CLI as a black-box subprocess under randomized, cryptographically-pinned workloads.
- External monotonic wall-clock capture as the sole gate timer.
- Reading, but never writing, the product WAL for diagnostic phase decomposition.
- Direct out-of-band AWS API calls (boto3) for independent completion anchoring and orphan sweeping.
- Hash-chained append-only evidence: manifest events, allocation registry, closure hash inventory.
- Atomic writes, state-transition validation, crash-recovery adoption, completeness fail-closed.
- Bounded outcome classification and mechanical decision-matrix evaluation.
- A rig self-test suite that must pass before any real session counts.
- A host-adapter layer isolating macOS-specific suitability checks.

**Out of scope (must not accidentally become product implementation):**

- Any modification of AERO product code, WAL schema, supervisor, runner image, or CLI.
- Any cache, memoization, or content-addressed layer.
- Any library API, web UI, or hosted control plane.
- Any retry logic, override flag (beyond `--adopt-stale-lock`), or tolerant-comparison mode.
- Any feature explicitly deferred in ADR-001 through ADR-005.
- Any cross-session statistics beyond single-session gate evaluation.
- Any mutation of closed sessions.
- CI/CD integration, TSDB, dashboards, alerts.

### 3. Test Topology

| Role | Location | Responsibility |
|------|----------|----------------|
| **Rig host** | macOS laptop | Runs the rig orchestrator, drives the CLI subprocess, captures wall-clock, writes all evidence, queries AWS out-of-band, finalizes sessions. Sole writer of session directories. |
| **AERO client under test** | Same laptop, launched as subprocess | The black box. Frozen binary, hash pinned. Reads its own product WAL from disk; the rig reads that WAL read-only. |
| **Object storage** | AWS S3 staging bucket, user-owned, versioning enabled, lifecycle pinned per ADR-005 | Holds input archive, output archive, commit sidecar. Rig calls `HeadObject` directly for independent anchor. Rig queries inventory four times per session (pre, during-registry, post, delayed). |
| **Runner** | Ephemeral EC2 VM, payload-on-boot, pinned AMI digest | Launched by the CLI under test, not by the rig. Rig never provisions the runner directly. Rig observes only via AWS API and product WAL. |
| **Observer / probe** | Rig's `preflight` module | Captures host fingerprint, host suitability, timer sanity, fixed-protocol network probe, fixture verification. Runs at preflight and at midflight/postflight with a shared schema. |
| **Result analysis** | Rig's `evaluator` module | Runs only after `completeness` validator passes. Produces `cause_bundle.json` and a contentless `decision_report.json`. |

Single-host topology. No second machine. No distributed coordinator.

### 4. Measurement Phases

The end-to-end path from CLI invocation to user-visible completion is decomposed into **named, stable phases**. Wall-clock is measured externally across the whole path. Phase decomposition is derived from the product WAL read-only and is tagged `trust_level: "diagnostic"`.

**Gate timer (authoritative):**

- `T_wallclock` = `CLOCK_MONOTONIC` span from `subprocess.Popen` return to `subprocess.wait()` return in the rig process.

**Decomposed phases (diagnostic only):**

| Phase | Start event | End event | Notes |
|-------|-------------|-----------|-------|
| `T_local_prep` | CLI invocation | WAL `INPUT_COMMITTED` | Local validation, work-spec freeze, input discovery |
| `T_input_archive_build` | `STAGED` | `INPUT_COMMITTED` | Restricted-ZIP archive creation (ADR-003) |
| `T_input_upload` | `INPUT_COMMITTED` | `LAUNCH_SPEC_FROZEN` | Single-part PUT, checksum tied to VersionId |
| `T_launch_request` | `LAUNCH_SPEC_FROZEN` | `LAUNCH_REQUESTED` | Rig also records boto3 `RunInstances` DryRun latency in preflight for baseline |
| `T_boot_launch` | `LAUNCH_REQUESTED` | `LAUNCH_CONFIRMED` | EC2 control-plane admission. This is the phase that determines `STOP` vs `REDESIGN` on B3a failure per ADR-006. |
| `T_boot_userdata` | `LAUNCH_CONFIRMED` | first observable supervisor heartbeat (via sidecar observation) | Covers boot, user-data handoff, supervisor start |
| `T_execution` | supervisor start | workload exit | Covers runner workload; near zero for B3a |
| `T_output_upload` | workload exit | `SIDECAR_OBSERVED` | Output archive PUT |
| `T_sidecar_commit` | `SIDECAR_OBSERVED` | `SIDECAR_ACCEPTED` | Rig also captures the independent `HeadObject` result here (§7) |
| `T_termination` | `SIDECAR_ACCEPTED` | `RUNNER_FENCED` | Waits for EC2 `terminated` per ADR-004 |
| `T_local_verify_placement` | `OUTPUT_FETCHED_VERIFIED` | `DONE` | Local hashing and single-output placement (rig assumption) |

The evaluator publishes the **named diagnostic deltas** required by ADR-006: `B2 → B3a → B3b → full`, computed from the per-profile phase sums. These are published in `cause_bundle.json` only.

### 5. Fixture Architecture

Fixtures live in `fixtures/` and are cryptographically pinned. The rig refuses to start any session if fixture verification fails.

**Fixture types (all pinned):**

- **Realistic Minimal** — the primary fixture per ADR-006. Manifest pins: CPython version string, exact list of 20 stdlib modules, generator seed `0xAER0`, 10 input files with SHA-256, script SHA-256, 5 expected outputs with SHA-256, and `fixture_semver`.
- **Breakeven workloads** — workloads calibrated so the break-even point with local execution sits near the deliberate-failure ceiling. Used to produce the B3b and full-path measurements.
- **Deliberate failure workload** — a workload whose program exits non-zero via a pinned path. The rig expects a specific non-zero exit code and a specific failure oracle; any other exit is `fail_fixture`.
- **Zero-compute / orchestration-isolation workload (B3a)** — pre-uploaded inputs, zero-compute `exit 0`. Used to isolate cold-orchestration tax.

**Fixture manifest fields (mandatory, per fixture directory):**

```
fixture_semver
cpython_version
stdlib_modules[]
generator_version
generator_cmdline
seed
inputs[{path, sha256, size}]
script_sha256
expected_outputs[{path, sha256, size}]
failure_oracle { expected_exit_code, expected_stderr_substring_sha256 }
schema_version
```

**Fixture drift** is defined as **any** mismatch at session start or mid-session between the committed manifest and the bytes on disk, or between the workload's observed outputs and the manifest's `expected_outputs`. Drift produces `fail_fixture`, counts against the 50/50 completion rule, and is an instant gate failure if detected at session start.

**Regeneration rule:** fixtures are regenerated only by a pinned `rig fixture regenerate` command that runs the pinned generator, writes a new manifest with a new `fixture_semver`, and mechanically emits a `fixture_incompatibility.json` marking all prior sessions non-comparable. There is no human-authored fixture mutation path.

### 6. Measurement Environment Controls

All environment controls are implemented behind the **host adapter layer** (`rig/host/macos.py`) so a future Linux adapter can be added without architectural change.

**Machine controls (macOS canonical):**

- AC power required; battery → `fail_invalid_session_context`.
- No CPU governor transition during session (`pmset` snapshot).
- Background load sample (load average, process count snapshot).
- Memory pressure sample via `memory_pressure`.
- Disk free percentage floor (configurable ceiling, refuses below threshold).
- Sleep / lid events monitored via `CLOCK_BOOTTIME` vs `CLOCK_MONOTONIC` delta; any drift mid-run invalidates that run.

**Thermal protocol:**

- Thermal state captured at preflight, midflight, postflight.
- Any thermal throttling event detected during a B1 Local run invalidates the session (per ADR-006).
- Thermal samples recorded as raw + normalized.

**Network controls:**

- Fixed-protocol network probe (pinned S3 endpoint, fixed payload size, fixed request count). Not iperf3.
- Absolute floor: any sample below the floor → session → `INVALID` with `network_floor_breach`.
- VPN / route-table state captured; any change mid-session invalidates the session.
- Residential baseline assumptions from ADR-006 are encoded as configurable floors.

**Invalid session criteria (bounded, causally named):**

- `host_suitability_breach` (named disqualifier from adapter)
- `network_floor_breach`
- `thermal_event`
- `timer_sanity_breach` (suspend/resume, clock source change, implausible elapsed)
- `fixture_drift`
- `aws_api_hard_failure` (boto3 non-retryable)
- `cloud_weather_drift` (the 3-session EC2 rolling median rule, **only** when corroborated by at least one other causal signal above)
- `state_transition_illegal`

**Cloud-region / provider controls:**

- One region, pinned per session in `session_envelope.json`.
- One AWS account, pinned.
- STS credential lifetime recorded at preflight; if shorter than planned session duration plus margin, refuses to start.

**Recorded for reproducibility (mandatory in every session):**

- AERO client SHA-256
- Runner AMI digest
- Rig git commit + `rig_self_sha256`
- Python version + package lock hash
- Fixture manifest SHA-256 + `fixture_semver`
- Host fingerprint
- Self-test result hash
- PRNG seed
- Region, account ID hash

### 7. Instrumentation Architecture

**Timestamp sources (authoritative):**

- `T_wallclock`: rig-process `time.monotonic_ns()` around subprocess.
- Every other rig-captured timestamp uses the same monotonic clock with its resolution and source recorded per run.

**Timestamp sources (diagnostic, low-trust):**

- Product WAL timestamps, read-only. Used to compute phase decomposition. Every phase artifact is tagged `trust_level: "diagnostic"`.
- AWS API response timestamps (boto3 request IDs preserved).

**Raw evidence, preserved per run:**

- `cli_stdout.raw` + `cli_stdout.sha256` (capture-time digest)
- `cli_stderr.raw` + `cli_stderr.sha256`
- `wal_raw.bin` + `wal_raw.sha256`
- `head_object_raw.jsonl` (full boto3 response envelopes)

**Derived evidence:**

- `wal_events.jsonl` — normalized projection, own schema
- `phases.json` — phase deltas, diagnostic-tagged
- `outputs_audit.json` — expected/observed/missing/extra/per-file
- `completion_proof.json` — four-tier trust structure

**Correctness verification records:**

- `outputs_audit.json` is symmetric for success and non-success runs.
- `completion_proof.json` is required on every run; its absence blocks evaluation.

**Orphan detection records:**

- `allocation_registry.jsonl` — hash-chained, append-only, first-class.
- `sweep/inventory_{pre,post,delayed}_raw.jsonl` + normalized counterparts.
- `sweep/correlation.json` — the sweeper's verdict.

**Run record schema (symmetric across success and failure):**

```
run_identity
outcome_code                (bounded, exactly one)
wallclock_sha256
raw_artifacts: {stdout_sha256, stderr_sha256, wal_raw_sha256}
completion_proof_sha256
outputs_audit_sha256
classifier_reason
```

**Postmortem records:** every non-success run gets a postmortem derived mechanically from the run record and phase artifacts. The postmortem contains no free-form text — only bounded fields: `dominant_failing_phase`, `wal_terminal_state`, `transfer_mode` (for ≥100 MB runs), `exit_classification`, `invalidation_ref_if_any`.

### 8. Session Validity Model

**Session states (legal transitions only):**

```
OPEN → RUNNING → FINALIZING → CLOSED
{OPEN, RUNNING, FINALIZING} → INCOMPLETE
RUNNING → INVALID
```

Any attempt at an illegal transition produces a `state_transition_illegal` event, and the session is marked `INVALID`.

**Definitions:**

- **Valid session:** reached `CLOSED`, completeness validator passed, no orphan found, no invalidation reason recorded, reconciliation clean.
- **Invalid session:** reached a terminal state but recorded at least one causal invalidation reason. Preserved on disk, published for transparency, excluded from gate evaluation.
- **Incomplete session:** crashed mid-session or failed to finalize. Sealed as `INCOMPLETE` by the crash-recovery adopter. Non-evaluable, but still runs the delayed sweep before sealing so orphan evidence survives.
- **Discarded session:** never exists as a concept. Sessions are preserved, not discarded. Invalid and incomplete sessions are published alongside valid ones.
- **Published-but-invalid:** the default for invalid and incomplete sessions. Every session directory, regardless of verdict, is part of the permanent record.

**Rerun eligibility codes (bounded):**

- `host_interrupted`
- `rig_crash`
- `aws_api_failure`
- `session_incomplete`
- `network_floor_breach`
- `self_test_failed`

No other reason justifies a rerun. "Looked weird" is not a reason.

### 9. Gate Logic and Decision Matrix

Applied mechanically by `evaluator.py`. Table-driven. Every cell has a golden test.

**Per-profile thresholds (from product ADR-006, frozen):**

| Profile | Target | Hard gate? |
|---------|--------|------------|
| `B3a_cold_orch` | p90 < 25s, p50 < 18s | Yes — hard sub-gate |
| `deliberate_failure` | p90 < 40s | Yes |
| `full` | total wall-clock p90 as primary gate alongside overhead tax | Yes |
| `B2_storage`, `B3b_compute`, `B1_local` | Used to compute named diagnostic deltas | Diagnostic, but contribute to narrowing logic |

**Completion rule:** per ADR-006, only 50/50 per profile is a clean pass. 48/50 or 49/50 → `INCONCLUSIVE`, triggers 50 additional runs. Anything worse → `INCONCLUSIVE` at minimum, and typically `STOP`.

**Orphan tolerance:** zero. Any orphan in the sweep correlation → instant gate failure regardless of latency.

**Fixture drift:** any drift → instant gate failure.

**Clustered failure rule:** `clustered_failures` is defined as the count of maximal contiguous non-success runs in session execution order. Threshold: any cluster of length ≥ 3, or total clustered runs ≥ 5% of session. Above threshold → `INCONCLUSIVE`.

**Decision matrix:**

| Condition | Verdict |
|-----------|---------|
| All profiles 50/50, all latency gates clear, no orphan, no invalidation, no fixture drift, reconciliation clean, completeness clean | `GO` |
| Same as `GO` but at least one profile is marginal and narrowing (single-output, etc.) brings it under target | `GO_WITH_NARROWING` |
| Any profile 48/50 or 49/50 | `INCONCLUSIVE` → mandatory 50 additional runs |
| Clustered failures above threshold | `INCONCLUSIVE` |
| B3a p90 ≥ 25s **and** `T_boot_launch` dominates B3a wall-clock | `STOP` |
| B3a p90 ≥ 25s **and** `T_boot_launch` does not dominate | `REDESIGN` |
| Deliberate-failure p90 ≥ 40s | `STOP` |
| Any `fail_orphan_suspected` | Instant gate failure (reported as `STOP` unless latency evidence explicitly contradicts) |
| Any fixture drift | Instant gate failure |
| Completeness validator fail | `INCOMPLETE` (not evaluable) |
| Any causal invalidation reason recorded | `INVALID` (not evaluable) |

**What dominates the final decision:**

1. Completeness / reconciliation / state-machine legality (gating, fail-closed).
2. Orphan / fixture drift (zero tolerance).
3. B3a sub-gate (`STOP` vs `REDESIGN` path per ADR-006).
4. Completion-rate dead zone.
5. Deliberate-failure ceiling.
6. Full-path wall-clock thresholds.
7. Clustered-failure threshold.

Human interpretation is limited to **strategic response** to the verdict (pursue narrowing, abandon, revise). Reclassification of evidence is not permitted.

### 10. Automation Architecture

**Must be automated (code, not judgment):**

- Fixture verification and hash check at session start
- Host fingerprint capture and drift refusal
- Self-test suite execution, result hash recorded per session
- Preflight host suitability, timer sanity, network probe
- PRNG-seeded interleaved run schedule
- Subprocess driver with single monotonic timer
- Raw evidence capture with capture-time digests
- Per-run temp sweep
- Product WAL read-only copy and parsing
- Output auditing and hashing
- Completion proof construction (builder)
- Completion proof verification (separate verifier module, no shared code)
- Four-phase AWS inventory snapshot
- Independent `HeadObject` anchoring
- Allocation registry appends and hash chain
- Sweep correlation
- Bounded outcome classification (run and session)
- Invalidation rule application (including the corroboration-required `cloud_weather_check`)
- Manifest events → projection (single-writer orchestrator)
- State transition validator
- Reconciliation (manifest ↔ events ↔ disk)
- Completeness validator (fail-closed)
- Percentile math (p50, p90)
- Clustered-failure computation
- Cause bundle generation
- Decision report emission (contentless)
- Finalizer: Merkle hash inventory, cross-linked closure, seal
- Crash-recovery adopter: stale lock detection, event replay, crash classification, delayed sweep before sealing `INCOMPLETE`

**May remain manual:**

- Strategic response to a verdict (pursue, narrow, abandon).
- Committing a new host baseline via `rig baseline capture` (but the baseline itself is machine-generated; the human commits the diff, cannot edit fields).
- Committing a new fixture via `rig fixture regenerate` (same pattern).
- Reading a published invalid/incomplete session to decide whether to rerun (but the rerun requires a bounded eligibility code).

### 11. Output Artifacts

Per-session directory layout (write-once, sealed at close):

```
sessions/<session_id>/
  session_manifest.json
  manifest_events.jsonl
  session.lock
  session_envelope.json
  host_fingerprint.json
  host_baseline_diff.json
  timer_sanity.json
  self_test_result.json
  preflight/
    fixture_verify.json
    host_suitability.json
    network_probe_raw.json
    network_probe.json
    inventory_pre_raw.jsonl
    inventory_pre.json
  allocation_registry.jsonl
  runs/
    run_<ordinal>_<profile>_<uuid>/
      identity.json
      invocation_envelope.json
      wallclock.json
      cli_stdout.raw
      cli_stdout.sha256
      cli_stderr.raw
      cli_stderr.sha256
      wal_raw.bin
      wal_raw.sha256
      wal_events.jsonl
      phases.json
      outputs_audit.json
      completion_proof.json
      temp_sweep.json
      run_record.json
      postmortem.json        # present for non-success; mechanically derived, bounded fields only
  midflight/
    network_probe_raw.json
    network_probe.json
    host_sample.json
  postflight/
    network_probe_raw.json
    network_probe.json
    host_sample.json
    inventory_post_raw.jsonl
    inventory_post.json
    inventory_delayed_raw.jsonl
    inventory_delayed.json
  sweep/
    head_object_raw.jsonl
    correlation.json
  invalidation.json
  reconciliation.json
  completeness.json
  cause_bundle.json
  decision_report.json
  closure.json
```

**Artifact authority rules:**

- `manifest_events.jsonl` is ground truth while the session is open.
- `session_manifest.json` is a materialized projection, authoritative while `{OPEN, RUNNING, FINALIZING}`.
- `closure.json` is authoritative for the sealed full-tree hash inventory.
- At `CLOSED`, the manifest and closure cross-reference each other's hashes.
- `decision_report.json` is contentless: `{session_id, verdict, cause_bundle_sha256, completeness_sha256, closure_sha256, rig_self_sha256, git_commit, timestamp}`.

**Cross-session artifacts:**

- `fixtures/realistic_minimal/MANIFEST.json` (and equivalents for breakeven, deliberate-failure, B3a fixtures)
- `pinned/aero_client.sha256`, `pinned/runner_ami.digest`, `pinned/rig_env.lock`, `pinned/rig_self.sha256`
- `docs/RUNBOOK.md`, `docs/DECISION_MATRIX.md`, `docs/OUTCOME_CODES.md`, `docs/SCHEMA_VERSIONS.md`

**Diagnostic deltas:** published in `cause_bundle.json` only. Not in the decision report.

### 12. Repository / Module Structure

```
aero-rig/
  pyproject.toml
  pinned/
    aero_client.sha256
    runner_ami.digest
    rig_env.lock
    rig_self.sha256
  fixtures/
    realistic_minimal/
    breakeven/
    deliberate_failure/
    b3a_orchestration_isolation/
  rig/
    cli.py                    # rig run, selftest, baseline capture, fixture regenerate, reconcile, adopt
    atomic.py                 # single atomic writer library
    schemas/                  # versioned JSON schemas, one per artifact type
    manifest.py               # events, projection, single-writer lock, state validator
    identity.py               # session/run identity, host fingerprint
    host/
      __init__.py             # host adapter interface
      macos.py                # canonical host adapter
      # linux.py              # future, not v1
    preflight.py              # fixture verify, host suitability, timer sanity, network probe
    allocation.py             # first-class registry
    sweeper.py                # four-phase inventory, HeadObject anchor, correlation
    envelope.py               # session-level and run-level envelopes
    driver.py                 # randomized schedule, subprocess driver, wallclock, per-run temp sweep
    wal_raw.py                # raw WAL copy only
    wal_parse.py              # normalized events + phase derivation
    auditor.py                # output discovery and hashing
    completion.py             # four-tier completion proof builder
    completion_verify.py      # separate verifier, zero shared code with builder
    outcome.py                # bounded outcome classifier
    invalidator.py            # causally-named reason codes including cloud_weather_check
    completeness.py           # fail-closed validator
    evaluator.py              # p50/p90, cluster math, decision matrix, cause bundle, decision report
    finalizer.py              # Merkle hash inventory, cross-linked closure, seal
    selftest/
      goldens/
      test_*.py
  sessions/
  docs/
    RUNBOOK.md
    DECISION_MATRIX.md
    OUTCOME_CODES.md
    SCHEMA_VERSIONS.md
```

Strict rules:

- `completion.py` and `completion_verify.py` share no code beyond schema imports.
- All writes go through `atomic.py`. No component writes files any other way.
- Only `manifest.py`'s orchestrator writes `session_manifest.json`.
- Host-specific code lives only under `rig/host/`. Other modules import `host.current()` and call adapter methods.

### 13. Failure Model

Every failure mode below is named, testable, and has a bounded outcome code.

| Failure | Rig handling |
|---------|--------------|
| Instrumentation missing (WAL file absent) | Run → `fail_missing_wal`. Counts against 50/50. Postmortem generated. |
| Phase timestamps unavailable | Phase artifact still written with absent fields tagged; run outcome is not derived from phase data, so it does not by itself fail the run. Evaluator records `dominant_failing_phase: unknown`. |
| Product WAL schema unknown | Session → `INVALID` with `state_transition_illegal`'s cousin `unknown_wal_schema_version`. No attempt to parse. |
| Fixture mismatch at preflight | Session refuses to start. No session directory created beyond the fixture-verify record. |
| Fixture drift mid-session | Run → `fail_fixture`. Instant gate failure at session level. |
| Network floor breach | Session → `INVALID` with `network_floor_breach`. Published. |
| EC2 degradation (hard API failure) | Session → `INVALID` with `aws_api_hard_failure`. |
| EC2 rolling median drift | `cloud_weather_check` records drift; by itself does not invalidate; combined with any other causal signal, invalidates. |
| Orphan detection ambiguity | Sweeper uses allocation registry as authoritative input. Any unmatched resource → `orphan_suspected`. Instant gate failure. No "ambiguous" state. |
| Correctness mismatch (output hash vs manifest) | Run → `fail_audit`. Postmortem generated. |
| Completion proof tier disagreement | Run → `fail_completion_proof`. |
| `HeadObject` independent anchor fails | Run → `fail_completion_proof`. No fallback. |
| Partial result artifact loss (crash mid-write) | Atomic writer guarantees no partial visible files. Crash recovery seals session as `INCOMPLETE` after running the delayed sweep. |
| Subprocess timeout exceeded | Run → `fail_timeout`. Counts against 50/50. |
| Stdout or stderr > 16 MiB | Run → `fail_rig_error` with truncation flag. Not silently truncated. |
| Lingering temp file after a run | Run → `fail_rig_error` (per-run temp sweep). |
| Host fingerprint drift mid-session | Session → `INVALID`. |
| Suspend/resume mid-run | Run → `fail_invalid_session_context`. Counts against 50/50. Timer sanity record captures it. |
| Multi-output placement encountered (under current single-output assumption) | Run → `fail_rig_error` with reason `multi_output_not_supported`. The rig does not expand to handle it silently. |
| Rig self-test suite fails | Session refuses to start. |
| Reconciliation mismatch | Session → `INCOMPLETE`. Evaluator refuses. |

### 14. Risks and Simplifications

**Risks (residual, accepted):**

- **Laptop host noise.** Host suitability checks only detect disqualifiers, not cleanliness. Documented in the runbook. Mitigation is a dedicated host, never looser rules.
- **Coupling to product WAL schema.** Unknown schema version refuses to parse. The rig churns when the product WAL schema moves; this is accepted since the product WAL is gated before build.
- **Coupling to product completion-acceptance protocol.** Independent `HeadObject` anchor reduces but does not eliminate this. Pinned schema versions for `completion_proof.json`.
- **Four-phase sweep cost.** Measurable latency and API cost. Accepted because zero-orphan is frozen product-side.
- **Rolling EC2 median retained as corroborating signal only.** Partially honors the product ADR-006 text without letting weak statistics single-handedly invalidate. This is the one place where the rig is not a pure mechanical mirror of the ADR text.
- **Self-test halo effect.** A passing self-test proves regression resistance, not real-world completeness. Runbook explicitly warns.
- **Cloud weather starvation.** Strict invalidation may discard many sessions. Remedy is patience, not relaxation.

**Intentional simplifications:**

- Single-output archive semantics (pending ADR-004 narrowing freeze).
- One region, one account, one host.
- No retry logic.
- No overrides except `--adopt-stale-lock`.
- No dashboards, no TSDB, no CI gate, no mocked AWS in the real path.
- `hyperfine` is not present anywhere.

**Cuts if implementation complexity rises:**

- The `cloud_weather_check` corroboration layer is the first thing to drop if it proves operationally brittle; the rig degrades gracefully by removing one invalidation signal. (Cutting it requires a product-side ADR-006 revision, not a rig-side override.)
- The breakeven fixture is the second simplification target; B3a, deliberate failure, and full-path are the non-negotiable profiles.
- Midflight host sampling can be thinned to start/end if postflight proves sufficient.

**Where the rig stays narrower than the product:**

- The rig never writes to the product's WAL, bucket, or runner.
- The rig does not implement the restricted ZIP parser; it uses whatever the product emits and verifies hashes only.
- The rig does not implement TCB change governance; it only records the AMI digest and refuses unknown digests.
- The rig does not implement cleanup-of-real-orphans; it only detects and fails the gate.

---

## Part 4 — Unresolved Blockers

These block full implementation readiness of the rig. Each is parameterized in the rig architecture; the rig refuses unknown schema versions and unknown fixture versions.

**Product-side blockers (external to the rig):**

1. **Product WAL schema freeze (ADR-004).** The rig's `wal_parse.py` is keyed to a specific WAL schema version. Until frozen, the rig cannot ship goldens for WAL parsing. Parameterized: rig refuses unknown `wal_schema_version`.
2. **Realistic Minimal fixture freeze (ADR-006).** Until the fixture manifest is committed with final SHA-256 values, the rig's fixture verification has placeholder hashes and cannot run real sessions. Parameterized: fixture manifest is loaded by path, not hardcoded.
3. **Single-output placement decision (ADR-004).** The rig adopts single-output. If reversed, the output auditor, completion proof, and per-run schemas expand to multi-file. Parameterized: `outputs_audit.json` already supports an array of files, so the schema survives; the evaluator and completion proof would need a multi-entry path.
4. **Exact numerical ceilings for workload class (ADR-002).** Resource profile names and VM shape mappings. The rig parameterizes this as a config; it does not hardcode profile-to-shape tuples.
5. **Runner AMI digest pinning (ADR-003).** Until the pinned AMI digest is committed, the rig's `runner_ami.digest` is a placeholder and the `HeadObject` anchor cannot validate the runner-image-to-session linkage.
6. **Bucket lifecycle schema (ADR-005).** The rig enforces the exact-schema lifecycle check at preflight. Until the schema is frozen in the product ADR, the rig's check uses the fields named in ADR-005 verbatim but cannot validate the numeric values.
7. **Decision matrix publication (ADR-006).** The product ADR names the matrix but has not committed the full cell table. The rig encodes the cells documented in §9 above; any product-side revision requires a rig code update.
8. **Final decision on cloud-weather corroboration rule.** The reconciled rig rule (§9 / §E) partially honors ADR-006. A product-side revision to either strengthen or cut the rolling median requirement would let the rig drop the corroboration layer cleanly.

**Rig-side items that are not blockers but must be decided before coding:**

- Exact network probe payload size and request count (configurable, needs a pinned value).
- Exact delayed-sweep cooldown window (default 300s, needs commitment).
- Exact stdout/stderr capture ceiling (default 16 MiB, needs commitment).

---

## Part 5 — Implementation Order (Sequential, Non-Negotiable)

A short ordered phase list. The full task backlog remains the authoritative per-task spec and is not duplicated here.

**Phase 0 — Foundation**

- Repo skeleton, pins, `pyproject.toml`.
- `rig/atomic.py` and its failure tests.
- `rig/schemas/` with one JSON Schema per artifact type.
- `rig/selftest/` harness skeleton.

**Phase 1 — Manifest protocol**

- `manifest_events.jsonl` append + hash chain.
- `session_manifest.json` materialized projection.
- Single-writer lock with stale-lock semantics.
- State transition validator.
- Reconciliation function.

**Phase 2 — Identity, host, preflight**

- Session and run identity.
- Host fingerprint (behind `rig/host/macos.py`).
- Timer sanity capture.
- Host suitability checks.
- Fixed-protocol network probe.
- Fixture verify.

**Phase 3 — Allocation registry and AWS evidence**

- First-class `allocation_registry.jsonl`.
- Four-phase AWS inventory with raw and normalized artifacts.
- Independent `HeadObject` anchor.
- Sweep correlation.

**Phase 4 — Driver and raw capture**

- Session and run envelopes.
- PRNG-seeded randomized run schedule.
- Subprocess driver with single monotonic timer.
- Raw stdout/stderr capture with capture-time digests.
- Per-run temp sweep.

**Phase 5 — WAL, audit, completion proof**

- `wal_raw.py` (read-only copy).
- `wal_parse.py` (normalized events + phase derivation).
- Output auditor.
- Completion proof builder.
- Separate completion proof verifier module.

**Phase 6 — Outcome, completeness, invalidation**

- Bounded outcome classifier.
- Invalidator with causally-named reason codes.
- Completeness validator (fail-closed).

**Phase 7 — Evaluation and finalization**

- Percentile and cluster math.
- Cause bundle generator.
- Decision matrix evaluator.
- Contentless decision report.
- Finalizer with Merkle hash inventory and cross-linked closure.
- Crash-recovery adopter.

**Phase 8 — Self-test completion**

- Goldens for parser, sweeper, invalidator, evaluator, decision matrix cells.
- Corruption, crash, fail-closed tests.
- Deliberate-failing canary profile.

**Phase 9 — CLI and runbook**

- `rig` CLI entrypoint and subcommands.
- `RUNBOOK.md`, `DECISION_MATRIX.md`, `OUTCOME_CODES.md`, `SCHEMA_VERSIONS.md`.

Each phase must complete its failure tests before the next begins. Parallel build is prohibited.

The full per-task backlog — including acceptance criteria, failure-mode tests, prohibited-scope items, and per-module schemas — is the canonical task spec referenced by this architecture. It is not re-embedded here to keep this document architectural.

---

## Part 6 — Do-Not-Exceed Boundary

The rig must not, under any circumstances, grow any of the following. Each is a guardrail against silently becoming a product, a dashboard, or a debugging tool.

**Product boundary:**

- No modification to AERO product code, WAL schema, supervisor, runner image, CLI, or bucket layout.
- No feature deferred in ADR-001 through ADR-005 (cache, library API, multipart, listener, env vars, multi-cloud, etc.).
- No cleanup or remediation of real orphans. Detection and gate failure only.
- No writing to the product's WAL or staging bucket beyond what is required to place inputs for the workload under test.

**Evidence boundary:**

- No second timing source. `hyperfine` is not in the repo.
- No in-runner timestamps used for gate timing.
- No merging of raw and derived evidence.
- No narrative or summary fields in `decision_report.json`.
- No free-form fields in postmortems or the cause bundle.
- No convenience helpers that bypass `atomic.py`.
- No shared code between `completion.py` and `completion_verify.py` beyond schema imports.

**Control boundary:**

- No retries.
- No override flags except `--adopt-stale-lock`.
- No resume-execution after crash. Only resume-evidence.
- No mutation of closed sessions.
- No cross-session statistics beyond single-session gate evaluation.
- No human reclassification of evidence.
- No "tolerant" or "fuzzy" comparison modes.

**Operational boundary:**

- No CI/CD integration.
- No dashboards, TSDB, alerts, or web UI.
- No mocked AWS in the real session path. Mocking is self-test only.
- No distributed or concurrent load.
- No host other than the canonical macOS laptop unless a Linux host adapter is added under `rig/host/` without other changes.
- No parallelization of the build sequence.

**Scope-drift boundary:**

- The `cloud_weather_check` corroboration rule is the only place where the rig interprets rather than mirrors the product ADR. No further interpretation is permitted. Any other gap between ADR-006 and rig behavior requires a product-side ADR revision.
- The single-output assumption is the only architectural assumption the rig makes on behalf of a still-open product-side decision. No further pre-emptive adoption is permitted.

Anything that feels like it needs one of these boundaries relaxed is a signal to stop, re-read this document, and surface the question — not to relax the boundary.

---

**End of canonical architecture specification.**