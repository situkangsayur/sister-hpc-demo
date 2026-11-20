// Bentuk data yang dikirim service monitor lewat SSE (/api/stream).

export type Fault = {
  mode: "normal" | "hang" | "mati";
  latency_ms: number;
  jitter_ms: number;
  drop_request_pct: number;
  drop_response_pct: number;
  skew_ms: number;
};

export type Status = "hidup" | "lambat" | "dicurigai" | "mati";

export type NodeState = {
  nama: string;
  status: Status;
  rtt_ms: number;
  gagal_beruntun: number;
  terakhir_ok_ms: number;
  offset_ms: number;
  a_heartbeat: number;
  fault: Fault | null;
};

export type Bucket = {
  t: number;
  per_node: Record<string, number>;
  sukses: number;
  gagal: number;
  retry: number;
  p50: number;
  p95: number;
  maks: number;
};

export type Snapshot = {
  waktu_ms: number;
  nodes: NodeState[];
  lalu_lintas: { aktif: boolean; rps: number; metode: "baca" | "tulis" };
  seri: Bucket[];
  kumulatif: {
    kirim: number;
    sukses: number;
    gagal: number;
    retry: number;
    per_node: Record<string, number>;
    gagal_jenis: Record<string, number>;
    tulis_sukses: number;
    tulis_gagal: number;
  };
  db: { ok: boolean; total: number; per_node: Record<string, number> | null; urutan_jam_mundur: number };
  availability: {
    per_node: Record<string, number>;
    lb: number;
    prediksi_paralel: number;
    prediksi_jalur: number;
    terukur_sistem: number;
  };
};

export const NODE_WARNA: Record<string, string> = {
  node1: "var(--series-1)",
  node2: "var(--series-2)",
  node3: "var(--series-3)",
};

export const STATUS_INFO: Record<Status, { ikon: string; label: string; warna: string }> = {
  hidup: { ikon: "●", label: "hidup", warna: "var(--status-good)" },
  lambat: { ikon: "◐", label: "lambat", warna: "var(--status-warning)" },
  dicurigai: { ikon: "?", label: "dicurigai", warna: "var(--status-serious)" },
  mati: { ikon: "✕", label: "mati", warna: "var(--status-critical)" },
};

export async function kirim(path: string, body?: unknown) {
  const r = await fetch(`/api/${path}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const isi = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(isi.error ?? `HTTP ${r.status}`);
  return isi;
}

export const persen = (x: number, d = 1) => `${(x * 100).toFixed(d).replace(".", ",")}%`;
export const angka = (x: number, d = 1) => x.toFixed(d).replace(".", ",");
