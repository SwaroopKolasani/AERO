export type CacheResult = "hit" | "miss" | "coalesced" | "bypass" | "error" | "unknown";

export type AeroReceipt = {
  request_id?: string;
  key_prefix?: string;
  tier: string;
  cache: CacheResult;
  verified: boolean;
  ttft_ms?: number;
  total_ms: number;
  cost_usd: number;
  gpu_seconds: number;
  tokens_out: number;
  answer_sha256?: string;
  tier_b: boolean;
};

export type FireResult = {
  receipt: AeroReceipt;
  status: number;
  headers: Record<string, string>;
  bodyText: string;
  bodyJSON: unknown | null;
  ok: boolean;
};

export type StatsSnapshot = {
  started_at?: string;
  uptime_seconds?: number;
  requests: number;
  hits: number;
  misses: number;
  coalesced: number;
  bypass: number;
  errors: number;
  hit_ratio: number;
  gpu_seconds_saved: number;
  usd_saved: number;
  tokens_out: number;
  verify_mismatch: number;
  writeback_queue_depth: number;
  writeback_dropped: number;
  upstream_calls: number;
  per_tier?: Record<string, {
    requests: number;
    hits: number;
    misses: number;
    bypass: number;
    coalesced: number;
    errors: number;
    latency_ms_avg: number;
  }>;
  per_endpoint?: Record<string, {
    requests: number;
    latency_ms_avg: number;
  }>;
  tier_a?: {
    requests: number;
    hits: number;
    hit_ratio: number;
    verify_mismatch: number;
  };
  tier_b?: {
    requests: number;
    hits: number;
    enabled: boolean;
  };
};