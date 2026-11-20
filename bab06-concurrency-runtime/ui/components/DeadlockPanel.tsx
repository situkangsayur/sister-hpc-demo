"use client";

import { useState } from "react";
import { LogLine, useStream } from "./lib";

type Thread = { id: string; status: string; holds: string[]; waits_for?: string };
type Edge = { from: string; to: string; kind: string };
type Snap = { t_ms: number; threads: Thread[]; locks: string[]; edges: Edge[] | null; wait_for: Edge[] | null; cycle: string[] | null };
type Done = {
  status: string; duration_ms: number; transfers: number; expected_transfers: number; retries: number;
  saldo: Record<string, number>; total: number; expected_total: number;
};

const STRATEGIES: Record<string, { label: string; info: string }> = {
  naive: { label: "Naif (urutan lock berbeda)", info: "Ti mengunci rekening asal lalu tujuan. Arah transfer berlawanan → circular wait." },
  ordered: { label: "Urutan lock global", info: "Selalu kunci nama rekening terkecil dulu. Mematahkan syarat circular wait." },
  trylock: { label: "tryLock + timeout + jitter", info: "Bila lock kedua tidak didapat dalam timeout, lepas yang pertama dan mundur acak. Mematahkan hold and wait." },
  "trylock-nojitter": { label: "tryLock tanpa jitter", info: "Mundur dengan jeda yang sama persis: thread terus saling mengalah (livelock)." },
};

const STATUS_ID: Record<string, string> = {
  running: "berjalan", waiting: "menunggu", backoff: "mundur", done: "selesai", aborted: "dibatalkan",
};

function Graph({ snap }: { snap: Snap }) {
  const k = snap.threads.length;
  // Urutan cincin: rekA, T1, rekB, T2, ... sehingga siklus naif tampak sebagai cincin.
  const order: { id: string; kind: "t" | "l" }[] = [];
  for (let i = 0; i < k; i++) {
    order.push({ id: snap.locks[i], kind: "l" });
    order.push({ id: snap.threads[i].id, kind: "t" });
  }
  const R = 145, cx = 260, cy = 190, nr = 38;
  const pos: Record<string, { x: number; y: number }> = {};
  order.forEach((n, i) => {
    const a = (i / order.length) * Math.PI * 2 - Math.PI / 2;
    pos[n.id] = { x: cx + R * Math.cos(a), y: cy + R * Math.sin(a) };
  });
  const cyc = new Set(snap.cycle ?? []);
  const tmap = Object.fromEntries(snap.threads.map((t) => [t.id, t]));

  return (
    <div className="graph">
      <svg viewBox="0 0 520 380" role="img" aria-label="Graf resource-allocation: lock dipegang dan ditunggu thread">
        <defs>
          <marker id="ah" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
            <path d="M0,0 L10,5 L0,10 z" fill="var(--ink-2)" />
          </marker>
          <marker id="aw" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
            <path d="M0,0 L10,5 L0,10 z" fill="var(--bad)" />
          </marker>
        </defs>
        {(snap.edges ?? []).map((e, i) => {
          const a = pos[e.from], b = pos[e.to];
          if (!a || !b) return null;
          const dx = b.x - a.x, dy = b.y - a.y, d = Math.hypot(dx, dy) || 1;
          const x1 = a.x + (dx / d) * nr, y1 = a.y + (dy / d) * nr;
          const x2 = b.x - (dx / d) * (nr + 2), y2 = b.y - (dy / d) * (nr + 2);
          const inCycle = e.kind === "waits" ? cyc.has(e.from) : cyc.has(e.to);
          return (
            <line key={i} x1={x1} y1={y1} x2={x2} y2={y2} strokeWidth={2}
              className={`${e.kind === "waits" ? "waits" : "holds"} ${inCycle ? "cycle" : ""}`}
              markerEnd={e.kind === "waits" ? "url(#aw)" : "url(#ah)"} />
          );
        })}
        {order.map((n) => {
          const p = pos[n.id];
          const t = tmap[n.id];
          return n.kind === "t" ? (
            <g key={n.id}>
              <circle cx={p.x} cy={p.y} r={nr} className="node-t"
                style={cyc.has(n.id) ? { stroke: "var(--bad)", strokeWidth: 3 } : { strokeWidth: 1.5 }} />
              <text x={p.x} y={p.y - 2} textAnchor="middle">{n.id}</text>
              <text x={p.x} y={p.y + 14} textAnchor="middle" className="st">{STATUS_ID[t?.status] ?? t?.status ?? ""}</text>
            </g>
          ) : (
            <g key={n.id}>
              <rect x={p.x - nr} y={p.y - 18} width={nr * 2} height={36} rx={6} className="node-l" strokeWidth={1.5} />
              <text x={p.x} y={p.y + 5} textAnchor="middle">{n.id}</text>
            </g>
          );
        })}
      </svg>
      <div className="legend" style={{ justifyContent: "center" }}>
        <span style={{ ["--c" as string]: "var(--ink-2)" }}>lock → thread: dipegang</span>
        <span style={{ ["--c" as string]: "var(--bad)" }}>thread ⇢ lock: menunggu</span>
      </div>
    </div>
  );
}

export default function DeadlockPanel() {
  const [strategy, setStrategy] = useState("naive");
  const [threads, setThreads] = useState(2);
  const [hold, setHold] = useState(300);
  const [snap, setSnap] = useState<Snap | null>(null);
  const [log, setLog] = useState<LogLine[]>([]);
  const [done, setDone] = useState<Done | null>(null);
  const [detected, setDetected] = useState<string[] | null>(null);

  const { running, start, stop } = useStream({
    start: () => {},
    state: (d) => setSnap(d as Snap),
    log: (d) => setLog((l) => [...l.slice(-200), d as LogLine]),
    detected: (d) => setDetected((d as { cycle: string[] }).cycle),
    done: (d) => setDone(d as Done),
  });

  function run() {
    setLog([]); setDone(null); setDetected(null); setSnap(null);
    start(`deadlock/stream?strategy=${strategy}&threads=${threads}&hold_ms=${hold}&rounds=3`);
  }
  const wf = snap?.wait_for ?? [];

  return (
    <div className="panel">
      <div className="card">
        <h2>2. Deadlock dan wait-for graph</h2>
        <p className="theory">
          Thread T<sub>i</sub> mentransfer dari rekening ke-i ke rekening berikutnya, dan setiap transfer butuh dua lock.
          Deadlock butuh empat syarat Coffman sekaligus: mutual exclusion, hold and wait, no preemption, circular wait.
          Patahkan satu, deadlock hilang. Bab 6 subbab &ldquo;Deadlock&rdquo;.
        </p>
        <div className="controls">
          <label>Strategi
            <select value={strategy} onChange={(e) => setStrategy(e.target.value)} style={{ width: 240 }}>
              {Object.entries(STRATEGIES).map(([k, v]) => <option key={k} value={k}>{v.label}</option>)}
            </select>
          </label>
          <label>Jumlah thread
            <select value={threads} onChange={(e) => setThreads(+e.target.value)}>
              {[2, 3, 4].map((n) => <option key={n} value={n}>{n}</option>)}
            </select>
          </label>
          <label>Lama memegang lock (ms)<input type="number" min={20} max={2000} value={hold} onChange={(e) => setHold(+e.target.value)} /></label>
          <button className="btn" onClick={run} disabled={running}>{running ? "Berjalan…" : "Mulai"}</button>
          {running && <button className="btn ghost" onClick={stop}>Hentikan</button>}
        </div>
        <p className="muted">{STRATEGIES[strategy].info}</p>
      </div>
      <div className="grid2">
        <div className="card">
          <h3>Graf alokasi resource (live)</h3>
          {snap ? <Graph snap={snap} /> : <p className="muted">Tekan Mulai.</p>}
          <p style={{ textAlign: "center", margin: "6px 0 0" }}>
            Wait-for graph:{" "}
            {wf.length === 0 ? <span className="muted">tidak ada yang saling menunggu</span> :
              wf.map((e) => `${e.from} → ${e.to}`).join(",  ")}
            {snap?.cycle && snap.cycle.length > 0 && (
              <> <span className="badge bad">siklus: {[...snap.cycle, snap.cycle[0]].join(" → ")}</span></>
            )}
          </p>
        </div>
        <div className="card">
          <h3>Hasil</h3>
          {detected && <p><span className="badge bad">✗ DEADLOCK terdeteksi: {detected.join(" ↔ ")}</span></p>}
          {done ? (
            <table className="data">
              <tbody>
                <tr><th>Status</th><td>
                  {done.status === "selesai" ? <span className="badge good">✓ selesai</span>
                    : done.status === "livelock" ? <span className="badge warn">⚠ livelock / tanpa kemajuan</span>
                      : <span className="badge bad">✗ {done.status}</span>}
                </td></tr>
                <tr><th>Transfer selesai</th><td className="num">{done.transfers} / {done.expected_transfers}</td></tr>
                <tr><th>Coba ulang (retry)</th><td className="num">{done.retries}</td></tr>
                <tr><th>Durasi</th><td className="num">{done.duration_ms} ms</td></tr>
                <tr><th>Total saldo</th><td className="num">{done.total} (harus {done.expected_total})</td></tr>
                {Object.entries(done.saldo).sort().map(([k, v]) => (
                  <tr key={k}><th>{k}</th><td className="num">{v}</td></tr>
                ))}
              </tbody>
            </table>
          ) : <p className="muted">{running ? "Mengamati…" : "Belum ada hasil."}</p>}
          <h3 style={{ marginTop: 10 }}>Log</h3>
          <div className="log">
            {log.map((l, i) => <div key={i}><span className="t">{(l.t_ms / 1000).toFixed(2)}s</span>{l.msg}</div>)}
          </div>
        </div>
      </div>
    </div>
  );
}
