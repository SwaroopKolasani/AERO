#!/usr/bin/env python3

import argparse
import concurrent.futures
import json
import statistics
import time
import urllib.error
import urllib.request
from collections import Counter


def post_json(url: str, body: dict, timeout: float) -> dict:
    raw = json.dumps(body).encode("utf-8")

    req = urllib.request.Request(
        url,
        data=raw,
        method="POST",
        headers={
            "Content-Type": "application/json",
            "Accept": "application/json",
        },
    )

    started = time.perf_counter()

    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            payload = resp.read()
            elapsed_ms = (time.perf_counter() - started) * 1000

            return {
                "ok": True,
                "status": resp.status,
                "latency_ms": elapsed_ms,
                "cache": resp.headers.get("X-Aero-Cache", ""),
                "tier": resp.headers.get("X-Aero-Tier", ""),
                "bypass_reason": resp.headers.get("X-Aero-Bypass-Reason", ""),
                "bytes": len(payload),
                "error": "",
            }

    except urllib.error.HTTPError as e:
        payload = e.read()
        elapsed_ms = (time.perf_counter() - started) * 1000

        return {
            "ok": False,
            "status": e.code,
            "latency_ms": elapsed_ms,
            "cache": e.headers.get("X-Aero-Cache", ""),
            "tier": e.headers.get("X-Aero-Tier", ""),
            "bypass_reason": e.headers.get("X-Aero-Bypass-Reason", ""),
            "bytes": len(payload),
            "error": str(e),
        }

    except Exception as e:
        elapsed_ms = (time.perf_counter() - started) * 1000

        return {
            "ok": False,
            "status": 0,
            "latency_ms": elapsed_ms,
            "cache": "",
            "tier": "",
            "bypass_reason": "",
            "bytes": 0,
            "error": repr(e),
        }


def percentile(values, pct):
    if not values:
        return 0.0

    values = sorted(values)
    idx = int(round((pct / 100.0) * (len(values) - 1)))
    return values[idx]


def main():
    parser = argparse.ArgumentParser(description="AeroCache hot-key load test")
    parser.add_argument("--url", default="http://127.0.0.1:8080/v1/chat/completions")
    parser.add_argument("--model", default="llama3.2:3b")
    parser.add_argument("--requests", type=int, default=200)
    parser.add_argument("--concurrency", type=int, default=20)
    parser.add_argument("--timeout", type=float, default=60.0)
    parser.add_argument("--prompt", default="Say exactly pong.")
    parser.add_argument("--warmup", type=int, default=2)
    args = parser.parse_args()

    body = {
        "model": args.model,
        "temperature": 0,
        "messages": [
            {"role": "system", "content": "You are concise."},
            {"role": "user", "content": args.prompt},
        ],
    }

    print("warmup:")
    for i in range(args.warmup):
        result = post_json(args.url, body, args.timeout)
        print(
            f"  {i + 1}: status={result['status']} "
            f"cache={result['cache']} tier={result['tier']} "
            f"latency_ms={result['latency_ms']:.2f}"
        )

    print()
    print(
        f"load: requests={args.requests} "
        f"concurrency={args.concurrency} url={args.url}"
    )

    started = time.perf_counter()

    results = []

    with concurrent.futures.ThreadPoolExecutor(max_workers=args.concurrency) as pool:
        futures = [
            pool.submit(post_json, args.url, body, args.timeout)
            for _ in range(args.requests)
        ]

        for future in concurrent.futures.as_completed(futures):
            results.append(future.result())

    elapsed_s = time.perf_counter() - started

    latencies = [r["latency_ms"] for r in results]
    ok = [r for r in results if r["ok"] and 200 <= r["status"] < 300]
    failed = [r for r in results if not (r["ok"] and 200 <= r["status"] < 300)]

    cache_counts = Counter(r["cache"] or "<empty>" for r in results)
    tier_counts = Counter(r["tier"] or "<empty>" for r in results)
    status_counts = Counter(str(r["status"]) for r in results)
    bypass_counts = Counter(r["bypass_reason"] or "<empty>" for r in results)

    print()
    print("summary:")
    print(f"  total_requests: {len(results)}")
    print(f"  ok: {len(ok)}")
    print(f"  failed: {len(failed)}")
    print(f"  elapsed_s: {elapsed_s:.3f}")
    print(f"  throughput_rps: {len(results) / elapsed_s:.2f}")

    print()
    print("latency_ms:")
    print(f"  min: {min(latencies):.2f}" if latencies else "  min: 0.00")
    print(f"  p50: {statistics.median(latencies):.2f}" if latencies else "  p50: 0.00")
    print(f"  p95: {percentile(latencies, 95):.2f}")
    print(f"  p99: {percentile(latencies, 99):.2f}")
    print(f"  max: {max(latencies):.2f}" if latencies else "  max: 0.00")

    print()
    print("status_counts:")
    for k, v in sorted(status_counts.items()):
        print(f"  {k}: {v}")

    print()
    print("cache_counts:")
    for k, v in sorted(cache_counts.items()):
        print(f"  {k}: {v}")

    print()
    print("tier_counts:")
    for k, v in sorted(tier_counts.items()):
        print(f"  {k}: {v}")

    print()
    print("bypass_reason_counts:")
    for k, v in sorted(bypass_counts.items()):
        print(f"  {k}: {v}")

    if failed:
        print()
        print("sample_errors:")
        for r in failed[:5]:
            print(f"  status={r['status']} error={r['error']}")

    hit_count = cache_counts.get("hit", 0)
    coalesced_count = cache_counts.get("coalesced", 0)
    miss_count = cache_counts.get("miss", 0)
    bypass_count = cache_counts.get("bypass", 0)

    print()
    print("aerocache:")
    print(f"  hit_ratio: {hit_count / len(results):.4f}")
    print(f"  coalesced_ratio: {coalesced_count / len(results):.4f}")
    print(f"  miss_ratio: {miss_count / len(results):.4f}")
    print(f"  bypass_ratio: {bypass_count / len(results):.4f}")


if __name__ == "__main__":
    main()