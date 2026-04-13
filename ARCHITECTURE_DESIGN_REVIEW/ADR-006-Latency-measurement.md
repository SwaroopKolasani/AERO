ADR-006: v1 Latency Measurement Plan 
Status
Proposed (Final for this topic)

Context
Previous revisions of the Latency Measurement Plan established total wall-clock targets, pinned fixtures, T_boot decomposition, and tightened completion-rate rules.

However, an adversarial review identified five remaining gaps that could allow a fundamentally flawed architecture to slip past the gate:

B3a Was Merely Diagnostic: Cold-path remote orchestration (zero compute, zero upload) is the core AERO tax. If it is too slow, the product is dead. It must be a hard sub-gate, not just diagnostic.

Ignored Failure UX: "Deliberate failure" lacked a hard wall-clock threshold, despite broken code being a normal user experience.

Vulnerable to 'Lucky' Networks: There were no formal rules to discard sessions artificially boosted by an exceptionally quiet residential network or a flawlessly fast EC2 control plane day.

Completion Rate Pliability: Even without phase-clustering, a 49/50 pass is statistically suspicious and lacked the skepticism applied to latency dead-zones.

Fixture Ambiguity: The "Realistic Minimal" fixture still relied on implicit elements rather than explicit, cryptographic commitments.

Decision
We promote B3a (cold remote orchestration) to a hard sub-gate. We establish a strict wall-clock limit for deliberate failures. We mandate rigid session invalidation rules based on network throughput and thermal events. We tighten the completion rate so that only 50/50 is a clean pass. We cryptographically freeze the Realistic Minimal fixture.

1. B3a as a Hard Sub-Gate
B3a (pre-uploaded inputs, zero-compute exit 0 workload) is now a primary gate.

Target: Total wall-clock < 25s p90, < 18s p50.

Consequence: If B3a fails, the product is dead. Failure is a STOP if dominated by T_boot_launch (EC2 bottleneck), or a REDESIGN otherwise.

2. Deliberate Failure Wall-Clock
Experiencing a fast failure when code is broken is critical to interactivity.

Target: Deliberate failure workload total wall-clock < 40s p90.

3. Session Invalidation Rules
A measurement session is invalid and discarded (but published for transparency) if any of the following occur:

Throughput Variance: iperf3 sustained upload/download at the start, middle, or end differs by > 25% from the session median.

Absolute Throughput Floor: Any measurement drops below 150 Mbps down or 10 Mbps up (violating the residential baseline assumption).

Thermal Event: Any thermal throttling detected on the laptop during B1 Local runs (session only continues if B1 can be cleanly re-run).

Regional Degradation: EC2 RunInstances p50 in the session exceeds 2x the 3-session rolling median. No cherry-picking data.

4. Completion-Rate Dead Zone (Extended)
Only 50/50 is a clean pass.

48/50 or 49/50 on any gate-driving workload is automatically INCONCLUSIVE, triggering 50 additional runs.

Zero Orphan Tolerance: A single FAILED_ORPHAN_SUSPECTED remains an instant gate failure.

Mandatory Postmortems: Every non-success run requires a formal postmortem record (phase attribution, WAL state, transfer-mode confirmation) before gate evaluation can proceed.

5. Frozen Realistic Minimal Fixture
The fixtures/realistic_minimal/README.md must contain strict, committed expectations before measurement begins:

Exact CPython version string (verified via sys.version).

Exact list of 20 stdlib modules.

Exact generator seed (0xAER0) for 10 input files, with committed SHA-256 hashes.

Committed script SHA-256.

Exact expected byte contents and SHA-256 hashes for all 5 outputs.

Any fixture drift during execution results in a fail_fixture, which counts against the completion rate.

6. Named Diagnostic Deltas
The decision report must explicitly publish the deltas between the diagnostic stack layers: B2 (Storage only) -> B3a (Cold Orchestration) -> B3b (+ Compute) -> Full AERO (+ Upload). Cold-path tax cannot be hand-waved away as compute time.

Alternatives considered
Leaving B3a as diagnostic (Rejected: If cold-orchestration is inherently slow, the product cannot be saved by optimizing uploads. It must be a hard fail).

Allowing 49/50 as a pass (Rejected: A 2% failure rate on a core control plane tool is unacceptable for a reliable developer experience).

Relying on "best effort" network measurements (Rejected: A residential network fluctuating between 50Mbps and 500Mbps destroys the statistical validity of the p90/p50 measurements).

Why this choice won
Unavoidable Truth: Making B3a a hard sub-gate forces the project to confront the raw tax of cloud orchestration immediately.

Statistical Integrity: Session invalidation rules prevent a "lucky" fast network day from rescuing a marginal architecture.

Zero Pliability: Cryptographically freezing the fixtures removes any temptation to tweak the workload to meet the latency targets after the fact.

What is in scope for v1
Hard B3a sub-gate (<25s p90).

Hard Deliberate failure target (<40s p90).

Strict session invalidation rules (Network variance, absolute floors, thermal events, EC2 degradation).

Extended completion-rate dead zone (50/50 required for clean pass).

Formal failure postmortems required for all non-success runs.

Cryptographically frozen Realistic Minimal fixture.

Mandatory transfer-mode artifacts for ≥100 MB runs (verifying multipart usage).

Explicit named deltas published in the decision report.

What is out of scope for v1
Manual judgment for session invalidation (must be automated in the rig).

Accepting 49/50 without a 50-run re-test.

Key invariants
B3a Stops Everything: A failure in B3a where T_boot_launch does not dominate results in an immediate REDESIGN. If T_boot_launch dominates, it is an immediate STOP.

Drift is Failure: Any deviation in the Realistic Minimal fixture's hashes at runtime constitutes a test failure.

Risks
Extremely High Effort: The strictness of session invalidation and the 50/50 requirement means many measurement sessions will be discarded, requiring significant time investment from the solo founder.

Cloud Weather: Intermittent EC2 or S3 latency spikes might continually invalidate sessions, delaying the start of actual product build indefinitely.

Blockers still to resolve
Rig Automation: Implement B3a, session invalidation checks, and postmortem generation as automated gates in the test rig.

Commit Fixtures: Commit the exact Realistic Minimal SHA-256 manifest to the repository.

Publish Matrix: Publish the final, comprehensive decision matrix in writing.

Consequences
Positive
The measurement gate is now as rigorous as an enterprise performance test, guaranteeing that if AERO passes, it is genuinely viable.

There is no room for confirmation bias or hand-waving poor results.

Negative
The measurement phase is now a massive, expensive undertaking.

The 50/50 completion requirement is unforgiving; a single dropped packet could ruin a 49-run streak.

What would invalidate this ADR later
N/A. This is the final, definitive measurement plan.

Next linked design topics
EXECUTE LATENCY MEASUREMENT PLAN. No implementation work on ADR-001 through ADR-006 may begin until this gate runs and produces a GO or GO WITH NARROWING on valid sessions.