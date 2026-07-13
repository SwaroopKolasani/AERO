#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${AEROCORE_SMOKE_PORT:-$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)}"
ADDR=":${PORT}"
BASE_URL="http://localhost:${PORT}"
LOG_FILE="${TMPDIR:-/tmp}/aerocore-smoke-${PORT}.log"
SMOKE_BACKENDS_FILE="${TMPDIR:-/tmp}/aerocore-smoke-backends-${PORT}.json"

cd "${ROOT_DIR}"

rm -f "${LOG_FILE}" "${SMOKE_BACKENDS_FILE}"

cat > "${SMOKE_BACKENDS_FILE}" <<'JSON'
{
  "backends": [
    {
      "id": "mac-m2-ollama",
      "rung": "fleet",
      "url": "http://mac.local:11434",
      "healthy": true,
      "loaded_models": ["llama3.2:3b"],
      "capable_models": ["llama3.2:3b"],
      "cost_per_1k_tokens": 0,
      "p95_latency_ms": 900,
      "max_context": 8192
    },
    {
      "id": "cloud-gate",
      "rung": "gate",
      "url": "https://cloud.example/v1",
      "healthy": false,
      "capable_models": ["llama3.2:70b"],
      "cost_per_1k_tokens": 0.01,
      "p95_latency_ms": 1200,
      "max_context": 32768
    }
  ]
}
JSON

AEROCORE_ADDR="${ADDR}" \
AEROCORE_DEFAULT_UPSTREAM_URL="http://localhost:11434" \
AEROCORE_BACKENDS_FILE="${SMOKE_BACKENDS_FILE}" \
go run ./aerocore >"${LOG_FILE}" 2>&1 &

PID="$!"

cleanup() {
  if kill -0 "${PID}" >/dev/null 2>&1; then
    kill "${PID}" >/dev/null 2>&1 || true
    wait "${PID}" >/dev/null 2>&1 || true
  fi
  rm -f "${SMOKE_BACKENDS_FILE}"
}
trap cleanup EXIT

for _ in $(seq 1 60); do
  if ! kill -0 "${PID}" >/dev/null 2>&1; then
    echo "aerocore exited early"
    cat "${LOG_FILE}" || true
    exit 1
  fi

  if curl -fsS "${BASE_URL}/healthz" >/dev/null 2>&1; then
    break
  fi

  sleep 0.25
done

curl -fsS "${BASE_URL}/healthz" | grep -q '"status":"ok"'
curl -fsS "${BASE_URL}/readyz" | grep -q '"ready":true'
curl -fsS "${BASE_URL}/config" | grep -q '"default_upstream_configured":true'

BACKENDS_BODY="$(curl -fsS "${BASE_URL}/backends")"
echo "${BACKENDS_BODY}" | grep -q '"id":"mac-m2-ollama"'
echo "${BACKENDS_BODY}" | grep -q '"healthy":true'

RESOLVE_BODY="$(curl -fsS -X POST "${BASE_URL}/resolve" \
  -H 'content-type: application/json' \
  -H 'X-Aero-Request-Id: req_smoke_trace' \
  -d '{
    "request_id": "req_smoke",
    "model": "llama3.2:3b",
    "deadline_ms": 2000,
    "estimated_input_tokens": 512,
    "estimated_output_tokens": 128,
    "tier": "A"
  }')"

echo "${RESOLVE_BODY}" | grep -q '"decision":"route"'
echo "${RESOLVE_BODY}" | grep -q '"backend_id":"mac-m2-ollama"'
echo "${RESOLVE_BODY}" | grep -q '"backend_url":"http://mac.local:11434"'

METRICS_BODY="$(curl -fsS "${BASE_URL}/metrics")"

echo "${METRICS_BODY}" | grep -q 'aerocore_resolve_total'
echo "${METRICS_BODY}" | grep -q 'aerocore_ready 1'

grep -q 'aerocore_access request_id=req_smoke_trace method=POST path=/resolve status=200' "${LOG_FILE}"

echo "aerocore smoke ok"