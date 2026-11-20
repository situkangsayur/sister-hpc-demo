"use client";
import { useEffect, useState } from "react";
import { fmt, postJSON, probe } from "./lib";
import { Legend, Timing, Waterfall } from "./Waterfall";

type Conn = { id: number; tls_version: string; alpn: string };
type Load = { proto: string; assets: number; conns: number; total_ms: number; alt_svc?: string; requests: Timing[]; connections: Conn[] };
type ProtoResult = {
  proto: string;
  median_ms: number;
  totals_ms: number[];
  model_rtt: number;
  model_ms: number;
  conns: number;
  tls_version: string;
  alpn: string;
  sample: Load;
};
type Netem = { dev: string; delay_ms: number; loss_pct: number; err?: string };
type Compare = { assets: number; think_ms: number; runs: number; netem: Netem; results: ProtoResult[] };

const NAMA: Record<string, string> = { h1: "HTTP/1.1 (TCP+TLS, maks. 6 koneksi)", h2: "HTTP/2 (TCP+TLS, 1 koneksi)", h3: "HTTP/3 (QUIC)" };
const WARNA: Record<string, string> = { h1: "var(--merah)", h2: "var(--c-wait)", h3: "var(--hijau)" };

export default function Page() {
  const [n, setN] = useState(30);
  const [think, setThink] = useState(0);
  const [runs, setRuns] = useState(5);
  const [delay, setDelay] = useState(25);
  const [loss, setLoss] = useState(0);
  const [netem, setNetem] = useState<Netem | null>(null);
  const [data, setData] = useState<Compare | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => {
    probe<Netem>("netem")
      .then((v) => {
        setNetem(v);
        setDelay(v.delay_ms);
        setLoss(v.loss_pct);
      })
      .catch((e) => setErr(String(e)));
  }, []);

  async function applyNetem() {
    setErr("");
    try {
      setNetem(await probe<Netem>("netem", postJSON({ delay_ms: delay, loss_pct: loss })));
    } catch (e) {
      setErr(String(e));
    }
  }

  async function run() {
    setBusy(true);
    setErr("");
    try {
      setData(await probe<Compare>(`compare?n=${n}&think=${think}&runs=${runs}`));
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  }

  const maxMs = data ? Math.max(...data.results.map((r) => r.sample.total_ms)) * 1.03 : 1;
  const maxMedian = data ? Math.max(...data.results.map((r) => r.median_ms)) : 1;

  return (
    <>
      <h1>Mengukur: ke mana waktu habis? (h1 vs h2 vs h3)</h1>
      <p className="muted">
        Bab 3 subbab &quot;Menghitung cost HTTP/1.1&quot; dan &quot;Mengukur: Ke Mana Waktu Habis?&quot;. Probe meniru browser: ambil HTML, lalu
        semua aset kecil secara paralel, dengan klien baru (koneksi dingin) setiap run. Waktu tiap tahap dicatat dengan{" "}
        <code>net/http/httptrace</code>.
      </p>

      <div className="card">
        <h2>Jaringan buatan (tc netem di probe)</h2>
        <p className="muted">
          RTT antar container hampir nol, sehingga perbedaan versi HTTP tidak terlihat. Delay ditambahkan di arah keluar probe, jadi RTT ≈ delay.
        </p>
        <div className="row">
          <label>
            Delay / RTT (ms)
            <input type="number" min={0} max={500} value={delay} onChange={(e) => setDelay(+e.target.value)} />
          </label>
          <label>
            Packet loss (%)
            <input type="number" min={0} max={30} step={0.5} value={loss} onChange={(e) => setLoss(+e.target.value)} />
          </label>
          <button className="alt" onClick={applyNetem}>
            Terapkan
          </button>
          <span className="muted">
            Aktif: {netem ? `${netem.delay_ms} ms, loss ${netem.loss_pct}% (interface ${netem.dev || "?"})` : "memuat..."}
            {netem?.err && <span className="bad"> · {netem.err}</span>}
          </span>
        </div>
      </div>

      <div className="card">
        <h2>Halaman uji</h2>
        <div className="row">
          <label>
            Jumlah aset
            <input type="number" min={1} max={120} value={n} onChange={(e) => setN(+e.target.value)} />
          </label>
          <label>
            Server &quot;berpikir&quot; (ms)
            <input type="number" min={0} max={3000} value={think} onChange={(e) => setThink(+e.target.value)} />
          </label>
          <label>
            Run per versi
            <input type="number" min={1} max={15} value={runs} onChange={(e) => setRuns(+e.target.value)} />
          </label>
          <button onClick={run} disabled={busy}>
            {busy ? "Mengukur..." : "Ukur h1, h2, h3"}
          </button>
        </div>
        {err && <div className="err">{err}</div>}
      </div>

      {data && (
        <>
          <div className="card">
            <h2>
              Ringkasan: median dari {data.runs} run, {data.assets} aset, RTT buatan {data.netem.delay_ms} ms
              {data.netem.loss_pct > 0 ? `, loss ${data.netem.loss_pct}%` : ""}
            </h2>
            <table>
              <thead>
                <tr>
                  <th>Versi</th>
                  <th>Median total</th>
                  <th style={{ width: "30%" }} />
                  <th>Semua run (ms)</th>
                  <th>Koneksi</th>
                  <th>TLS / ALPN</th>
                  <th>Model buku</th>
                </tr>
              </thead>
              <tbody>
                {data.results.map((r) => (
                  <tr key={r.proto}>
                    <td>
                      <strong>{r.proto}</strong> <span className="muted">{NAMA[r.proto]}</span>
                    </td>
                    <td>
                      <strong>{fmt(r.median_ms)} ms</strong>
                    </td>
                    <td>
                      <div className="bar" style={{ width: `${(100 * r.median_ms) / maxMedian}%`, background: WARNA[r.proto] }} />
                    </td>
                    <td className="mono">{r.totals_ms.map((x) => x.toFixed(0)).join(" · ")}</td>
                    <td>{r.conns}</td>
                    <td>
                      {r.tls_version} / <code>{r.alpn || "-"}</code>
                    </td>
                    <td>
                      {r.model_rtt} RTT ≈ {fmt(r.model_ms, 0)} ms
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <p className="muted">
              Model buku (server menjawab seketika, file kecil): h1 = 3 RTT (TCP+TLS+HTML) + 2 RTT (5 koneksi tambahan) + ⌈n/6⌉ putaran; h2 = 3 + 1;
              h3 = 2 + 1. Selisih dengan angka terukur berasal dari waktu server, slow start, dan antrean di container.
            </p>
            {data.results[0]?.sample.alt_svc && (
              <p className="muted">
                Header <code>Alt-Svc: {data.results[0].sample.alt_svc}</code> pada respons h1/h2: cara server mengiklankan bahwa HTTP/3 tersedia.
              </p>
            )}
          </div>

          <div className="card">
            <h2>Waterfall (run yang paling dekat dengan median; skala waktu sama untuk ketiganya)</h2>
            <Legend />
            {data.results.map((r) => (
              <div key={r.proto} style={{ marginBottom: 18 }}>
                <h3>
                  {NAMA[r.proto]} · total {fmt(r.sample.total_ms)} ms · {r.conns} koneksi
                </h3>
                <Waterfall requests={r.sample.requests} maxMs={maxMs} isH3={r.proto === "h3"} />
              </div>
            ))}
            <p className="muted">
              Arahkan kursor ke satu baris untuk melihat angka tiap tahap. Label <code>cN</code> adalah nomor koneksi. Pada h1 terlihat paling banyak 6
              koneksi dan aset yang mengantre (abu-abu); pada h2 dan h3 semua aset berangkat bersamaan di satu koneksi.
            </p>
          </div>
        </>
      )}
    </>
  );
}
