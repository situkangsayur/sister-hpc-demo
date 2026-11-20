"use client";

import { useState } from "react";
import LineChart from "./LineChart";
import { fmtBytes, postJSON } from "./lib";

export type Metrics = {
  t_ms: number; goroutines: number; gomaxprocs: number; num_cpu: number; heap_alloc_mb: number; heap_inuse_mb: number;
  stack_inuse_mb: number; sys_mb: number; num_gc: number; last_pause_us: number; pause_total_ms: number;
  gc_cpu_fraction: number; new_pauses_us?: number[]; rss_kb: number; vsize_kb: number; threads: number;
};

type Spawn = {
  kind: string; n: number; spawn_ms: number; spawn_us_per_unit: number; stack_bytes_per_unit: number;
  rss_bytes_per_unit: number; vsize_bytes_per_unit: number; threads_before: number; threads_after: number;
  rss_mb_before: number; rss_mb_after: number;
};
type Churn = { allocated_mb: number; gc_count: number; pause_p50_us: number; pause_p99_us: number; pause_max_us: number; pause_total_ms: number };

export default function RuntimePanel({ hist }: { hist: Metrics[] }) {
  const [gN, setGN] = useState(100000);
  const [tN, setTN] = useState(2000);
  const [res, setRes] = useState<Record<string, Spawn>>({});
  const [churn, setChurn] = useState<Churn | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  async function act<T>(f: () => Promise<T>) {
    setBusy(true); setErr("");
    try { await f(); } catch (e) { setErr(String(e)); }
    setBusy(false);
  }
  const spawn = (kind: string, n: number) => act(async () => {
    const r = await postJSON<Spawn>("spawn", { kind, n });
    setRes((o) => ({ ...o, [kind]: r }));
  });
  const t0 = hist[0]?.t_ms ?? 0;
  const rel = (f: (m: Metrics) => number) => hist.map((m) => [(m.t_ms - t0) / 1000, f(m)] as [number, number]);
  const pauses = hist.flatMap((m) => (m.new_pauses_us ?? []).map((p) => [(m.t_ms - t0) / 1000, p] as [number, number]));

  return (
    <div className="panel">
      <div className="card">
        <h2>5. Biaya goroutine vs thread OS, dan metrik runtime</h2>
        <p className="theory">
          Runtime Go menjadwalkan banyak goroutine (G) di atas sedikit thread OS (M) lewat P sejumlah <code>GOMAXPROCS</code>
          (scheduling M:N, model G-M-P). Stack awal goroutine hanya beberapa KB dan tumbuh bila perlu. Untuk pembanding
          thread OS, setiap goroutine di mode &ldquo;thread&rdquo; memanggil <code>runtime.LockOSThread()</code> lalu parkir,
          sehingga runtime terpaksa membuat satu thread OS per goroutine. Bab 6 subbab &ldquo;CSP di Go&rdquo; dan &ldquo;Go:
          scheduler M:N dengan model G-M-P&rdquo;.
        </p>
        <div className="controls">
          <label>Goroutine<input type="number" min={1} max={500000} step={10000} value={gN} onChange={(e) => setGN(+e.target.value)} /></label>
          <button className="btn" disabled={busy} onClick={() => spawn("goroutine", gN)}>Buat goroutine parkir</button>
          <label>Thread OS<input type="number" min={1} max={5000} step={500} value={tN} onChange={(e) => setTN(+e.target.value)} /></label>
          <button className="btn" disabled={busy} onClick={() => spawn("thread", tN)}>Buat thread OS</button>
          <button className="btn ghost" disabled={busy} onClick={() => act(() => postJSON("spawn/release"))}>Lepaskan semua</button>
        </div>
        {err && <p className="badge bad">{err}</p>}
        <div className="scroll-x">
          <table className="data">
            <thead>
              <tr><th></th><th>Jumlah</th><th>Waktu buat / unit</th><th>Stack / unit</th><th>RSS / unit</th><th>Virtual / unit</th><th>Thread OS proses</th></tr>
            </thead>
            <tbody>
              {(["goroutine", "thread"] as const).map((k) => {
                const r = res[k];
                return (
                  <tr key={k}>
                    <th>{k === "goroutine" ? "Goroutine" : "Thread OS (LockOSThread)"}</th>
                    {r ? (
                      <>
                        <td className="num">{r.n.toLocaleString("id-ID")}</td>
                        <td className="num">{r.spawn_us_per_unit.toFixed(2)} µs</td>
                        <td className="num">{fmtBytes(r.stack_bytes_per_unit)}</td>
                        <td className="num">{fmtBytes(r.rss_bytes_per_unit)}</td>
                        <td className="num">{fmtBytes(r.vsize_bytes_per_unit)}</td>
                        <td className="num">{r.threads_before} → {r.threads_after}</td>
                      </>
                    ) : <td colSpan={6} className="muted">belum diukur</td>}
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        <p className="muted">
          RSS = memori fisik yang benar-benar terpakai; virtual = tambahan ruang alamat. Biner Go tanpa cgo mengalokasikan
          stack thread (g0) dari memori runtime sendiri, sehingga virtual bisa nol; thread pthread di C atau Java memesan
          stack 1–8 MB per thread. Yang terlihat jelas: setiap thread OS butuh struktur kernel, stack g0, dan waktu buat
          yang berlipat dibanding goroutine. Hasil tetap tersimpan sampai &ldquo;Lepaskan semua&rdquo;.
        </p>
      </div>

      <div className="card">
        <h3>Garbage collector</h3>
        <p className="theory">
          Buat sampah dengan laju 300 MB/s (64 MB tetap hidup) selama 4 detik. GC Go bersifat concurrent: jeda
          stop-the-world hanya puluhan sampai ratusan mikrodetik, tetapi tetap ada dan memakan CPU.
        </p>
        <button className="btn" disabled={busy} onClick={() => act(async () => setChurn(await postJSON<Churn>("gc/churn", { seconds: 4, mb_per_s: 300, live_mb: 64 })))}>
          {busy ? "Berjalan…" : "Jalankan beban GC"}
        </button>
        {churn && (
          <p className="row" style={{ marginTop: 8 }}>
            <span>Dialokasikan <b>{churn.allocated_mb.toFixed(0)} MB</b></span>
            <span>GC <b>{churn.gc_count}×</b></span>
            <span>jeda p50 <b>{churn.pause_p50_us.toFixed(0)} µs</b></span>
            <span>p99 <b>{churn.pause_p99_us.toFixed(0)} µs</b></span>
            <span>maks <b>{churn.pause_max_us.toFixed(0)} µs</b></span>
          </p>
        )}
      </div>

      {hist.length > 1 && (
        <div className="grid2">
          <div className="card">
            <h3>Jumlah goroutine</h3>
            <LineChart ariaLabel="Jumlah goroutine" xLabel="detik" series={[{ name: "goroutine", color: "var(--s1)", points: rel((m) => m.goroutines) }]} height={180} />
          </div>
          <div className="card">
            <h3>Jumlah thread OS proses lab</h3>
            <LineChart ariaLabel="Jumlah thread OS" xLabel="detik" series={[{ name: "thread", color: "var(--s2)", points: rel((m) => m.threads) }]} height={180} />
          </div>
          <div className="card">
            <h3>Memori (MB)</h3>
            <LineChart ariaLabel="Memori heap, stack, dan RSS" xLabel="detik" yLabel="MB"
              series={[
                { name: "heap alloc", color: "var(--s1)", points: rel((m) => m.heap_alloc_mb) },
                { name: "stack inuse", color: "var(--s2)", points: rel((m) => m.stack_inuse_mb) },
                { name: "RSS", color: "var(--s3)", points: rel((m) => m.rss_kb / 1024) },
              ]} height={200} />
          </div>
          <div className="card">
            <h3>Jeda GC (µs, per siklus GC)</h3>
            {pauses.length ? (
              <LineChart ariaLabel="Jeda GC" xLabel="detik" yLabel="µs" markers
                series={[{ name: "jeda GC", color: "var(--s4)", points: pauses }]} height={200} />
            ) : <p className="muted">Belum ada GC sejak halaman dibuka. Jalankan beban GC.</p>}
          </div>
        </div>
      )}
    </div>
  );
}
