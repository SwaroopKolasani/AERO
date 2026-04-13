ADR-005: v1 Orphan Accounting and Output Placement
Status
Proposed (Final for this topic)

Context
Previous revisions (ADR-001 through ADR-005) removed broad account sweeps, narrowed cleanup strictly to the per-job Write-Ahead Log (WAL), and mandated single-output archive placement.

However, an adversarial review identified four remaining risks in the proposed cleanup and placement models:

Imprecise S3 Deletion: Deleting "current versions" was too coarse. Deletion must be bound to the exact (key, VersionId) the client recorded.

Weak Lifecycle Checks: Verifying S3 bucket lifecycle configurations was narrative ("check that it's set up") rather than an exact schema match, risking silent failures.

Fragile Placement Proofs: Relying on the absence of a temporary file to prove a rename(2) succeeded depended on implicit assumptions about who else might have deleted that file.

Ambiguous VM States: Handling EC2 stopped instances was treated as an exception text rather than a formalized anomaly class requiring operator intervention.

Decision
We will tighten orphan accounting to exactly three categories, formalize S3 lifecycle verification as a strict schema match, enforce cryptographic naming and locking for temp-file proofs, and isolate stopped instances into an anomaly management path.

1. Orphan Categories (Final)
All orphaned resources fall into exactly three categories. Anything else is handled by the cloud provider's lifecycle rules or is an anomaly.

orphan_vm_known: An InstanceId recorded in a non-terminal WAL. Actionable only if the instance is pending, running, or shutting-down.

orphan_s3_recorded_current_version: An exact (key, VersionId) tuple recorded in the WAL that still exists. Actionable only via explicit flags.

cloud_state_anomaly: Discrepancies between the WAL and cloud reality (e.g., stopped VMs, missing resources). Reported, never auto-acted on.

2. Strict Cleanup Semantics
aero cleanup <job_id> reads one WAL and acts only on what it recorded. It never scans or filters by tags.

S3 Deletion is default-off and hard-gated:

Requires aero cleanup <job_id> --delete-objects.

Requires the WAL to be in a terminal state (cannot delete objects for a potentially running job).

Calls DeleteObjectVersion using the exact recorded VersionId. Never key-only.

Requires interactive confirmation per tuple (or a strict script-acknowledgement flag).

Stopped VMs are anomalies: A stopped VM produces a vm_state_anomaly. It is never touched by normal cleanup and requires the break-glass flag --force-terminate-stopped.

3. Formal S3 Lifecycle Schema Verification
At every client startup, aero calls GetBucketLifecycleConfiguration and requires an exact schema match for expiration rules, including:

Expiration.Days (current versions)

NoncurrentVersionExpiration.NoncurrentDays (noncurrent versions)

Expiration.ExpiredObjectDeleteMarker (delete marker cleanup)

AbortIncompleteMultipartUpload

Mismatch results in a hard refusal to operate. We explicitly document that lifecycle rules are the eventual backstop, while aero cleanup is the deterministic path.

4. Deterministic Output Placement & Temp-File Proof
For the absence of a temp file to definitively prove a successful rename(2):

Exclusive Naming: Temp files use the format <destination_parent>/.aero-tmp-<job_id>-<sha256(abs_destination)>.zip.

No External Deletion: No client command (not even GC) may delete a temp file belonging to a WAL in PLACEMENT_PREPARED or PLACEMENT_COMMITTED.

Strict Lock Ordering: The job lock is acquired first, then the destination lock. Released in reverse. Both are held throughout temp file creation and rename.

Full Path Resolution: realpath is checked against the entire parent chain to refuse symlinks at any level. Missing parents are never auto-created.

Alternatives considered
Deleting the "current" S3 version by key (Rejected: Vulnerable to race conditions and deleting user overwrites. Must use exact VersionId).

Auto-terminating stopped instances (Rejected: A stopped instance violates the InstanceInitiatedShutdownBehavior=terminate contract. It is an anomaly requiring human review).

Allowing loose lifecycle configurations (Rejected: Leads to silent buildup of artifacts. Exact schema match forces correct bucket setup).

Auto-creating missing parent directories during placement (Rejected: Creating parents grants the tool ambient authority over the user's filesystem structure).

Why this choice won
No Collateral Damage: Strict VersionId binding ensures --delete-objects cannot destroy data the client didn't record.

Sound Recovery Proofs: Cryptographic naming and strict locking make temp-file absence an ironclad proof of placement success.

Fail-Fast Configuration: Exact-schema lifecycle checks prevent users from running jobs without a guaranteed storage backstop.

Anomaly Visibility: Forcing stopped instances into a break-glass path highlights infrastructure failures rather than masking them.

What is in scope for v1
Three strict orphan categories (orphan_vm_known, orphan_s3_recorded_current_version, cloud_state_anomaly).

aero cleanup <job_id> restricted to pending/running/shutting-down VMs.

Hard-gated --delete-objects requiring terminal WAL, interactive confirmation, and exact VersionId usage.

Break-glass flag --force-terminate-stopped for anomaly VMs.

Exact-schema GetBucketLifecycleConfiguration check at every invocation.

Cryptographic temp-file naming, strict lock ordering, and full-chain symlink rejection during output placement.

Startup warnings indicating the critical nature of the WAL directory (~/.aero/jobs).

What is out of scope for v1
Automatic handling of any cloud_state_anomaly.

Any S3 deletion that does not use an exact VersionId.

Auto-creation of destination parent directories.

Account-wide sweeps or tag-based garbage collection.

Key invariants
WAL is Absolute: Cleanup acts only on the exact InstanceId and VersionId recorded in the WAL.

Lifecycle is Backstop: The S3 bucket must have exact lifecycle rules configured; aero refuses to run otherwise.

Atomic Placement Proof: Temp-file absence equals rename success only because locks are held and names are cryptographically bound to the job and destination.

Risks
UX Friction: The exact-schema lifecycle check will frustrate users who want to configure their buckets loosely.

Cleanup Friction: Requiring per-tuple confirmation for --delete-objects makes scripted cleanup harder (requires a specific --yes-i-recorded-these override).

API Cost: Checking bucket lifecycle at every client invocation adds a small latency/cost penalty.

Blockers still to resolve
Flag Naming: Finalize the exact flag name for scripted --delete-objects acknowledgement (e.g., --yes-i-recorded-these).

Anomaly UX: Define the exact console output format for reporting a cloud_state_anomaly.

Lock Break Record: Define the exact WAL schema for a LOCKS_BROKEN record.

First-Run Warning: Draft the exact wording for the first-run warning regarding WAL criticality.

Consequences
Positive
Output placement recovery is mathematically sound.

S3 artifact deletion is perfectly safe and race-condition free.

Bucket configuration errors are caught immediately.

WAL loss is surfaced to the user, not buried in documentation.

Negative
High friction for bucket setup.

Additional API call overhead at startup.

What would invalidate this ADR later
The API cost/latency of checking the lifecycle configuration on every run becomes a noticeable bottleneck for users running many small jobs.

Users overwhelmingly reject the strict lifecycle schema, requiring a fallback to a more narrative verification mode.