#!/usr/bin/env bash
set -euo pipefail

PORT="${AERORIG_SMOKE_PORT:-18080}"
TMPDIR="$(mktemp -d)"
LOG_FILE="${TMPDIR}/http.log"

cleanup() {
  if [[ -n "${PID:-}" ]]; then
    kill "${PID}" >/dev/null 2>&1 || true
  fi
  rm -rf "${TMPDIR}"
}
trap cleanup EXIT

printf 'aerorig smoke\n' > "${TMPDIR}/index.html"

(
  cd "${TMPDIR}"
  python3 -m http.server "${PORT}" --bind 127.0.0.1 >"${LOG_FILE}" 2>&1
) &
PID="$!"

READY=0
for _ in $(seq 1 50); do
  if curl -fsS "http://127.0.0.1:${PORT}/" >/dev/null 2>&1; then
    READY=1
    break
  fi
  sleep 0.1
done

if [[ "${READY}" != "1" ]]; then
  echo "smoke server did not become ready"
  cat "${LOG_FILE}" || true
  exit 1
fi

mkdir -p out

set +e
go run ./aero-rig probe-http \
  -name local-smoke \
  -target "http://127.0.0.1:${PORT}/" \
  -count 3 \
  -timeout 2s \
  -out out/smoke_http.jsonl
STATUS="$?"
set -e

cat out/smoke_http.jsonl || true

if [[ "${STATUS}" != "0" ]]; then
  echo "aerorig probe failed"
  cat "${LOG_FILE}" || true
  exit "${STATUS}"
fi

test -s out/smoke_http.jsonl
grep -q '"ok":true' out/smoke_http.jsonl