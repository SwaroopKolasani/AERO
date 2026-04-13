Context
The previous workload-class revision narrowed the v1 scope but left four mechanisms named without being specified ("closed but undefined"): the user environment allowlist, resource_profile mappings, the network namespace construction, and the mount model. A closed mechanism with undefined contents is not effectively closed.

Furthermore, an adversarial review flagged that several spec fields (working_dir, stdio) were merely ceremonial—present but with only one permitted value—and should be cut rather than preserved for "forward compatibility." To maintain a defensible boundary, we must replace undefined mechanisms with concrete, narrow choices and shrink the specification by subtraction.

Decision
For v1, AERO will support a single workload class: Bounded File-to-File Batch, with a fully specified, concrete definition.

The work-spec is reduced to exactly eight mandatory fields: spec_version, mode (explicitly "exec" or "shell"), command, environment (must be explicitly empty), inputs, outputs, timeout_seconds, and resource_profile. Ceremonial fields are removed. The runner will mechanically enforce a strict execution environment: an isolated PID namespace, a strictly loopback network namespace, a fixed read-only root mount with a writable workspace, and exact byte-level stdio truncation.

Alternatives considered
Leaving mechanisms "closed but undefined" (rejected: leads to drift and implementation sprawl).

Preserving ceremonial fields for forward compatibility (rejected: false optionality creates dead code and confusion).

Allowing user-provided environment variables (rejected: introduces ambient state surface; deferred to v1.1).

Hiding shell execution inside a general command field (rejected: obscures reproducibility guarantees).

Exec-only mode with no shell support (rejected: too restrictive for v1, pushes users to write shell wrappers as inputs; adopted a named, disclosed "shell" mode instead).

Why this choice won
Concrete mechanisms prevent drift: Hardcoding the mount model and network namespace closes security gaps (e.g., cloud metadata service exfiltration) that firewalls cannot reliably catch.

Narrowing by subtraction: Removing working_dir and stdio simplifies the schema and implementation.

Honesty about reproducibility: Explicitly separating "exec" mode (reproducible) from "shell" mode (convenient but ambiguous) allows the product to set accurate expectations.

Eliminates ambient state: An empty environment allowlist and an immutable profile-to-tuple mapping remove hidden execution variables.

What is in scope for v1
Single Workload Class: Bounded File-to-File Batch (single process tree, non-interactive, bounded time/size, terminating).

Eight-Field Closed Work-Spec: spec_version, mode, command, environment, inputs, outputs, timeout_seconds, resource_profile.

Explicit Execution Modes: exec (direct execution, absolute path) and shell (runner invokes /bin/bash -c).

Strict Runner Environment: - Dedicated PID namespace (killed entirely when PID 1 exits).

Network namespace with exactly one interface (lo), no routes, no resolver.

Fixed mount layout (read-only root, /workspace tmpfs, minimal /dev).

Exact Stdio Semantics: Captured to separate, fixed 1 MiB ring buffers; exact byte-level head/tail truncation; raw byte delivery guaranteed on all terminal states.

Explicit Terminal Reasons: success, exit_nonzero, signal, timeout, harvest_failed, infrastructure_failure.

What is out of scope for v1
Stateful or incremental workloads relying on runtime network access (e.g., pip install, npm install).

User-supplied environment variables (the allowlist is frozen as empty {}).

Custom working directories (working_dir field removed; cwd is always /workspace).

Custom stdio routing (stdio field removed).

Any inbound or outbound network access beyond loopback.

Privileged workloads (no elevated Linux capabilities, no root uid, no_new_privs set).

Cacheability declarations, retries, priorities, or labels.

Key invariants
Empty Environment: The environment field must be passed as an empty map ({}). The command sees exactly the fixed system environment set by the runner.

Immutable Profiles: A resource_profile name resolves to an exact, immutable tuple (CPU architecture, vCPU count, memory, disk size, runner image digest) pinned per client version.

Closed Writes, Pinned Reads: The execution's write surface is strictly limited to /workspace. The read surface is the entirety of the pinned runner image.

Strict Harvest: Output harvesting refuses symlinks at every path level and rejects files with a link count > 1 to prevent hard-link exfiltration.

Risks
The workload class is extremely narrow, preventing users from running tasks that require dynamic environment variables or network downloads.

Providing "shell" mode weakens reproducibility, requiring clear documentation to prevent user confusion.

The runner image contract (toolchains, interpreters, exact bash version) becomes a heavy, load-bearing product artifact that must be managed and versioned carefully.

Blockers still to resolve
Exact v1 Numerical Ceilings: Settling on the exact maximums for timeout_seconds, input/output sizes, file counts, and stdio buffer limits.

Exact Resource Profile Set: Defining the two or three named profiles mapping to concrete cloud VM shapes.

Runner Image Contents: Determining the exact toolchains, interpreters, and libraries baked into the pinned image.

Input Content Transport: Defining the exact mechanism and reference format for staging input file content.

Consequences
Positive
Every mechanism is now concrete, leaving no room for implementation interpretation.

The empty user environment and profile-to-tuple mapping eliminate hidden state drift.

Network construction definitively closes cloud metadata-service security gaps.

Distinct terminal reasons make error handling deterministic for the caller.

The execution contract is clean enough to provide a stable target for v2's hashing/caching work.

Negative
The v1 feature set is highly restrictive; users must bake dependencies into the image or pass them entirely via file inputs.

The explicit shell-mode escape hatch requires user education on the trade-offs of convenience vs. reproducibility.

Significant upfront architecture is now required simply to build the runner image contract.

What would invalidate this ADR later
The restriction on user-supplied environment variables proves fatal to baseline usability, requiring early promotion of v1.1 features.

Maintaining the pinned runner image (to cover required "read surface" binaries) becomes too complex or bloats the image size beyond acceptable boot-latency limits.

The path-resolution and hard-link detection logic during harvest introduces unacceptable performance overhead for workloads with many outputs.