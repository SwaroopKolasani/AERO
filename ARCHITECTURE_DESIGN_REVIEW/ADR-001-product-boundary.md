Context
Project AERO's long-term ambition combines multi-cloud ephemeral compute bursting with content-addressed memoization. A first round of skeptical review forced the v1 scope down to remote execution only, deferring the entire memoization layer.

A second round of adversarial review showed that even the narrowed v1 concentrated significant hidden complexity in the client and the client↔runner bootstrap path. Several items previously labeled "design during build" were actually gating risks that had to be resolved before commitment. It also showed that an implicit "runner accepts a request" model created more protocol surface than v1 needed, and that maintaining a residual local-only cache was a costly distraction.

The architecture is still being designed. The main risk is architectural sprawl: combining remote execution, durable state, correctness expectations, and cloud orchestration too early.

Decision
For v1, AERO will use a revised product boundary centered on:

a client-side orchestrator

an ephemeral stateless runner

a narrowly scoped durable state layer

a deliberately constrained first workload class

Specifically, we are adopting a strict Payload-on-Boot model with no cache:

AERO Client (Control Plane): Embedded in callers (CLI only). It owns everything except the execution itself. It has an explicit on-disk state machine, deterministic recovery, and a strict compatibility policy. It mechanically enforces the workload class and constructs a closed work-spec.

AERO Runner (Stateless, Single-Use): A payload-on-boot ephemeral VM. It is launched by the client with its work attached at provisioning time. It boots from a pinned image, executes immediately, returns outputs through one tightly scoped return path, and self-terminates. It has no listener, accepts no requests, and has no general API endpoint.

No Cache of Any Kind: The optional local-only cache is entirely cut from v1 to eliminate lingering complexity.

Alternatives considered
Unified runner

Hosted control plane

Fully composable Unix-style separate tools

Remote execution without team-shared cache

Remote execution plus very narrow cache support (Local-only cache)

Request-Response Runner (Boot -> Listen -> Accept Request)

Providing a Library API alongside the CLI

Why this choice won
Keeps durable infrastructure minimal: The runner has essentially no protocol surface and nothing worth stealing.

Preserves survivability for a solo founder: The v1 surface is small enough for one person to build, operate, and defend.

Avoids turning the project into a hosted control plane too early: The client's true nature as an embedded control plane is acknowledged, gating the highest-risk parts (auth, recovery, orphans).

Keeps the architecture narrow enough to test core value before expansion: Tests the most important business risk (latency) before sunk cost accumulates.

Eliminates state complexities: There is no shared state, hence no cross-machine correctness contract to get wrong. The payload-on-boot runner trades flexibility for extreme structural simplification.

What is in scope for v1
A versioned CLI client for one host OS family.

A pinned runner image and a launch path for one cloud provider.

A payload-on-boot runner model with no listener.

A concrete return-path mechanism with scoped, time-limited credentials.

A closed work-spec format with a version field.

A manifest schema that mechanically accepts only the v1 workload class and rejects everything else.

The v1 workload class: A single batch command, bounded declared inputs/outputs, single process, no interactive I/O, no inbound network exposure, no daemons, bounded wall-clock timeout, and bounded resource footprint.

An explicit client state machine with deterministic recovery from every defined crash point.

A first-class orphan-VM listing and cleanup command, scoped to the client's identity.

A client compatibility policy covering protocols, state records, and refuse-to-operate conditions.

Defined output placement semantics (overwrite, permissions, path-traversal protection).

A latency measurement plan executed on representative workloads as a go/no-go gate before further build.

What is out of scope for v1
The optional local-only cache (removed entirely).

Any library surface or API (CLI only).

Any "runner accepts a request" or listener model.

Shared remote cache, content-addressed memoization, and action hashing.

Cacheability declarations, bucket schema, publish protocol.

Multi-cloud, multi-tenant, hosted control planes, or web UIs.

Lookup-before-provisioning and runner-side object-store access.

Long-running, interactive, networked, or daemon workloads.

Key invariants
1:1 Mapping: One client invocation maps to one runner VM, used exactly once. No reuse, no pooling, no reconnect.

No Listener: Trust is established at launch time. The runner exposes no general request endpoint.

Guaranteed Destruction: The runner is destroyed on every terminal state (success, failure, timeout, or client-side recovery).

Strict Versioning: The client is the only versioned component crossing trust boundaries, governed by an explicit compatibility policy. Old clients cannot launch runners they cannot fully communicate with.

Closed Work Spec: The runner observes only what the spec declares. Ambient state dependencies are mechanically forbidden.

Strict Enforcement: The v1 workload class is enforced mechanically at the client. No "best effort" or soft acceptance.

Durable Client State: Client state is on-disk, written before the corresponding side effect (e.g., cloud API calls), ensuring deterministic recovery.

Pinned Image: The runner image is pinned by digest; the client refuses any other image.

Accountable Orchestration: Orphan accounting is a first-class user-facing capability.

No Shared State: Zero shared state across invocations or machines.

Risks
Client becoming an unbounded control plane or overly complex state machine.

End-to-end miss-path latency rendering the tool slower than manual execution.

Hidden complexity in mutual auth, network exposure, and the payload-on-boot credential handoff.

Performance ceilings from bulk input/output movement through a single transport path.

The payload-on-boot model being too inflexible for early adopters.

Blockers still to resolve
(Note: These are hard gates. Build does not begin until resolved.)

End-to-end miss-path latency: Must be measured on representative workloads first as a go/no-go gate.

Mutual auth & trust establishment: Concrete sequence for the payload-on-boot model (replay prevention, crash-after-provisioning).

Network exposure model: What interfaces are reachable, by whom, and whether the return path requires public internet access.

Exact v1 workload class rules: Defined as enforceable validation logic, not just prose.

Closed work-spec format: Enumerating every observable field.

Single transport mechanism: One path for inputs (launch) and outputs (return).

Client state machine: Every state, transition, crash point, and re-entry behavior written down.

Orphan accounting model: Detection, listing, and cleanup scoped to client identity.

Client compatibility policy: Rules for state-record versioning and protocol skew.

Output placement semantics: Overwrite, symlinks, traversal protection, and cleanup.

Runner image contract: Pinning, building, and client-side validation.

Consequences
Positive
A radically narrower, highly defensible initial product.

Cleaner reasoning about persistent vs ephemeral components.

Lower coordination overhead and no distributed state correctness bugs.

High-risk business constraints (latency, orphan leakage) are confronted immediately.

Negative
Less ambitious v1; the headline memoization/caching narrative is entirely deferred.

The client carries substantial complexity as an embedded control plane.

The rigid payload-on-boot model trades away flexibility that may require revisiting later.

What would invalidate this ADR later
End-to-end miss-path latency on representative workloads proves too slow or expensive, failing the initial go/no-go gate.

Client-side orchestration becomes operationally dominant, overwhelmingly complex, or too fragile for a solo founder to maintain.

Auth/bootstrap complexity in the payload-on-boot model cannot be made secure without introducing a listener.

Data transfer through the single boundary transport path fails to scale.

Next linked design topics
ADR-002: End-to-End Latency Measurement Plan (Go/No-Go Gate)

ADR-003: Mutual Auth and Network Exposure Model

ADR-004: Workload-Class Validation Rules & Closed Work-Spec

ADR-005: Client State Machine and Deterministic Recovery

ADR-006: Orphan Accounting and Output Placement Semantics