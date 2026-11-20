// Tipe data dari service prober dan helper fetch.

export type Netem = { delay_ms: number; jitter_ms: number; loss_pct: number; rate_mbit: number };

export type NetemStatus = {
  params: Netem;
  summary: string;
  c2s: { iface: string; tc: string };
  s2c: { iface: string; tc: string };
};

export type Sample = { seq: number; rtt_ms: number; lost: boolean; t_ms: number };
export type Stats = { count: number; min: number; median: number; p95: number; max: number; mean: number };
export type Throughput = {
  sent_bytes: number;
  delivered_bytes: number;
  ms: number;
  mbps: number;
  loss_pct: number;
  note?: string;
  error?: string;
};
export type ProtoResult = {
  proto: "tcp" | "udp" | "quic";
  status: string;
  error?: string;
  sent: number;
  received: number;
  loss_pct: number;
  stats: Stats;
  samples: Sample[];
  throughput?: Throughput;
};
export type Handshake = {
  name: string;
  handshake_ms: number;
  first_reply_ms: number;
  runs: number[] | null;
  theory_rtt: string;
  note?: string;
  error?: string;
};
export type Run = {
  id: number;
  status: "idle" | "running" | "done";
  phase?: string;
  netem_text?: string;
  params?: { count: number; interval_ms: number; payload: number; bulk_kb: number };
  protocols?: ProtoResult[];
  handshakes?: Handshake[] | null;
};
export type RunSummary = {
  id: number;
  netem_text: string;
  protos: Record<string, { median: number; max: number; p95: number; loss_pct: number; mbps: number }>;
};

export type HOLSample = { stream: number; seq: number; t_ms: number; lat_ms: number; lost: boolean };
export type HOLResult = {
  transport: string;
  description: string;
  samples: HOLSample[] | null;
  per_stream: { stream: number; stats: Stats }[] | null;
  stalls: number;
  shared_pct: number;
  threshold_ms: number;
  duration_ms: number;
  error?: string;
};

export type TraceEvent = { ts: number; side: "client" | "server"; call: string; bytes: number; detail: string };
export type Flow = {
  dir: "c2s" | "s2c";
  send_ts: number | null;
  recv_ts: number | null;
  kind: string;
  flags?: string;
  seq?: string;
  ack?: string;
  len: number;
  info: string;
  retrans: boolean;
};
export type TraceResult = {
  proto: string;
  netem_text: string;
  client: string;
  server: string;
  events: TraceEvent[] | null;
  flows: Flow[] | null;
  lost_packets: number;
  retrans_packets: number;
  client_tcpdump: string[] | null;
  server_tcpdump: string[] | null;
  tcpdump_cmd: string;
  error?: string;
};

export const COLORS: Record<string, string> = { tcp: "#2563eb", udp: "#ea580c", quic: "#16a34a" };
export const STREAM_COLORS = ["#2563eb", "#d97706", "#9333ea", "#0d9488", "#db2777", "#4b5563"];

export async function api<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/api/prober/${path}`, {
    method: body === undefined ? "GET" : "POST",
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    cache: "no-store",
  });
  const data = await res.json();
  if (!res.ok) throw new Error(data?.error ?? `HTTP ${res.status}`);
  return data as T;
}

export function fmt(x: number | undefined | null, digits = 1): string {
  if (x === undefined || x === null || Number.isNaN(x)) return "-";
  return x.toLocaleString("id-ID", { maximumFractionDigits: digits, minimumFractionDigits: 0 });
}

// Jumlah packet yang dibuang netem, dari keluaran `tc -s qdisc show`.
export function droppedFrom(tc: string): number | null {
  const m = tc.match(/dropped (\d+)/);
  return m ? Number(m[1]) : null;
}
