# AeroCache Correctness Contract

Status: M0–M2 Tier-A correctness contract
Scope: `aerocache/` only
Applies to: exact cache hits served by AeroCache
Does not apply to: Tier-B semantic matching, future placement decisions, future BYOC multi-tenancy

---

## 1. Purpose

AeroCache is allowed to save GPU work only when it can prove that a cached response belongs to the exact request being served.

This document defines the Tier-A correctness contract:

> A Tier-A cache hit may be served only if the live request material and stored request material are byte-equal, and the decompressed stored response hashes to the stored response hash.

AeroCache is an optimization layer. It must never become a source of wrong answers or outages.

---

## 2. Invariants

All implementation choices must preserve these invariants.

### 2.1 Never wrong

A served Tier-A cache hit must be byte-identical to the response previously produced for the same effective request under the same serving fingerprint.

“Same effective request” means:

* same rendered prompt token IDs
* same canonical sampling parameters
* same serving fingerprint
* same invalidation epoch

A hash lookup alone is not proof. The hash only finds a candidate. Store-and-verify decides whether that candidate may be trusted.

### 2.2 Never an outage

Cache-side failure must not convert a servable request into a failed request.

The following must degrade to bypass or miss:

* tokenizer unavailable
* tokenizer error
* canonicalization error
* key-build error
* L1/L2/L3 timeout
* L1/L2/L3 store error
* decompression error
* response hash mismatch
* write-back error
* write-back queue full

Only upstream/model errors may propagate as request failures.

### 2.3 Never silently semantic

Tier A and Tier B must remain separate.

Tier A:

* exact only
* content-addressed
* store-and-verify required
* metrics under `aero_cache_*`
* response tier `cache-l1`, `cache-l2`, or `cache-l3`

Tier B:

* semantic or approximate
* disabled by default
* separate code path
* separate key space
* separate metrics under `aero_tierb_*`
* never counted as exact savings

No Tier-B result may be reported as a Tier-A hit.

---

## 3. Exact-hit theorem

### 3.1 Statement

For a deterministic request `R`, AeroCache may serve cached response bytes `B` as a Tier-A hit only if all of the following hold:

1. `R` passes the determinism gate.
2. `R` is transformed into live key material `M_live`.
3. A cache tier returns candidate entry `E`.
4. `E` passes request-side verification against `M_live`.
5. `E` passes response-side verification.
6. The response bytes served to the client are the decompressed bytes covered by `E.RespHash`.

If these conditions hold, AeroCache serves a verified exact hit.

If any condition fails, AeroCache must not serve the cached response. It must treat the candidate as a miss or bypass and proceed to upstream.

### 3.2 Proof obligations

The theorem depends on two proof obligations.

#### Obligation A: Request-side equality

The stored entry must match the live request material exactly:

```text
E.TokenIDs     == M_live.TokenIDs
E.Params       == M_live.CanonicalParams
E.Fingerprint  == M_live.Fingerprint
E.Epoch        == M_live.Epoch
```

This protects against:

* BLAKE3 collision
* key-space poisoning
* wrong entry under correct key
* stale model/template namespace
* stale epoch
* params mismatch
* tokenizer/template mismatch

#### Obligation B: Response-side equality

The stored response must match its stored hash:

```text
BLAKE3(decompressed(E.Response)) == E.RespHash
```

This protects against:

* L2/L3 bit rot
* corrupted compressed payload
* partial write
* storage-layer corruption
* accidental overwrite

Both obligations are required. Request-side equality alone is insufficient. Response-side equality alone is insufficient.

---

## 4. Cacheable predicate

A request is cacheable in strict mode only if all conditions below hold.

```text
temperature == 0
n unset or n == 1
best_of unset or best_of == 0 or best_of == 1
tokenizer available for the active fingerprint
renderer available for the active chat template
no unsupported tools/tool_calls path
no known engine-random field present
```

If any condition fails:

```text
X-Aero-Cache: bypass
```

The request must go upstream and must not be written back to Tier A.

### 4.1 Temperature

`temperature` must be explicitly zero for strict mode.

Missing temperature is not assumed deterministic unless a future compatibility mode explicitly normalizes provider defaults and tests them.

### 4.2 Seeded generation

Seeded deterministic generation is not Tier-A cacheable by default.

A future lenient mode may allow seeded generation only for a fingerprint whose backend has been attested deterministic for that configuration.

Lenient mode may relax only the gate. It must never relax store-and-verify.

### 4.3 Tools and tool calls

Tools and tool calls must bypass unless the exact model template branch has parity tests against the reference tokenizer/template.

Unsupported tools behavior:

```text
tools present      -> key build fails or gate bypasses
tool_calls present -> key build fails or gate bypasses
```

This is correct fail-open behavior.

---

## 5. Key material

The cache key is derived from:

```text
BLAKE3(
  domain_separator
  fingerprint_bytes
  epoch
  token_ids
  canonical_params
)
```

The resulting key is not trusted by itself. It is an index into candidate storage.

### 5.1 Domain separator

The domain separator identifies the key scheme version.

Example:

```text
aerocache-key-v1
```

Changing the key scheme requires changing the domain separator.

### 5.2 Token IDs

AeroCache keys on token IDs after chat-template rendering.

It does not key on raw prompt text or raw JSON messages.

For chat requests:

```text
messages -> chat template renderer -> rendered prompt -> tokenizer -> token IDs
```

For completion requests:

```text
prompt -> tokenizer -> token IDs
```

For embedding requests:

```text
input -> tokenizer or canonical embedding input representation
```

A tokenizer or renderer failure must bypass cache.

### 5.3 Canonical params

Canonical params include all request fields that may affect output shape or response bytes.

Minimum set:

```text
model
temperature
top_p
top_k
min_p
seed
max_tokens
stop
presence_penalty
frequency_penalty
repetition_penalty
logit_bias
response_format
logprobs
top_logprobs
n
best_of
tools
tool_choice
stream
user
encoding_format
dimensions
```

If a new output-affecting parameter is added, it must be added to canonical params before Tier-A caching may use it.

### 5.4 Fingerprint

The serving fingerprint identifies the environment that produced the response.

Minimum fingerprint inputs:

```text
model identity
model commit or revision when available
engine name
engine version
engine config affecting numerics
dtype
quantization
tensor parallel size
KV-cache dtype
tokenizer identity
tokenizer hash
tokenizer_config hash
chat_template hash
chat_template kind
pinned chat-template date if used
renderer implementation/version
```

Any change that can change output bytes must change the fingerprint or epoch.

### 5.5 Epoch

Epoch is a logical invalidation counter.

A changed epoch makes old keys unreachable without scanning or deleting storage.

Use epoch for:

* manual flush
* template-level invalidation
* renderer bug fix
* tokenizer parity correction
* emergency invalidation

---

## 6. Canonicalization rules

Canonicalization removes serialization noise without changing model semantics.

Rules:

```text
parse JSON with number preservation
sort object keys lexicographically
preserve array order
preserve message order
normalize number formats
normalize explicit integer-like floats to integers
normalize configured defaults consistently
do not trim tokenizer-sensitive text globally
do not reorder messages
do not reorder arrays
do not drop unknown fields unless they are proven irrelevant
```

Examples that must produce the same canonical params:

```json
{"temperature":0,"max_tokens":16}
```

```json
{"max_tokens":1.6e1,"temperature":0.0}
```

Examples that must not be normalized together:

```json
{"messages":[{"role":"user","content":"hello"}]}
```

```json
{"messages":[{"role":"user","content":"hello "}]}
```

Whitespace inside prompt content is tokenizer-sensitive.

---

## 7. Store-and-verify procedure

For every candidate entry from L1, L2, or L3:

1. Decompress response if needed.
2. Compare stored request material with live material.
3. Hash decompressed response bytes.
4. Compare hash with `RespHash`.
5. Serve only if both checks pass.

Pseudocode:

```text
verify(M_live, E):
  if E.Epoch != M_live.Epoch:
      return fail("epoch_mismatch")

  if E.Fingerprint != M_live.Fingerprint:
      return fail("fingerprint_mismatch")

  if E.TokenIDs != M_live.TokenIDs:
      return fail("token_ids_mismatch")

  if E.Params != M_live.CanonicalParams:
      return fail("params_mismatch")

  if BLAKE3(decompressed(E.Response)) != E.RespHash:
      return fail("response_hash_mismatch")

  return pass
```

On failure:

```text
increment aero_cache_verify_mismatch_total
delete bad entry from that tier if possible
continue lookup or proceed to miss
```

A verify failure is never served.

---

## 8. Tier lookup correctness

Lookup order:

```text
L1 -> L2 -> L3 -> miss
```

### 8.1 L1

L1 is in-process and per-replica.

A verified L1 hit may be served immediately.

A failed L1 lookup becomes a miss to L2.

### 8.2 L2

L2 is shared Valkey storage.

A verified L2 hit is promoted to L1.

A slow or failed L2 lookup becomes a miss to L3 or upstream.

### 8.3 L3

L3 is future Cloudflare R2-backed object storage.

A verified L3 hit is promoted to L2 and L1.

A slow or failed L3 lookup becomes an upstream miss.

### 8.4 Promotion

Promotion is an optimization, not part of correctness.

If promotion fails:

```text
serve verified hit anyway
record metric if applicable
do not fail request
```

---

## 9. Miss handling correctness

On miss:

```text
singleflight -> upstream -> stream/capture -> enqueue write-back
```

The leader calls upstream once per replica-local flight.

Followers wait and replay the completed buffer.

The singleflight entry must remain active until the leader has:

```text
completed upstream response capture
computed enough metadata for write-back
enqueued write-back or dropped it due to queue pressure
```

Default behavior:

```text
hold until enqueued
```

Optional future behavior:

```text
hold_until_commit=true
```

### 9.1 Upstream errors

Upstream errors are not cache errors.

If upstream fails before response headers are written, AeroCache may return a gateway-style error.

If upstream fails after streaming begins, AeroCache cannot rewrite the response. It must not write partial bytes to cache.

### 9.2 Write-back eligibility

Write-back is allowed only if:

```text
request passed determinism gate
key material was built successfully
upstream response completed successfully
response status is cacheable
response bytes were fully captured
```

Non-2xx responses are not written to Tier A unless explicitly allowed by future policy.

### 9.3 Write-back idempotence

Writes are content-addressed.

If two workers write the same key with the same material and response, last-write-wins is safe.

If a bad write occurs, store-and-verify must reject it later.

---

## 10. Fail-open table

| Failure                   | Required behavior                    | Correctness effect            |
| ------------------------- | ------------------------------------ | ----------------------------- |
| Invalid JSON              | Return request error                 | Not cache-related             |
| Non-deterministic request | Bypass cache                         | No Tier-A entry created       |
| Tokenizer unavailable     | Bypass cache                         | Avoids guessed keys           |
| Renderer unavailable      | Bypass cache                         | Avoids guessed templates      |
| Tokenizer error           | Bypass cache                         | Avoids guessed token IDs      |
| Key-build error           | Bypass cache                         | Avoids wrong key              |
| L1 get error              | Treat as miss                        | May cost upstream work        |
| L2 get error              | Treat as miss                        | May cost upstream work        |
| L3 get error              | Treat as miss                        | May cost upstream work        |
| Lookup timeout            | Treat as miss                        | Preserves latency/SLO         |
| Decompression error       | Treat as miss and delete if possible | Avoids corrupted response     |
| Request verify mismatch   | Treat as miss and delete if possible | Avoids wrong answer           |
| Response hash mismatch    | Treat as miss and delete if possible | Avoids corrupted answer       |
| Promotion error           | Serve verified hit                   | Promotion is optional         |
| Write-back error          | Drop write                           | Future miss only              |
| Write-back queue full     | Drop write and increment metric      | Future miss only              |
| Upstream error            | Propagate upstream failure           | Not caused by cache           |
| Metrics error             | Ignore                               | Metrics cannot affect serving |

---

## 11. Tier A and Tier B separation

Tier A exact caching is the only correctness-guaranteed path.

Tier B semantic caching may exist later, but it must satisfy all separation rules:

```text
different package/module
different metrics prefix
different response label
different key space
different stats bucket
different benchmark accounting
off by default
never counted as exact savings
```

Tier B may never return:

```text
X-Aero-Cache: hit
X-Aero-Tier: cache-l1/cache-l2/cache-l3
```

unless it has also passed Tier-A store-and-verify, in which case it is no longer a Tier-B result.

---

## 12. Observability requirements

The following metrics must exist for correctness visibility:

```text
aero_cache_requests_total{result,tier}
aero_cache_bypass_total{reason}
aero_cache_verify_mismatch_total
aero_cache_writeback_dropped_total
aero_cache_writeback_queue_depth
aero_upstream_calls_total
```

`verify_mismatch_total` should be approximately zero under normal operation.

Any nonzero value may indicate:

* collision
* corruption
* poisoning attempt
* code bug
* stale fingerprint
* tokenizer/template mismatch
* compression/decompression bug

A verified hit should expose:

```text
X-Aero-Cache: hit
X-Aero-Tier: cache-l1 | cache-l2 | cache-l3
```

A bypass should expose:

```text
X-Aero-Cache: bypass
X-Aero-Bypass-Reason: <reason>
```

A miss should expose:

```text
X-Aero-Cache: miss
X-Aero-Tier: dev | fleet | owned | burst
```

A coalesced follower should expose:

```text
X-Aero-Cache: coalesced
```

---

## 13. Security boundary

M0–M2 assumes trusted-network-only deployment.

This document does not claim:

* tenant isolation
* authorization
* authenticated cache access
* cache privacy across tenants
* BYOC-grade auditability
* public shared-cache safety

Store-and-verify protects correctness against poisoning, but it does not provide full multi-tenant security.

Full security belongs to the BYOC/productization phase.

---

## 14. Current implementation status

Implemented:

```text
determinism gate
canonical key builder
BLAKE3 keying
fingerprint and epoch binding
L1 lookup
L2 lookup
L3 disabled placeholder
store-and-verify
per-replica singleflight
direct upstream capture
async write-back
verified second-hit serving
Llama 3.2 basic tokenizer/template parity fixture
```

Dev-only:

```text
ByteTokenizer
LegacyRenderer
```

Deferred:

```text
generic Jinja execution
tool/tool_call parity fixtures
Cloudflare R2 L3
vLLM parity
distributed coalescing
hold_until_commit
BYOC security
```

---

## 15. Required tests

### 15.1 Key tests

Must prove:

```text
JSON field reorder -> same key
number representation change -> same key
prompt character change -> different key
max_tokens change -> different key
fingerprint change -> different key
epoch change -> different key
```

### 15.2 Tokenizer parity tests

Must prove for each supported model/template:

```text
Go renderer + Go tokenizer token IDs
==
Hugging Face apply_chat_template(..., tokenize=true, add_generation_prompt=true)
```

A model/template is not Tier-A eligible until parity passes.

### 15.3 Verify tests

Must prove:

```text
matching material + matching response hash -> pass
token_ids mismatch -> fail
params mismatch -> fail
fingerprint mismatch -> fail
epoch mismatch -> fail
response hash mismatch -> fail
```

### 15.4 Lookup tests

Must prove:

```text
L1 verified hit serves
L2 verified hit promotes to L1
L3 verified hit promotes to L2 and L1 when enabled
verify mismatch deletes bad entry
tier error degrades to miss
tier timeout degrades to miss
```

### 15.5 Write-back tests

Must prove:

```text
completed upstream response writes entry
stored entry has matching RespHash
queue full drops write without failing request
write error does not fail request
non-deterministic request is not written
```

### 15.6 End-to-end tests

Must prove:

```text
first deterministic request -> miss -> upstream
second identical deterministic request -> verified hit
same request after process restart with L2 -> cache-l2 hit
non-deterministic request -> bypass
tokenizer unavailable -> bypass
corrupted stored response -> miss, not hit
```

---

## 16. Non-goals

This document does not prove that:

* the model itself is deterministic under all batching conditions
* temp-0 is bit-exact across every backend
* vLLM and Ollama produce identical bytes
* semantic cache hits are correct
* untrusted public cache access is safe
* cross-replica coalescing is globally optimal

AeroCache proves only this:

> If a Tier-A cached response is served, it passed request-side equality and response-side hash verification under the active serving fingerprint and epoch.

---

## 17. Acceptance criteria

AeroCache satisfies this correctness contract when:

```text
go test ./...
passes

tokenizer parity passes for the active model/template

twice-fire demo shows:
  first request: miss/dev
  second request: hit/cache-l1 or hit/cache-l2

manual corruption test shows:
  corrupted entry is rejected
  verify_mismatch_total increments
  request falls through to upstream

tokenizer unavailable test shows:
  X-Aero-Cache: bypass
  X-Aero-Bypass-Reason: tokenizer_unavailable
```

No benchmark or savings claim may be published as Tier-A exact unless these acceptance criteria pass.
