"use client";
import { useState } from "react";
import { Down } from "@/components/Down";
import { fmtTime, usePoll } from "@/lib/poll";

type Shard = { shard: string; prirep: string; state: string; node: string | null; docs: string | null; "unassigned.reason": string | null };
type Node = { name: string; ip: string; "node.role": string; cluster_manager: string; "heap.percent": string };
type Snap = {
  up: boolean; via: string; status: string; nodes: Node[] | null; shards: Shard[] | null; doc_count: number; error?: string;
  known_nodes: string[]; history: { at_ms: number; status: string }[]; events: { at_ms: number; kind: string; msg: string }[];
  health?: { number_of_nodes: number; active_primary_shards: number; active_shards: number; unassigned_shards: number; active_shards_percent_as_number: number };
};
type Search = { via: string; took_ms: number; total: number; shards: { total: number; successful: number; failed: number }; hits: { _id: string; _source: { nama: string; harga: number } }[] };

const color: Record<string, string> = { green: "var(--ok)", yellow: "var(--warn)", red: "var(--bad)", unavailable: "var(--bad)" };

export default function SearchPage() {
  const p = usePoll<Snap>("/api/search/cluster", 1000);
  const [q, setQ] = useState("kopi");
  const [res, setRes] = useState<Search | null>(null);
  const [err, setErr] = useState("");
  if (p.down) return (<><h1>Shard dan replica di OpenSearch</h1><Down profile="search" note="OpenSearch butuh sekitar 30-60 detik sampai kluster terbentuk. Heap 512 MB per node." /></>);
  const s = p.data;
  const shards = s?.shards ?? [];
  const shardIds = Array.from(new Set(shards.map((x) => x.shard))).sort();
  const nodeNames = Array.from(new Set([...(s?.known_nodes ?? []), ...(s?.nodes ?? []).map((n) => n.name)]));
  const live = new Set((s?.nodes ?? []).map((n) => n.name));
  const unassigned = shards.filter((x) => !x.node);
  const doSearch = async () => {
    setErr("");
    try {
      const r = await fetch(`/api/search/search?q=${encodeURIComponent(q)}`);
      const j = await r.json();
      if (!r.ok) throw new Error(j.error || `HTTP ${r.status}`);
      setRes(j);
    } catch (e) { setErr(String(e)); setRes(null); }
  };
  const cell = (x: Shard, i: number) => (
    <span key={i} className={`cell ${x.state === "UNASSIGNED" ? "un" : x.state !== "STARTED" ? "init" : x.prirep === "p" ? "p" : "r"}`} title={`shard ${x.shard} ${x.prirep === "p" ? "primary" : "replica"} ${x.state}${x["unassigned.reason"] ? " (" + x["unassigned.reason"] + ")" : ""}`}>
      {x.prirep.toUpperCase()}{x.shard}
    </span>
  );

  return (
    <>
      <h1>Shard dan replica di OpenSearch</h1>
      <p className="lead">
        Bab 7 subbab Replication dan Data Partitioning (slide P07). Index <code>katalog</code> dipecah menjadi 3 primary shard
        (P0-P2) dan setiap shard punya 1 replica (R0-R2). OpenSearch tidak pernah meletakkan primary dan replica shard yang sama
        di node yang sama. Matikan satu node data, lalu amati status yellow dan replica yang dipromosikan menjadi primary.
      </p>
      <div className="card">
        <div className="row">
          <span>Status kluster:</span>
          <span className="badge" style={{ color: color[s?.status ?? "unavailable"] }}>{s?.status ?? "memuat..."}</span>
          {s?.health && <span className="muted">{s.health.number_of_nodes} node, {s.health.active_primary_shards} primary aktif, {s.health.active_shards} shard aktif, {s.health.unassigned_shards} unassigned ({s.health.active_shards_percent_as_number.toFixed(0)}%)</span>}
          <span className="muted">dokumen: {s?.doc_count ?? 0}</span>
          {s?.via && <span className="muted">dijawab oleh {s.via.replace("http://", "")}</span>}
        </div>
        {s?.error && <p className="err">{s.error}</p>}
        <div style={{ display: "flex", height: 12, marginTop: 10, borderRadius: 4, overflow: "hidden" }} title="riwayat status 3 menit terakhir">
          {(s?.history ?? []).map((h, i) => <div key={i} style={{ flex: 1, background: color[h.status] ?? "var(--line)" }} title={`${fmtTime(h.at_ms)} ${h.status}`} />)}
        </div>
        <p className="muted">Pita di atas: riwayat status kluster per detik (hijau green, kuning yellow, merah red/tidak tersedia).</p>
      </div>

      <div className="card">
        <h2>Alokasi shard per node</h2>
        <div className="grid g3">
          {nodeNames.map((n) => {
            const nd = s?.nodes?.find((x) => x.name === n);
            const on = live.has(n);
            return (
              <div key={n} className="podcard" style={{ borderColor: on ? "var(--accent)" : "var(--bad)", borderStyle: on ? "solid" : "dashed" }}>
                <div className="row" style={{ justifyContent: "space-between" }}>
                  <b>{n}</b>
                  <span className={`badge ${on ? "ok" : "bad"}`}>{on ? "hidup" : "mati / keluar"}</span>
                </div>
                {nd && <div className="muted">role {nd["node.role"]}{nd.cluster_manager === "*" ? ", cluster manager" : ""}, heap {nd["heap.percent"]}%</div>}
                <div style={{ marginTop: 6 }}>{shards.filter((x) => x.node === n).map(cell)}</div>
              </div>
            );
          })}
          <div className="podcard" style={{ borderStyle: "dashed" }}>
            <b>UNASSIGNED</b>
            <div className="muted">shard yang belum punya tempat</div>
            <div style={{ marginTop: 6 }}>{unassigned.length ? unassigned.map(cell) : <span className="muted">tidak ada</span>}</div>
          </div>
        </div>
        <div className="row muted" style={{ marginTop: 8 }}>
          <span><span className="cell p">P</span> primary</span>
          <span><span className="cell r">R</span> replica</span>
          <span><span className="cell init">I</span> initializing / relocating</span>
          <span><span className="cell un">U</span> unassigned</span>
        </div>
        <div className="tablewrap">
          <table style={{ marginTop: 8 }}>
            <thead><tr><th>shard</th><th>primary di</th><th>replica di</th></tr></thead>
            <tbody>{shardIds.map((id) => {
              const pr = shards.find((x) => x.shard === id && x.prirep === "p");
              const rp = shards.find((x) => x.shard === id && x.prirep === "r");
              return <tr key={id}><td>{id}</td><td>{pr?.node ?? <span className="bad">tidak ada</span>} <span className="muted">{pr?.state}</span></td><td>{rp?.node ?? <span className="warn">unassigned</span>} <span className="muted">{rp?.state}</span></td></tr>;
            })}</tbody>
          </table>
        </div>
      </div>

      <div className="grid g2">
        <div className="card">
          <h2>Mematikan node (jalankan di terminal)</h2>
          <pre>{`docker compose --profile search stop os-node2    # status menjadi yellow
docker compose --profile search start os-node2   # replica dibangun ulang, kembali green
docker compose --profile search stop os-node1    # node1 satu-satunya cluster manager:
                                # kluster tidak tersedia`}</pre>
          <p className="muted">Kluster dua node hanya punya satu cluster-manager-eligible (os-node1) agar status yellow dapat dipertunjukkan. Mematikan os-node1 memperlihatkan mengapa produksi memakai tiga node: tanpa mayoritas, kluster tidak bisa memilih cluster manager (bandingkan dengan quorum Raft).</p>
        </div>
        <div className="card">
          <h2>Kejadian</h2>
          <ul>{[...(s?.events ?? [])].reverse().slice(0, 14).map((e, i) => <li key={i} className={e.kind === "promote" ? "info" : e.kind === "node" ? "warn" : ""}><code>{fmtTime(e.at_ms)}</code> {e.msg}</li>)}</ul>
        </div>
      </div>

      <div className="card">
        <h2>Pencarian tetap jalan saat status yellow</h2>
        <div className="row">
          <input value={q} onChange={(e) => setQ(e.target.value)} />
          <button className="primary" onClick={doSearch}>Cari nama</button>
        </div>
        {err && <p className="err">{err}</p>}
        {res && (
          <>
            <p>{res.total} hasil dalam {res.took_ms} ms; shard ditanya {res.shards.total}, berhasil {res.shards.successful}, gagal {res.shards.failed}; dijawab {res.via.replace("http://", "")}.</p>
            <ul>{res.hits.map((h) => <li key={h._id}>{h._source.nama} <span className="muted">Rp{h._source.harga.toLocaleString("id-ID")}</span></li>)}</ul>
          </>
        )}
      </div>
    </>
  );
}
