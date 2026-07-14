#!/usr/bin/env bash
set -euo pipefail

DIRECT_URL="${AERORIG_DIRECT_URL:-http://127.0.0.1:11434/v1/chat/completions}"
CACHE_URL="${AERORIG_CACHE_URL:-http://127.0.0.1:8080/v1/chat/completions}"
MODEL="${AERORIG_MODEL:-llama3.2:3b}"
COUNT="${AERORIG_COUNT:-2}"
TIMEOUT="${AERORIG_TIMEOUT:-60s}"
OUT="${AERORIG_BENCH_OUT:-out/benchmarks/cache-vs-direct}"

DIRECT_DIR="${OUT}/direct"
CACHE_DIR="${OUT}/cache"
COMPARE_PATH="${OUT}/compare.json"

mkdir -p "${DIRECT_DIR}" "${CACHE_DIR}"

if [[ -x bin/aerorig ]]; then
  AERORIG_CMD=(./bin/aerorig)
else
  AERORIG_CMD=(go run ./aero-rig)
fi

echo "AeroRig cache-vs-direct benchmark"
echo "direct_url=${DIRECT_URL}"
echo "cache_url=${CACHE_URL}"
echo "model=${MODEL}"
echo "count=${COUNT}"
echo "timeout=${TIMEOUT}"
echo "out=${OUT}"

cat > "${DIRECT_DIR}/suite.json" <<EOF_JSON
{
  "name": "cache-vs-direct-direct",
  "output_dir": "${DIRECT_DIR}",
  "chat": [
    {
      "name": "exact-pong-chat",
      "target": "${DIRECT_URL}",
      "model": "${MODEL}",
      "prompt": "Return exactly: pong",
      "count": ${COUNT},
      "timeout": "${TIMEOUT}"
    },
    {
      "name": "short-summary-chat",
      "target": "${DIRECT_URL}",
      "model": "${MODEL}",
      "prompt": "Summarize this in one sentence: Aero measures cache hits, latency, TTFT, and cost evidence for repeated inference requests.",
      "count": ${COUNT},
      "timeout": "${TIMEOUT}"
    }
  ],
  "chat_stream": [
    {
      "name": "exact-pong-stream",
      "target": "${DIRECT_URL}",
      "model": "${MODEL}",
      "prompt": "Return exactly: pong",
      "count": ${COUNT},
      "timeout": "${TIMEOUT}"
    },
    {
      "name": "short-summary-stream",
      "target": "${DIRECT_URL}",
      "model": "${MODEL}",
      "prompt": "Summarize this in one sentence: Aero measures cache hits, latency, TTFT, and cost evidence for repeated inference requests.",
      "count": ${COUNT},
      "timeout": "${TIMEOUT}"
    }
  ]
}
EOF_JSON

cat > "${CACHE_DIR}/suite.json" <<EOF_JSON
{
  "name": "cache-vs-direct-cache",
  "output_dir": "${CACHE_DIR}",
  "chat": [
    {
      "name": "exact-pong-chat",
      "target": "${CACHE_URL}",
      "model": "${MODEL}",
      "prompt": "Return exactly: pong",
      "count": ${COUNT},
      "timeout": "${TIMEOUT}",
      "proof": {
        "require_cache_hit": true,
        "require_verified_hit": true,
        "require_miss_hit": true
      }
    },
    {
      "name": "short-summary-chat",
      "target": "${CACHE_URL}",
      "model": "${MODEL}",
      "prompt": "Summarize this in one sentence: Aero measures cache hits, latency, TTFT, and cost evidence for repeated inference requests.",
      "count": ${COUNT},
      "timeout": "${TIMEOUT}"
    }
  ],
  "chat_stream": [
    {
      "name": "exact-pong-stream",
      "target": "${CACHE_URL}",
      "model": "${MODEL}",
      "prompt": "Return exactly: pong",
      "count": ${COUNT},
      "timeout": "${TIMEOUT}"
    },
    {
      "name": "short-summary-stream",
      "target": "${CACHE_URL}",
      "model": "${MODEL}",
      "prompt": "Summarize this in one sentence: Aero measures cache hits, latency, TTFT, and cost evidence for repeated inference requests.",
      "count": ${COUNT},
      "timeout": "${TIMEOUT}"
    }
  ]
}
EOF_JSON

echo "running direct suite"
"${AERORIG_CMD[@]}" run-suite -manifest "${DIRECT_DIR}/suite.json"

echo "building direct matrix"
"${AERORIG_CMD[@]}" build-matrix \
  -suite-result "${DIRECT_DIR}/suite_result.json" \
  -out "${DIRECT_DIR}/matrix.json"

echo "running cache suite"
"${AERORIG_CMD[@]}" run-suite -manifest "${CACHE_DIR}/suite.json"

echo "building cache matrix"
"${AERORIG_CMD[@]}" build-matrix \
  -suite-result "${CACHE_DIR}/suite_result.json" \
  -out "${CACHE_DIR}/matrix.json"

echo "comparing cache vs direct"
"${AERORIG_CMD[@]}" compare-matrix \
  -baseline "${DIRECT_DIR}/matrix.json" \
  -candidate "${CACHE_DIR}/matrix.json" \
  -out "${COMPARE_PATH}"

echo "benchmark artifacts:"
echo "  direct suite:  ${DIRECT_DIR}/suite_result.json"
echo "  direct matrix: ${DIRECT_DIR}/matrix.json"
echo "  cache suite:   ${CACHE_DIR}/suite_result.json"
echo "  cache matrix:  ${CACHE_DIR}/matrix.json"
echo "  comparison:    ${COMPARE_PATH}"