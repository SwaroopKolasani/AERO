import type { AeroReceipt, FireResult, StatsSnapshot } from "./types";

export type PromptRequest = {
  model: string;
  prompt: string;
  temperature: number;
  max_tokens: number;
  stream: boolean;
};

export async function fetchStats(): Promise<StatsSnapshot> {
  const res = await fetch("/stats", {
    method: "GET",
    headers: {
      "accept": "application/json"
    }
  });

  if (!res.ok) {
    throw new Error(`stats failed: ${res.status}`);
  }

  return await res.json();
}

export async function firePrompt(req: PromptRequest): Promise<FireResult> {
  const started = performance.now();

  const res = await fetch("/v1/chat/completions", {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "accept": "application/json,text/event-stream"
    },
    body: JSON.stringify({
      model: req.model,
      messages: [
        {
          role: "user",
          content: req.prompt
        }
      ],
      temperature: req.temperature,
      max_tokens: req.max_tokens,
      stream: req.stream
    })
  });

  const bodyText = await res.text();
  const totalMs = performance.now() - started;

  const headers = collectHeaders(res);
  const bodyJSON = parseJSON(bodyText);
  const receipt = buildReceiptFromHeaders(headers, totalMs, bodyText);

  return {
    receipt,
    status: res.status,
    headers,
    bodyText,
    bodyJSON,
    ok: res.ok
  };
}

function collectHeaders(res: Response): Record<string, string> {
  const out: Record<string, string> = {};

  res.headers.forEach((value, key) => {
    out[key.toLowerCase()] = value;
  });

  return out;
}

function parseJSON(text: string): unknown | null {
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

function buildReceiptFromHeaders(
  headers: Record<string, string>,
  totalMs: number,
  bodyText: string
): AeroReceipt {
  const cache = normalizeCache(headers["x-aero-cache"]);
  const tier = headers["x-aero-tier"] || "unknown";
  const tokensOut = numberHeader(headers["x-aero-tokens-out"]);
  const cost = numberHeader(headers["x-aero-cost-estimate-usd"]);
  const latency = numberHeader(headers["x-aero-latency-ms"]);

  return {
    request_id: headers["x-aero-request-id"],
    key_prefix: trimKey(headers["x-aero-key"]),
    tier,
    cache,
    verified: cache === "hit" || cache === "coalesced",
    ttft_ms: undefined,
    total_ms: latency > 0 ? latency : totalMs,
    cost_usd: cost,
    gpu_seconds: cache === "hit" || cache === "coalesced" ? 0 : 0,
    tokens_out: tokensOut,
    answer_sha256: simpleBodyFingerprint(bodyText),
    tier_b: false
  };
}

function normalizeCache(v: string | undefined): AeroReceipt["cache"] {
  if (v === "hit" || v === "miss" || v === "coalesced" || v === "bypass" || v === "error") {
    return v;
  }

  return "unknown";
}

function numberHeader(v: string | undefined): number {
  if (!v) {
    return 0;
  }

  const n = Number(v);
  if (!Number.isFinite(n)) {
    return 0;
  }

  return n;
}

function trimKey(v: string | undefined): string | undefined {
  if (!v) {
    return undefined;
  }

  if (v.length <= 18) {
    return v;
  }

  return `${v.slice(0, 18)}…`;
}

function simpleBodyFingerprint(text: string): string {
  let h = 2166136261;

  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }

  return `fnv32:${(h >>> 0).toString(16).padStart(8, "0")}`;
}