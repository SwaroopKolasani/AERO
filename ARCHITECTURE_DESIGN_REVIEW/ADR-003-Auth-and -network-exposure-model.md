ADR-003: Auth and Network Exposure Model for V1
Status
Proposed

Context
Project AERO combines two ambitions:

Ephemeral remote compute on user-owned cloud infrastructure

Reuse of prior computation through a content-addressed cache

The architecture is still being designed. The main risk is architectural sprawl: combining remote execution, durable state, correctness expectations, and cloud orchestration too early.

An initial design proposed a smart-client / stateless-runner / cache-as-storage boundary.
An adversarial review found that this boundary is defensible only if v1 is narrowed and several assumptions are made explicit. Previous revisions established the boundary (ADR-001) and the exact workload class (ADR-002).

This ADR addresses the authentication, network exposure, and data transfer model. Previous iterations mislabeled the transport mechanism as "mutual auth," left checksum protocols vague ("via metadata"), did not strictly define the archive creation contract, and treated the supervisor as "boring glue" rather than the privileged Trusted Computing Base (TCB) it is.

Decision
For v1, AERO will use a revised product boundary centered on:

Launch-authorized execution with bearer-token object transport. The user's cloud IAM principal is the sole job-creation authorization system.

An explicit, strictly defined Trusted Computing Base (TCB): the supervisor.

A concrete, single-part-PUT checksum protocol tied to S3 Object Version IDs.

A rigidly constrained ZIP archive contract for both extraction and creation.

A frozen user-data envelope with a strict size budget.

Alternatives considered
Runtime mutual authentication between client and runner (Rejected: Unnecessary complexity; IAM launch authority + presigned URLs are sufficient).

Multipart S3 uploads (Rejected: Introduces checksum ambiguity and complexity; v1 will use single-part PUT only, capping output at 5 GiB).

Allowing the supervisor general S3 credentials (Rejected: Violates least privilege; only time-bound presigned URLs are permitted).

Relying solely on security groups to block metadata service access (Rejected: Insufficient; requires explicit HttpEndpoint=disabled at launch and a loopback-only network namespace).

Relying on the bucket key alone for output verification (Rejected: Vulnerable to replay/overwrite attacks; output acceptance must be tied to a specific S3 Object VersionId).

Why this choice won
Explicit Security Posture: Naming the supervisor as the TCB forces any changes through a rigorous review and version-bump process.

Checksum Certainty: Pinning the exact S3 API calls (single-part PUT, x-amz-checksum-sha256) removes implementation ambiguity.

Replay Protection: Tying output acceptance to the S3 VersionId via a dedicated commit sidecar object prevents race conditions and overwrite attacks.

Stable Base for V2: Deterministic archive creation (lexicographic ordering, normalized mtimes/modes) provides a byte-stable format that V2's caching mechanisms can inherit cleanly.

Fails Closed on Expiry: Clamping URL TTL to the underlying STS credential lifetime prevents jobs from silently failing mid-run due to expired URLs.

What is in scope for v1
Supervisor as TCB: Explicitly named, documented, and governed by a TCB change process.

Exact Checksum Protocol: Client and Supervisor compute SHA-256 incrementally. Uploads use single-part PUT with x-amz-checksum-sha256.

Output Commit Sidecar: Supervisor writes a <job_id>.version object via a third presigned URL to signal the exact VersionId committed.

Restricted ZIP Subset: Stored/deflate only, no ZIP64, UTF-8 NFC names, regular files only, normalized modes (0644) and mtimes, atomic stage-and-rename extraction.

Frozen User-Data Envelope: 8 named fields, JSON-encoded, base64 wrapped. Hard cap at 14 KiB raw (2 KiB headroom). Overflow is rejected pre-launch.

STS TTL Awareness: URL TTL is clamped to min(nominal_TTL, credential_remaining_lifetime). Client refuses launch if the credential lifetime is shorter than timeout_seconds + margin.

Mandatory Bucket Settings: Versioning, default encryption, Block Public Access, and matching region are verified by the client before use.

Strict Network Exposure: Host namespace has outbound HTTPS only (inbound deny-all). Workload namespace is loopback only. IMDS is strictly disabled.

What is out of scope for v1
Any runtime authentication/handshake between client and runner.

Multipart S3 uploads.

ZIP64, encryption, or multi-disk archives.

Supervisor hot-patching or configuration files (only user-data is read).

Supervisor metric collection, retries (beyond HTTPS native), or diagnostic endpoints.

Any network access from the workload namespace beyond loopback.

Key invariants
IMDS Disabled: HttpEndpoint=disabled must be verified by the client on every RunInstances call.

Single-Part PUTs Only: Archives exceeding S3's 5 GiB limit fail validation before upload.

VersionId Tie-Breaker: The client accepts only the S3 VersionId explicitly reported in the commit sidecar. Later overwrites are ignored.

Closed User-Data: No fields beyond the defined 8 are permitted. Additions require an envelope_version bump and a new runner AMI digest.

TCB Discipline: Any change to supervisor behavior requires a new runner image digest and client version bump.

Risks
Solo-Founder Complexity: Implementing or safely vendoring the restricted ZIP parser/creator is a significant engineering cost.

STS Credential Refresh: Users with short-lived STS tokens may frequently hit the "refresh credentials" launch refusal if job timeouts are long.

Staging Bucket Obligations: Mandatory versioning increases S3 storage costs and requires users to configure lifecycle expiration rules.

Blockers still to resolve
Exact v1 Ceilings: Pick concrete numbers for per-entry size, total archive size, entry count, compression ratio cap, upload deadline margin, and lifecycle expiry.

ZIP Implementation: Decide whether to write a custom parser or vendor/wrap a minimal library.

TCB Change Process: Write down the short document defining how the supervisor is updated.

Bucket Setup Guide: Document the staging bucket prerequisites and the client verification steps.

Consequences
Positive
Unambiguous checksum protocol at the API-call level.

Archive creation is as tight as extraction, ensuring byte-stability for V2.

The supervisor is properly governed as the TCB.

Temporary-credential expiry fails safely at launch rather than mid-run.

Feature creep is mechanically prevented by freezing the 14 KiB user-data envelope.

Negative
The ZIP restricted subset adds real implementation overhead.

Bucket versioning adds storage cost and cleanup requirements.

Output archives are strictly capped at 5 GiB.

The third presigned URL (commit sidecar) slightly reduces user-data headroom.

What would invalidate this ADR later
The 5 GiB output limit proves too restrictive for early adopters before V2 multipart/streaming can be built.

The restricted ZIP parser becomes a recurring source of bugs or vulnerabilities, overwhelming the solo-founder maintenance budget.

STS credential limitations make the tool unusable in strict corporate environments with very short token lifetimes and long compute jobs.

Next linked design topics
ADR-004: Resource Profile Set and Numerical Ceilings

ADR-005: Input Content Transport Protocol (resolved by the Checksum & ZIP sections herein, but may need distinct implementation spec)

ADR-006: Latency Gate and Measurement Plan (carried over from ADR-001)