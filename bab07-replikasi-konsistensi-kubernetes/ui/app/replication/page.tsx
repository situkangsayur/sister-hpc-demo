"use client";
import { useState } from "react";
import { Down } from "@/components/Down";
import { LineChart } from "@/components/LineChart";
import { post, usePoll } from "@/lib/poll";

type Status = {
  primary_up: boolean; replica_up: boolean; primary_lsn: string; replica_receive_lsn: string; replica_replay_lsn: string;
  lag_bytes: number; replica_state: string; sync_state: string; write_lag_s: number; flush_lag_s: number; replay_lag_s: number;
  synchronous_standby_names: string; synchronous_commit: string; recovery_min_apply_delay: string; replica_in_recovery: boolean;
  harga_primary: number; harga_replica: number; mode: string; error?: string;
};
type Resp = { status: Status; history: { at_ms: number; lag_bytes: number; replay_lag_s: number; replica_up: boolean }[] };
type Read = { who: string; model: string; source: string; harga: number; fresh: boolean; ms: number; note?: string };
type Scen = { harga_lama: number; harga_baru: number; write_lsn: string; write_ms: number; reads: Read[]; convergence: { t_ms: number; harga: number }[]; converged_ms: number; status: Status };

// Postgres menampilkan durasi sebagai "0", "500ms", "3s", atau "1min".
function toMs(v: string): number {
  const m = /^(\d+)\s*(ms|s|min)?$/.exec(v.trim());
  if (!m) return -1;
  const n = Number(m[1]);
  return m[2] === "s" ? n * 1000 : m[2] === "min" ? n * 60000 : n;
}

const rupiah = (n: number) => (n / 1000).toLocaleString("id-ID") + "rb";

export default function ReplicationPage() {
  const p = usePoll<Resp>("/api/repl/status", 1000);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [scen, setScen] = useState<Scen | null>(null);
  const [harga, setHarga] = useState(12000);
  if (p.down) return (<><h1>Replication dan consistency model</h1><Down profile="replication" /></>);
  const s = p.data?.status;
  const run = async (f: () => Promise<void>) => { setBusy(true); setErr(""); try { await f(); } catch (e) { setErr(String(e)); } setBusy(false); p.refresh(); };
  const hist = p.data?.history ?? [];
  const t0 = hist[0]?.at_ms ?? 0;
  const delayMs = toMs(s?.recovery_min_apply_delay ?? "0");

  return (
    <>
      <h1>Replication dan consistency model</h1>
      <p className="lead">
        Bab 7 subbab Replication (leader-follower) dan Consistency Model (slide P07). PostgreSQL 16 primary menyalin WAL
        ke satu replica lewat streaming replication. Klien Go membaca dari primary atau replica menurut consistency model
        yang dipilih. <code>recovery_min_apply_delay</code> di replica dipakai untuk membuat replication lag yang terlihat.
      </p>
      {err && <p className="err">{err}</p>}
      {s?.error && <p className="warn">{s.error}</p>}
      <div className="grid g3">
        <div className="card">
          <h2>Primary (leader)</h2>
          <dl className="kv">
            <dt>status</dt><dd className={s?.primary_up ? "ok" : "bad"}>{s?.primary_up ? "up" : "down"}</dd>
            <dt>harga barang 1</dt><dd>{s ? rupiah(s.harga_primary) : "-"}</dd>
            <dt>WAL LSN</dt><dd>{s?.primary_lsn}</dd>
            <dt>mode</dt><dd>{s?.mode === "sync" ? "synchronous (remote_apply)" : "asynchronous"}</dd>
            <dt>synchronous_standby_names</dt><dd>{s?.synchronous_standby_names || "''"}</dd>
          </dl>
        </div>
        <div className="card">
          <h2>Replica (follower)</h2>
          <dl className="kv">
            <dt>status</dt><dd className={s?.replica_up ? "ok" : "bad"}>{s?.replica_up ? (s.replica_in_recovery ? "up, hot standby" : "up") : "down"}</dd>
            <dt>harga barang 1</dt><dd className={s && s.harga_replica !== s.harga_primary ? "warn" : ""}>{s ? rupiah(s.harga_replica) : "-"}{s && s.harga_replica !== s.harga_primary ? " (usang)" : ""}</dd>
            <dt>receive LSN</dt><dd>{s?.replica_receive_lsn}</dd>
            <dt>replay LSN</dt><dd>{s?.replica_replay_lsn}</dd>
            <dt>apply delay</dt><dd>{s?.recovery_min_apply_delay}</dd>
          </dl>
        </div>
        <div className="card">
          <h2>Replication lag</h2>
          <dl className="kv">
            <dt>state</dt><dd>{s?.replica_state || "-"} / {s?.sync_state || "-"}</dd>
            <dt>lag (byte WAL)</dt><dd className={s && s.lag_bytes > 0 ? "warn" : "ok"}>{s?.lag_bytes ?? 0}</dd>
            <dt>write / flush lag</dt><dd>{s?.write_lag_s.toFixed(3)} s / {s?.flush_lag_s.toFixed(3)} s</dd>
            <dt>replay lag</dt><dd>{s?.replay_lag_s.toFixed(3)} s</dd>
          </dl>
          <LineChart label="replay lag" unit=" s" points={hist.map((h) => ({ x: h.at_ms - t0, y: h.replay_lag_s }))} height={110} />
        </div>
      </div>

      <div className="card">
        <h2>Pengaturan replication</h2>
        <div className="row">
          <span>Mode:</span>
          <button className={s?.mode === "async" ? "primary" : ""} disabled={busy} onClick={() => run(async () => { await post("/api/repl/mode", { mode: "async" }); })}>asynchronous</button>
          <button className={s?.mode === "sync" ? "primary" : ""} disabled={busy} onClick={() => run(async () => { await post("/api/repl/mode", { mode: "sync" }); })}>synchronous (remote_apply)</button>
          <span style={{ marginLeft: 16 }}>Apply delay replica:</span>
          {[0, 1000, 3000, 5000].map((ms) => (
            <button key={ms} className={delayMs === ms ? "primary" : ""} disabled={busy} onClick={() => run(async () => { await post("/api/repl/delay", { ms }); })}>{ms} ms</button>
          ))}
        </div>
        <p className="muted">Pada mode synchronous dengan <code>synchronous_commit=remote_apply</code>, commit di primary menunggu replica selesai menerapkan tulisan. Tulisan menjadi lambat (kira-kira sebesar apply delay), tetapi membaca dari replica sesudahnya selalu segar.</p>
      </div>

      <div className="card">
        <h2>Skenario Ani dan Budi (Gambar Ani-Budi di buku)</h2>
        <p>Ani (penjual) mengubah harga di primary, lalu Ani dan Budi (pembeli) langsung membaca. Bacaan dilakukan dengan tiga consistency model.</p>
        <div className="row">
          <label>Harga baru <input type="number" value={harga} step={1000} onChange={(e) => setHarga(Number(e.target.value))} /></label>
          <button className="primary" disabled={busy} onClick={() => run(async () => { setScen(await post<Scen>("/api/repl/scenario", { harga })); setHarga(harga + 1000); })}>
            {busy ? "berjalan..." : "Ani menulis, lalu Ani dan Budi membaca"}
          </button>
        </div>
        {scen && (
          <>
            <p>Harga {rupiah(scen.harga_lama)} → <b>{rupiah(scen.harga_baru)}</b>. Commit di primary {scen.write_ms.toFixed(1)} ms, LSN tulisan <code>{scen.write_lsn}</code>.</p>
            <div className="tablewrap">
              <table>
                <thead><tr><th>Model</th><th>Ani membaca</th><th>Budi membaca</th></tr></thead>
                <tbody>
                  {["strong", "read-your-writes", "eventual"].map((m) => {
                    const cell = (who: string) => {
                      const r = scen.reads.find((x) => x.model === m && x.who === who)!;
                      return (
                        <td>
                          <b className={r.fresh ? "ok" : "bad"}>{rupiah(r.harga)}</b> {r.fresh ? "(baru)" : "(usang)"}
                          <div className="muted">dari {r.source}, {r.ms.toFixed(1)} ms</div>
                          {r.note && <div className="muted">{r.note}</div>}
                        </td>
                      );
                    };
                    return <tr key={m}><td><b>{m}</b></td>{cell("Ani")}{cell("Budi")}</tr>;
                  })}
                </tbody>
              </table>
            </div>
            <p className="note">
              {scen.reads.some((r) => r.model === "eventual" && r.who === "Ani" && !r.fresh)
                ? "Anomali read-your-writes: Ani membaca dari replica dan tidak melihat tulisannya sendiri. Read-your-writes memperbaikinya dengan mengingat LSN tulisan dan membaca dari primary selama replica belum sampai di LSN itu."
                : "Semua bacaan segar: replica sudah menerapkan tulisan sebelum dibaca (apply delay 0 atau mode synchronous). Naikkan apply delay ke 3000 ms dan mode asynchronous untuk melihat anomalinya."}
            </p>
            <h3>Konvergensi replica (eventual consistency)</h3>
            <p className="muted">Replica dibaca setiap 100 ms sampai nilainya sama dengan primary{scen.converged_ms >= 0 ? `: konvergen setelah ${scen.converged_ms} ms` : ": belum konvergen dalam 12 detik"}.</p>
            <LineChart label="harga di replica" points={scen.convergence.map((c) => ({ x: c.t_ms, y: c.harga / 1000 }))} unit="rb" height={120} />
          </>
        )}
      </div>
    </>
  );
}
