#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${AEROCORE_SMOKE_PORT:-8088}"
ADDR=":${PORT}"
BASE_URL="http://localhost:${PORT}"
LOG_FILE="${TMPDIR:-/tmp}/aerocore-smoke.log"

cd "${ROOT_DIR}"

rm -f "${LOG_FILE}"

AEROCORE_ADDR="${ADDR}" \
AEROCORE_DEFAULT_UPSTREAM_URL="http://localhost:11434" \
AEROCORE_BACKENDS_FILE="internal/config/backends.example.json" \
go run ./aerocore >"${LOG_FILE}" 2>&1 &

PID="$!"

cleanup() {
  if kill -0 "${PID}" >/dev/null 2>&1; then
    kill "${PID}" >/dev/null 2>&1 || true
    wait "${PID}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

for _ in $(seq 1 40); do
  if curl -fsS "${BASE_URL}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done

curl -fsS "${BASE_URL}/healthz" | grep -q '"status":"ok"'

curl -fsS "${BASE_URL}/readyz" | grep -q '"ready":true'

curl -fsS "${BASE_URL}/config" | grep -q '"default_upstream_configured":true'

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