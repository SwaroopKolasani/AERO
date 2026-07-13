# AeroCore M3 Status

AeroCore M3 is the local control-plane and placement service for Project Aero.

It is intentionally not yet a Kubernetes operator. M3 freezes the local placement/control-plane surface that AeroCache can call after a cache miss.

## Current status

AeroCore M3 is complete enough for optional AeroCache integration.

Acceptance commands:

```bash
make fmt
make test
make contract
make smoke
make build