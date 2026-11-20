"use client";

import { useCallback, useEffect, useRef, useState } from "react";

// Semua panggilan API dari browser lewat /api/gw/... (proxy Next.js -> APISIX).
export const gw = (path: string) => `/api/gw/${path}`;

export type Health = { name: string; group: string; status: string; latency_ms: number; detail?: string };

export type Product = { id: string; name: string; description: string; category: string; price: number; score?: number };

export type SearchResult = {
  source: string;
  node?: string;
  took_ms: number;
  shards?: { total: number; successful: number };
  hits: Product[];
  error?: string;
};

export type Stock = { product_id: string; available: number; reserved: number };

export type Breaker = {
  name: string;
  state: string;
  requests: number;
  failures: number;
  successes: number;
  consecutive_failures: number;
  since_ms: number;
  timeout: string;
  attempts: number;
  transitions: string[];
};

export type ReplicaBreakers = { replica: string; up?: boolean; breakers?: Breaker[]; error?: string };

export type Item = { product_id: string; qty: number; price?: number };

export type Order = {
  id: string;
  customer: string;
  items: Item[];
  total: number;
  card: string;
  status: string;
  reason: string;
  request_id: string;
  served_by: string;
  precheck: string;
  created_at: string;
};

export type Saga = {
  order_id: string;
  request_id: string;
  steps: Record<string, string>;
  outcome: string;
  duplicates: number;
  redeliveries: number;
  first_seen: string;
};

export type Ev = {
  seq: number;
  at: string;
  kind: "publish" | "audit" | "lab";
  subject: string;
  from: string;
  to?: string[];
  msg_id?: string;
  request_id?: string;
  order_id?: string;
  action?: string;
  note?: string;
  delivered?: number;
  replica?: string;
};

export type Faults = {
  payment: { fail_rate: number } | null;
  inventory: { delay_ms: number; crash_before_ack: boolean; idempotent: boolean } | null;
  replicas: Record<string, { up: boolean; served: number } | null>;
};

export type LabStats = {
  published: { core: number; js: number };
  receiver: { offline: boolean; received: Record<string, number> } | null;
  js_pending: number;
};

export type OSInfo = {
  health: { status: string; number_of_nodes: number; active_shards: number; unassigned_shards: number } | null;
  nodes: { name: string; ip: string; "heap.percent": string; "heap.max": string; "node.role": string; cluster_manager: string }[] | null;
  shards: { index: string; shard: string; prirep: string; state: string; docs: string; node: string }[] | null;
};

export async function getJSON<T>(path: string): Promise<T> {
  const r = await fetch(gw(path), { cache: "no-store" });
  if (!r.ok) throw new Error(`HTTP ${r.status}`);
  return (await r.json()) as T;
}

export async function postJSON<T = unknown>(path: string, body: unknown): Promise<T> {
  const r = await fetch(gw(path), { method: "POST", body: JSON.stringify(body), headers: { "content-type": "application/json" } });
  return (await r.json()) as T;
}

// usePoll memanggil path setiap ms milidetik; refresh() memaksa panggilan segera.
export function usePoll<T>(path: string, ms: number): { data: T | null; error: string; refresh: () => void } {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState("");
  const pathRef = useRef(path);
  pathRef.current = path;
  const load = useCallback(() => {
    getJSON<T>(pathRef.current)
      .then((d) => {
        setData(d);
        setError("");
      })
      .catch((e) => setError(String(e)));
  }, []);
  useEffect(() => {
    load();
    const t = setInterval(load, ms);
    return () => clearInterval(t);
  }, [load, ms]);
  return { data, error, refresh: load };
}

export const rupiah = (n: number) => "Rp" + n.toLocaleString("id-ID");

export const shortTime = (iso: string) => {
  const d = new Date(iso);
  return d.toLocaleTimeString("id-ID", { hour12: false }) + "." + String(d.getMilliseconds()).padStart(3, "0");
};
