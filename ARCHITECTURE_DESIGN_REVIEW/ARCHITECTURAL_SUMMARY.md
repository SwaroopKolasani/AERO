ARCHITECTURE SUMMARY
Purpose
This document freezes the v1 architecture baseline for Project AERO. It provides a stable, closed target for the latency measurement rig, ensuring validation is performed against concrete, committed design choices rather than a moving target or speculative roadmap.

V1 Product Boundary
AERO utilizes a strict Payload-on-Boot model with no cache of any kind.

The AERO Client acts as an embedded control plane for callers via CLI only, owning everything except the execution itself.

The AERO Runner is an ephemeral, stateless, single-use VM launched by the client with its work attached at provisioning time.

The runner boots from a pinned image, executes immediately, returns outputs through one tightly scoped path, and self-terminates.

The architecture mandates a strict 1:1 mapping: one client invocation maps to exactly one runner VM.

The runner exposes no general request endpoint and has no listener.

There is no shared state across invocations or machines.

V1 Supported Workload Class
V1 supports a single workload class: Bounded File-to-File Batch (single process tree, non-interactive, bounded time/size, terminating).

The work-spec is a closed eight-field schema: spec_version, mode, command, environment, inputs, outputs, timeout_seconds, and resource_profile.

Execution modes are explicitly restricted to exec (direct execution, absolute path) and shell (runner invokes /bin/bash -c).

The environment field must be passed as an explicitly empty map ({}).

The execution environment mechanically enforces a dedicated PID namespace, a strictly loopback network namespace, and a fixed mount layout (read-only root, writable /workspace tmpfs, minimal /dev).

Stdio is captured to separate, fixed 1 MiB ring buffers with exact byte-level head/tail truncation.

Unresolved Blockers: The exact numerical ceilings (time/size bounds), exact resource profile sets mapping to cloud VM shapes, exact runner image contents, and input content transport mechanism are gated before build.

Auth and Network Model
The system uses launch-authorized execution with bearer-token object transport.

The user's cloud IAM principal is the sole job-creation authorization system.

The supervisor is explicitly named and governed as the Trusted Computing Base (TCB).

Checksums use a concrete, single-part-PUT protocol tied to S3 Object VersionIds.

The supervisor writes a <job_id>.version object via a third presigned URL to signal the exact VersionId committed.

Host namespace has outbound HTTPS only (inbound deny-all); workload namespace is loopback only.

IMDS is strictly disabled (HttpEndpoint=disabled) and verified at launch.

User-data is passed in a frozen envelope with a hard cap of 14 KiB raw.

Unresolved Blockers: Final decision on custom ZIP parser vs. minimal vendored library, written TCB change process, and documented bucket setup guide.

Client State Machine and Recovery Model
The client state machine is explicitly on-disk, using an append-only Write-Ahead Log (WAL), expanded to 14 states.

Explicit Sidecar Acceptance (SIDECAR_ACCEPTED) requires the client to persist the sidecar's bytes/digest and output VersionId to the WAL; all subsequent recovery ignores S3 keys and reads only by this versioned identity.

Strict File Placement Fencing (PLACEMENT_PREPARED) requires destination parent validation, same-filesystem checks, and persisting a complete rename plan before any user paths are touched.

Each rename(2) operation is individually fenced with RENAME_INTENT and RENAME_DONE WAL records.

Durability relies exclusively on append-only WAL writes followed by fsync(2) on files and parent directories.

The RUNNER_FENCED state strictly waits for the EC2 instance to reach terminated.

Unresolved Blockers: Pre-build decision on whether to narrow v1 to single-output placement (which would eliminate states 11/12), exact timeout/TTL values, UX definition for partial failures, and frozen WAL schema.

Orphan Accounting and Output Placement Model
Orphaned resources fall into exactly three categories: orphan_vm_known, orphan_s3_recorded_current_version, and cloud_state_anomaly.

Cleanup acts only on the exact InstanceId and VersionId recorded in a specific job's WAL.

S3 deletion is hard-gated: requires --delete-objects, a terminal WAL state, interactive confirmation per tuple, and the exact VersionId.

Discrepancies like stopped VMs are reported as anomalies, never auto-acted on, requiring break-glass flags to resolve.

S3 lifecycle configurations are verified against an exact schema match at every client startup; mismatch causes a hard refusal to operate.

Temporary file proofs for placement use exclusive cryptographic naming, full-chain realpath symlink rejection, and strict lock ordering (job lock then destination lock).

Unresolved Blockers: Exact script-acknowledgement flag name, console UX for anomaly reporting, WAL schema for LOCKS_BROKEN, and first-run warning wording.

Latency Gate Rules
Total wall-clock time is a first-class primary gate alongside Overhead Tax.

B3a (cold remote orchestration) is a hard sub-gate: failure (< 25s p90) resulting from T_boot_launch (EC2) is an immediate STOP.

Deliberate failure workload must hit a total wall-clock of < 40s p90.

Measurement sessions enforce strict invalidation rules based on throughput variance, absolute throughput floors, laptop thermal events, and EC2 regional degradation.

Completion rate requires exactly 50/50 for a clean pass; 48/50 or 49/50 triggers an INCONCLUSIVE result and re-test.

Zero tolerance is enforced for FAILED_ORPHAN_SUSPECTED.

The "Realistic Minimal" fixture is cryptographically frozen via committed SHAs; any drift constitutes test failure.

Unresolved Blockers: Automation of rig checks, committing the final Realistic Minimal manifest, and publishing the final decision matrix.

Explicitly Deferred from V1
Any form of caching (local or shared remote), content-addressed memoization, and action hashing.

Library APIs, web UIs, and hosted control planes (v1 is CLI only).

Request-response listener models for the runner.

User-supplied environment variables and custom working directories.

Stateful, incremental, networked, interactive, daemon, or privileged workloads.

Runtime mutual authentication handshakes between client and runner.

Multipart S3 uploads, multi-disk archives, ZIP64, and encryption.

Automatically creating missing parent directories during placement.

Account-wide sweeps, tag-based garbage collection, or automatic anomaly handling.

Validation Principle
The latency rig must test this architecture exactly. Implementation does not expand beyond this baseline unless the latency gate passes and the architecture is intentionally revised. No speculative features, roadmap items, or assumptions may be introduced into the test rig.