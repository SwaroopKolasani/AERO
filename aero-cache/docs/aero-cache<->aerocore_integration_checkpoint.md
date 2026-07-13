# AeroCache ↔ AeroCore Integration Checkpoint

## Status

AeroCache can optionally use AeroCore as a miss-placement service.

This integration is frozen as the first AeroCache ↔ AeroCore bridge. It is intentionally narrow: AeroCore is consulted only after AeroCache has determined that a request is cacheable and no verified cache entry exists.

## Runtime flags

```env
AEROCORE_ENABLED=1
AEROCORE_URL=http://localhost:8088
AEROCORE_TIMEOUT=2s
AERO_UPSTREAM_URL=http://localhost:11434



Request flow
client request
  ↓
AeroCache deterministic gate
  ↓
canonical key build
  ↓
L1/L2/L3 lookup
  ↓
verified hit?
  ├─ yes → serve from AeroCache, AeroCore is not called
  └─ no
      ↓
      coalesced miss leader
      ↓
      if AEROCORE_ENABLED=1:
          POST AeroCore /resolve
          use response.backend_url when decision is route or fail_open
      else:
          use AERO_UPSTREAM_URL
      ↓
      upstream response streams to client
      ↓
      deterministic response is written back to cache asynchronously