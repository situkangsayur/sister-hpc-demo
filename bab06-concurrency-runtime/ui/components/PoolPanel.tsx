"use client";

import { useState } from "react";
import LineChart from "./LineChart";
import { COLORS, postJSON } from "./lib";

type Point = { workers: number; elapsed_ms: number; throughput: number; lat_p50_ms: number; lat_p95_ms: number; service_avg_ms: number };
type Resp = { kind: string; jobs: number; job_ms: number; num_cpu: number; series: { gomaxprocs: number; points: Point[] }[] };

export default function PoolPanel() {
  const [kind, setKind] = useState("cpu");
  const [jobs, setJobs] = useState(200);
  const [jobMs, setJobMs] = useState(2);
  const [res, setRes] = useState<Resp | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  async function run() {
    setBusy(true); setErr("");
    try { setRes(await postJSON<Resp>("pool/sweep", { kind, jobs, job_ms: jobMs })); }
    catch (e) { setErr(String(e)); }
    setBusy(false);
  }
  const series = (f: (p: Point) => number) =>
    (res?.series ?? []).map((s, i) => ({
      name: `GOMAXPROCS=${s.gomaxprocs}`, color: COLORS[i], points: s.points.map((p) => [p.workers, f(p)] as [number, number]),
    }));

  return (
    <div className="panel">
      <div className="card">
        <h2>3. Worker pool: berapa worker yang pas?</h2>
        <p className="theory">
          Worker pool membatasi jumlah pekerja serentak. Semua job masuk antrean pada saat yang sama, lalu diambil worker.
          Untuk job <b>CPU-bound</b>, throughput naik hanya sampai jumlah worker = <code>GOMAXPROCS</code> (jumlah P yang boleh
          menjalankan kode Go); sesudahnya worker cuma bergantian di core yang sama. Untuk job <b>I/O-bound</b>
          (menunggu jaringan/disk, di sini <code>time.Sleep</code>), goroutine yang menunggu diparkir dan P bebas, sehingga
          lebih banyak worker tetap menambah throughput. Bab 6 subbab &ldquo;Thread Pool dan Virtual Thread&rdquo; dan &ldquo;Worker pool&rdquo;.
        </p>
        <div className="controls">
          <label>Jenis job
            <select value={kind} onChange={(e) => { setKind(e.target.value); setJobMs(e.target.value === "cpu" ? 2 : 10); }}>
              <option value="cpu">CPU-bound</option>
              <option value="io">I/O-bound</option>
            </select>
          </label>
          <label>Jumlah job<input type="number" min={10} max={2000} value={jobs} onChange={(e) => setJobs(+e.target.value)} /></label>
          <label>Lama per job (ms)<input type="number" min={0.1} max={50} step={0.5} value={jobMs} onChange={(e) => setJobMs(+e.target.value)} /></label>
          <button className="btn" onClick={run} disabled={busy}>{busy ? "Mengukur… (beberapa detik)" : "Jalankan sweep"}</button>
        </div>
        {err && <p className="badge bad">{err}</p>}
        {res && <p className="muted">Container melihat {res.num_cpu} CPU. Worker: 1, 2, 4, … (sumbu x logaritmik).</p>}
      </div>
      {res && (
        <div className="grid2">
          <div className="card">
            <h3>Throughput (job/detik)</h3>
            <LineChart ariaLabel="Throughput terhadap jumlah worker" series={series((p) => p.throughput)}
              xLabel="jumlah worker" yLabel="job/detik" logX markers xFmt={(v) => String(Math.round(v))} />
          </div>
          <div className="card">
            <h3>Latency p95 job (ms, termasuk antre)</h3>
            <LineChart ariaLabel="Latency p95 terhadap jumlah worker" series={series((p) => p.lat_p95_ms)}
              xLabel="jumlah worker" yLabel="ms" logX markers xFmt={(v) => String(Math.round(v))} />
          </div>
          <div className="card">
            <h3>Waktu layanan rata-rata per job (ms)</h3>
            <p className="theory">Untuk CPU-bound, worker melebihi GOMAXPROCS membuat tiap job lebih lama (disela dan antre P).</p>
            <LineChart ariaLabel="Waktu layanan per job" series={series((p) => p.service_avg_ms)}
              xLabel="jumlah worker" yLabel="ms" logX markers xFmt={(v) => String(Math.round(v))} />
          </div>
          <div className="card scroll-x">
            <h3>Tabel</h3>
            <table className="data">
              <thead><tr><th>GOMAXPROCS</th><th>Worker</th><th>Throughput</th><th>p50</th><th>p95</th><th>Total</th></tr></thead>
              <tbody>
                {res.series.flatMap((s) => s.points.map((p) => (
                  <tr key={`${s.gomaxprocs}-${p.workers}`}>
                    <td className="num">{s.gomaxprocs}</td><td className="num">{p.workers}</td>
                    <td className="num">{p.throughput.toFixed(0)}/s</td>
                    <td className="num">{p.lat_p50_ms.toFixed(1)}</td><td className="num">{p.lat_p95_ms.toFixed(1)}</td>
                    <td className="num">{p.elapsed_ms.toFixed(0)} ms</td>
                  </tr>
                )))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
