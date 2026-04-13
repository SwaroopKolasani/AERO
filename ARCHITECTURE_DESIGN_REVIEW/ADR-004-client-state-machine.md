ADR-005: Client State Machine (Third Revision)
Status
Proposed

Context
Project AERO's v1 relies on a client-side orchestrator with an explicit, on-disk state machine to manage an ephemeral remote runner. Previous revisions (ADR-001 through ADR-004) narrowed the scope, defined the workload class, and established the auth/checksum model.

The previous state machine revision closed launch retry ambiguities and teardown/success conflation. However, an adversarial review identified three remaining gaps:

Sidecar Acceptance was implicit: Observing a sidecar is not the same as committing to its specific versioned payload.

Placement was under-fenced: Progress during multi-file output placement was not fenced before each individual rename operation, making partial failures unrecoverable without complex filesystem inspection.

Durability rationale was flawed: The previous explanation leaned on PIPE_BUF semantics, which apply to pipes, not regular files.

Furthermore, the review noted that 14 states represent near-maximum control-plane complexity for a solo-founder v1, strongly recommending that placement scope be narrowed if possible.

Decision
The client state machine is expanded to 14 states, explicitly addressing sidecar acceptance and fine-grained placement fencing.

Explicit Sidecar Acceptance (SIDECAR_ACCEPTED): After observing the sidecar, the client must explicitly persist the sidecar's exact bytes (or digest) and the output (key, VersionId, expected_sha256) tuple to the Write-Ahead Log (WAL). All subsequent recovery refers only to this versioned identity, ignoring any later overwrites.

Strict File Placement Fencing (PLACEMENT_PREPARED): Before touching user paths, the client validates all destination parents, ensures same-filesystem constraints (preventing EXDEV failures), prevalidates overwrite policies, and persists a complete rename plan.

Per-Rename Fencing: Each rename(2) operation is individually fenced with RENAME_INTENT and RENAME_DONE WAL records.

Corrected Durability Rationale: Durability relies exclusively on append-only WAL writes followed by fsync(2), including parent-directory fsync(2) calls upon WAL creation, lockfile creation, and after each successful rename.

Strict Runner Fencing: RUNNER_FENCED now waits for the EC2 instance to reach the terminated state, not just shutting-down.

Precondition Check: Bucket versioning is verified at client startup, failing immediately if disabled.

(Note: The ADR explicitly records a pre-build option to narrow v1 to a single output archive, which would eliminate PLACEMENT_PREPARED, per-rename fencing, and FAILED_PLACEMENT_PARTIAL.)

Alternatives considered
Relying on filesystem inspection for partial placement recovery (Rejected: Fragile and complex; WAL-driven recovery is deterministic).

Accepting EC2 shutting-down as a fenced state (Rejected: AWS documentation does not strictly guarantee no instance-originated writes can occur from this state; waiting for terminated is safer).

Falling back to copy on EXDEV (cross-mount-point) errors (Rejected: Violates atomic placement; v1 strictly requires the staging directory and destination to share a filesystem device ID).

Narrowing to single-output placement immediately (Deferred: Acknowledged as a viable pre-build decision to reduce solo-founder complexity, but the current state machine supports the full multi-file workload class if needed).

Why this choice won
Immutable Job State: Explicit sidecar acceptance prevents a compromised or lagging runner from altering the accepted job output via S3 overwrites.

Deterministic Recovery: Per-rename WAL records allow the client to perfectly reconstruct interrupted placements without guessing based on filesystem state.

Accurate Durability: Correctly relying on fsync(2) (including directory fsyncs) aligns with Linux filesystem guarantees.

Fail Fast: Verifying bucket versioning at startup and validating placement constraints before touching user files prevents messy, mid-operation failures.

What is in scope for v1
14-State Machine:

INIT

STAGED

INPUT_COMMITTED

LAUNCH_SPEC_FROZEN

LAUNCH_REQUESTED

LAUNCH_CONFIRMED

SIDECAR_OBSERVED (Sidecar version ID recorded).

SIDECAR_ACCEPTED (Bytes/digest and S3 VersionId committed to WAL).

RUNNER_FENCED (Waits for EC2 terminated).

OUTPUT_FETCHED_VERIFIED

PLACEMENT_PREPARED (Validation complete, plan persisted).

PLACEMENT_COMMITTED (Per-rename intent/done fencing).

DONE

Terminals: FAILED_CLEAN, FAILED_ORPHAN_SUSPECTED, FAILED_PLACEMENT_PARTIAL.

Per-Rename Fencing: WAL records for RENAME_INTENT, RENAME_DONE, and RENAME_FAILED.

Strict Durability: Append-only WAL with fsync(2) on files and parent directories.

Startup Bucket Check: Strict verification of S3 bucket versioning before initialization.

What is out of scope for v1
Filesystem inspection to determine placement progress (WAL is the sole authority).

Fallback copying for cross-filesystem output placement.

Automatically creating missing parent directories during placement (creating parents is ambient authority; v1 refuses).

Post-terminal cleanup within the FSM (garbage collection is an external sweep).

Key invariants
VersionId Primacy: After SIDECAR_ACCEPTED, the client only reads output objects by their explicit VersionId. Current S3 keys are ignored.

Atomic Rename: Output placement uses rename(2) and strictly enforces that staging and destination paths share the same device ID (statfs).

Append-Only WAL: The state machine log is strictly append-only; it is never rewritten in place.

Runner Death Confirmed: RUNNER_FENCED does not proceed until EC2 confirms terminated.

Risks
High Control-Plane Complexity: 14 states is near the upper limit for a solo founder to implement and operate reliably.

WAL Churn: Multi-file output placement generates WAL churn proportional to the output count.

Teardown Latency: Waiting for EC2 terminated adds latency to the critical path.

Blockers still to resolve
Single-Output Decision: Make a definitive pre-build choice on whether to narrow v1 to single-output placement (eliminating states 11, 12, and partial failures). Recommended: YES.

Exact Timeouts: Pick concrete numbers for polling intervals, sidecar deadlines, stale lock TTLs, and ClientToken horizons.

UX Definition: Write the exact CLI UX for aero resume and aero abandon for FAILED_PLACEMENT_PARTIAL.

WAL Schema: Freeze the WAL record schema, including a version field.

ADR-001 Latency Gate: Remains the overall project blocker before any build begins.

Consequences
Positive
Post-observation sidecar rewrites cannot compromise an accepted job.

Partial placement failures are 100% reconstructable without risky filesystem probing.

The durability mechanism aligns with actual OS-level fsync guarantees.

Missing bucket versioning fails safely at startup.

Negative
High implementation overhead for a solo founder.

Additional teardown latency (terminated wait) and disk I/O (per-rename fsync).

What would invalidate this ADR later
The 14-state machine proves too complex to implement or debug reliably, forcing the adoption of the single-output narrowing option.

The I/O cost of fsyncing parent directories during multi-file placement degrades local performance unacceptably.

EC2 terminated polling times out inconsistently, causing false FAILED_ORPHAN_SUSPECTED errors.