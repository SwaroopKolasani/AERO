# AeroRig M4 Status

AeroRig M4 is the first measurement harness for Project Aero.

It measures HTTP reachability, non-streaming OpenAI-compatible chat latency, streaming TTFT/duration, cache proof headers, answer stability, and benchmark deltas. It does not route requests, mutate cache state, register backends, or make placement decisions.

## Frozen scope

AeroRig M4 includes:

- HTTP probe
- HTTP summary
- non-stream chat probe
- non-stream chat summary
- chat proof check
- streaming chat probe
- streaming chat summary
- suite runner
- workload-to-suite compiler
- suite-to-matrix builder
- matrix comparison
- local cache-vs-direct benchmark script

## Non-goals

AeroRig M4 does not include:

- AeroEvidence graph persistence
- placement decisions
- backend registry mutation
- distributed load generation
- provider cost accounting
- Context-Anchor Routing
- Kubernetes/operator integration
- cloud benchmark study

## Schemas

AeroRig M4 emits these JSON schema versions:

- `aerorig.http_probe.v1`
- `aerorig.http_summary.v1`
- `aerorig.chat_probe.v1`
- `aerorig.chat_summary.v1`
- `aerorig.chat_proof.v1`
- `aerorig.chat_stream_probe.v1`
- `aerorig.chat_stream_summary.v1`
- `aerorig.suite_result.v1`
- `aerorig.matrix.v1`
- `aerorig.matrix_compare.v1`
- `aerorig.workload.v1`

## Acceptance

Core acceptance:

```bash
make fmt
make test
make build
make smoke
make summary
make build-suite