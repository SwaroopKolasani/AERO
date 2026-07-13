# AeroRig

AeroRig is Project Aero's measurement harness.

M4 starts with deterministic, reproducible HTTP probes. Later checkpoints add inference probes, TTFT, tokens/sec, backend/model matrices, workload replay, and Study-1 cache-vs-prefix benchmark data.

## Scope

In:
- HTTP reachability probes
- latency samples
- JSONL result output
- local smoke tests
- future inference timing

Out:
- routing decisions
- cache mutation
- AeroCore registry mutation
- benchmark claims without raw results

## Commands

```bash
make fmt
make test
make build
make smoke