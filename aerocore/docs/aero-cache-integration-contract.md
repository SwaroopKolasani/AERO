# AeroCache ↔ AeroCore Integration Contract

This document freezes the integration boundary before AeroCache is modified.

## Boundary

AeroCache owns:

- OpenAI-compatible ingress
- deterministic gate
- canonical cache key construction
- L1/L2/L3 lookup
- store-and-verify
- singleflight miss coalescing
- streaming capture/replay
- cache proof headers

AeroCore owns:

- backend registry
- backend health/lifecycle
- placement decision
- fail-open target selection
- placement metrics
- placement trace continuity

## Request flow

1. Client calls AeroCache.
2. AeroCache checks deterministic gate.
3. AeroCache checks exact cache.
4. On verified hit, AeroCache serves the cached response.
5. On miss, AeroCache calls AeroCore `/resolve`.
6. AeroCore returns one of:
   - `route`
   - `fail_open`
   - `reject`
7. AeroCache forwards the original upstream request to `backend_url` for `route` and `fail_open`.
8. AeroCache does not forward on `reject`.
9. AeroCache preserves the original `X-Aero-Request-Id`.

## HTTP contract

AeroCache calls:

```http
POST /resolve
Content-Type: application/json
X-Aero-Request-Id: <request_id>

Request body:

{
  "request_id": "req_123",
  "model": "llama3.2:3b",
  "deadline_ms": 2000,
  "estimated_input_tokens": 512,
  "estimated_output_tokens": 128,
  "stream": true,
  "tier": "A"
}
Route response
{
  "request_id": "req_123",
  "decision": "route",
  "backend_id": "mac-m2-ollama",
  "backend_url": "http://mac.local:11434",
  "rung": "fleet",
  "reason": "cheapest_healthy_capable_backend",
  "fail_open": false
}

AeroCache action:

Forward original OpenAI-compatible request to backend_url.
Fail-open response
{
  "request_id": "req_123",
  "decision": "fail_open",
  "backend_id": "default-upstream",
  "backend_url": "http://localhost:11434",
  "rung": "upstream",
  "reason": "no_healthy_capable_backend",
  "fail_open": true
}

AeroCache action:

Forward original OpenAI-compatible request to backend_url.
Reject response
{
  "request_id": "req_123",
  "decision": "reject",
  "reason": "tier_b_not_supported_in_m3",
  "fail_open": false
}

AeroCache action:

Do not forward. Treat as placement rejection and use AeroCache's existing bypass/fail-open behavior.