"use client";

import { useState } from "react";
import { fmtMs, postJSON } from "./lib";

type Trial = { got: number; lost: number; duration_ms: number };
type ModeResult = { mode: string; expected: number; trials: Trial[]; all_correct: boolean; median_ms: number };
type RaceResp = { goroutines: number; iters: number; gomaxprocs: number; results: ModeResult[] };
type DetectorResp = { mode: string; command: string; exit_code: number; race_reports: number; output: string };

const MODE_INFO: Record<string, string> = {
  unsafe: "n++ tanpa sinkronisasi (data race)",
  mutex: "sync.Mutex menjaga critical section",
  atomic: "sync/atomic: Add(1) atomik",
  channel: "CSP: satu goroutine pemilik saldo",
  actor: "Actor: mailbox + state privat",
};

export default function RacePanel() {
  const [g, setG] = useState(1000);
  const [n, setN] = useState(1000);
  const [trials, setTrials] = useState(5);
  const [res, setRes] = useState<RaceResp | null>(null);
  const [det, setDet] = useState<DetectorResp | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  async function run() {
    setBusy(true); setErr("");
    try { setRes(await postJSON<RaceResp>("race", { goroutines: g, iters: n, trials })); }
    catch (e) { setErr(String(e)); }
    setBusy(false);
  }
  async function detect(mode: string) {
    setBusy(true); setErr("");
    try { setDet(await postJSON<DetectorResp>("race/detector", { mode })); }
    catch (e) { setErr(String(e)); }
    setBusy(false);
  }
  const maxMs = res ? Math.max(...res.results.map((r) => r.median_ms)) : 1;

  return (
    <div className="panel">
      <div className="card">
        <h2>1. Race condition: satu saldo, lima model</h2>
        <p className="theory">
          <code>n++</code> adalah tiga langkah (baca, tambah, tulis). Bila dua goroutine menjalankannya bersamaan tanpa
          sinkronisasi, satu penambahan bisa hilang (<em>lost update</em>). Hasil yang benar wajib tepat
          G × N. Bab 6 subbab &ldquo;Thread dan Lock: Race Condition&rdquo; dan studi kasus &ldquo;Satu Soal, Tiga Model&rdquo;.
        </p>
        <div className="controls">
          <label>Goroutine (G)<input type="number" min={1} max={10000} value={g} onChange={(e) => setG(+e.target.value)} /></label>
          <label>Tambah per goroutine (N)<input type="number" min={1} max={100000} value={n} onChange={(e) => setN(+e.target.value)} /></label>
          <label>Percobaan<input type="number" min={1} max={10} value={trials} onChange={(e) => setTrials(+e.target.value)} /></label>
          <button className="btn" onClick={run} disabled={busy}>{busy ? "Menjalankan…" : "Jalankan semua model"}</button>
        </div>
        {err && <p className="badge bad">{err}</p>}
        {res && (
          <div className="scroll-x">
            <p className="muted">Seharusnya {res.results[0]?.expected.toLocaleString("id-ID")} · GOMAXPROCS = {res.gomaxprocs}</p>
            <table className="data">
              <thead>
                <tr><th>Model</th><th>Cara kerja</th><th>Hasil tiap percobaan</th><th>Benar?</th><th>Median waktu</th><th style={{ width: "22%" }}></th></tr>
              </thead>
              <tbody>
                {res.results.map((r) => (
                  <tr key={r.mode}>
                    <td><code>{r.mode}</code></td>
                    <td>{MODE_INFO[r.mode]}</td>
                    <td className="num" style={{ textAlign: "left" }}>
                      {r.trials.map((t, i) => (
                        <div key={i}>
                          {t.got.toLocaleString("id-ID")}
                          {t.lost > 0 && <span style={{ color: "var(--bad)" }}> (hilang {t.lost.toLocaleString("id-ID")})</span>}
                        </div>
                      ))}
                    </td>
                    <td>{r.all_correct ? <span className="badge good">✓ tepat</span> : <span className="badge bad">✗ salah</span>}</td>
                    <td className="num">{fmtMs(r.median_ms)}</td>
                    <td>
                      <div className="bar" style={{ gridTemplateColumns: "1fr" }}>
                        <div className="track"><div className="fill" style={{ width: `${(r.median_ms / maxMs) * 100}%` }} /></div>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <p className="theory" style={{ marginTop: 8 }}>
              <code>unsafe</code> paling cepat karena tidak menunggu apa pun, tetapi salah. <code>atomic</code> membayar CAS/XADD
              yang direbutkan; <code>mutex</code> membayar antrean lock; <code>channel</code> dan <code>actor</code> membayar
              sinkronisasi queue per pesan. Operasi sekecil &ldquo;tambah satu&rdquo; terlalu halus untuk channel dan actor.
            </p>
          </div>
        )}
      </div>

      <div className="card">
        <h3>Race detector (<code>go build -race</code>)</h3>
        <p className="theory">
          Uji yang lulus seratus kali hanya berarti interleaving buruk belum terjadi. Detektor race menandai akses memori
          bersamaan yang salah satunya menulis, walaupun hasil kebetulan benar. Biner <code>racedemo</code> di container
          dibangun dengan <code>-race</code>.
        </p>
        <div className="row">
          {["unsafe", "mutex", "atomic", "channel"].map((m) => (
            <button key={m} className="btn ghost" disabled={busy} onClick={() => detect(m)}>Jalankan {m} dengan -race</button>
          ))}
        </div>
        {det && (
          <>
            <p className="row" style={{ marginTop: 8 }}>
              {det.race_reports > 0
                ? <span className="badge bad">✗ {det.race_reports} laporan DATA RACE · exit {det.exit_code}</span>
                : <span className="badge good">✓ bersih menurut detektor · exit {det.exit_code}</span>}
              <span className="muted">{det.command}</span>
            </p>
            <pre className="term">{det.output}</pre>
          </>
        )}
      </div>
    </div>
  );
}
