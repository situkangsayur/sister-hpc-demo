"use client";
// Head-of-line blocking: beberapa stream logis di satu koneksi TCP vs stream QUIC.
import { useEffect, useState } from "react";
import NetemPanel from "@/components/NetemPanel";
import Scatter, { Legend } from "@/components/Scatter";
import { api, fmt, HOLResult, STREAM_COLORS } from "@/lib/api";

type HOLResp = { netem_text: string; results: HOLResult[] };

export default function HOLPage() {
  const [streams, setStreams] = useState(3);
  const [messages, setMessages] = useState(60);
  const [interval, setIntervalMs] = useState(30);
  const [res, setRes] = useState<HOLResp | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  // ?auto=1 langsung menjalankan uji saat halaman dibuka (berguna di kelas dan untuk tangkapan layar)
  useEffect(() => {
    if (new URLSearchParams(window.location.search).get("auto") === "1") start();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function start() {
    setBusy(true);
    setErr("");
    try {
      setRes(await api<HOLResp>("hol", { streams, messages, interval_ms: interval }));
    } catch (e) {
      setErr(String((e as Error).message));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <section className="card">
        <h1>Head-of-line blocking: TCP vs QUIC</h1>
        <p className="muted">
          Beberapa stream logis (misalnya beberapa request HTTP/2) mengirim message terjadwal secara bersamaan. Pada
          TCP semuanya berbagi <b>satu byte stream</b>; satu segmen hilang menahan semua byte di belakangnya. Pada QUIC
          setiap stream punya urutan sendiri, sehingga packet hilang hanya menahan stream miliknya (Bab 2, subbab
          Head-of-line blocking pada TCP dan Empat gagasan utama QUIC).
        </p>
      </section>
      <NetemPanel />
      <section className="card">
        <h2>Jalankan uji HOL</h2>
        <div className="row">
          <label>
            stream <input type="number" min={2} max={6} value={streams} onChange={(e) => setStreams(Number(e.target.value))} />
          </label>
          <label>
            message per stream <input type="number" min={10} max={300} value={messages} onChange={(e) => setMessages(Number(e.target.value))} />
          </label>
          <label>
            jeda per stream (ms) <input type="number" min={5} max={500} value={interval} onChange={(e) => setIntervalMs(Number(e.target.value))} />
          </label>
          <button onClick={start} disabled={busy}>
            {busy ? "Berjalan..." : "Jalankan TCP dan QUIC"}
          </button>
          {res && <span className="muted">kondisi: <b>{res.netem_text}</b></span>}
        </div>
        <p className="muted">Saran: pasang loss 3–5% dan delay 30–50 ms, supaya lonjakan terlihat jelas.</p>
        {err && <p className="error">{err}</p>}
      </section>
      {res && (
        <div className="grid2">
          {res.results.map((r) => (
            <section className="card" key={r.transport}>
              <h2>{r.transport.toUpperCase()}</h2>
              <p className="muted">{r.description}</p>
              {r.error ? (
                <p className="error">{r.error}</p>
              ) : (
                <>
                  <Legend items={(r.per_stream ?? []).map((s) => ({ name: `stream ${s.stream}`, color: STREAM_COLORS[s.stream] }))} />
                  <Scatter
                    height={260}
                    xLabel="waktu kirim (ms)"
                    yLabel="latency message (ms)"
                    threshold={r.threshold_ms}
                    series={(r.per_stream ?? []).map((s) => ({
                      name: `stream ${s.stream}`,
                      color: STREAM_COLORS[s.stream],
                      points: (r.samples ?? [])
                        .filter((x) => x.stream === s.stream)
                        .map((x) => ({ x: x.t_ms, y: x.lat_ms, lost: x.lost, label: `stream ${x.stream} #${x.seq}: ${x.lat_ms} ms` })),
                    }))}
                  />
                  <table>
                    <tbody>
                      <tr>
                        <th>message tersendat</th>
                        <td className="num">{r.stalls}</td>
                      </tr>
                      <tr>
                        <th>tersendat bersamaan dengan stream lain</th>
                        <td className="num">
                          <b>{fmt(r.shared_pct)}%</b>
                        </td>
                      </tr>
                      {(r.per_stream ?? []).map((s) => (
                        <tr key={s.stream}>
                          <th>
                            <span className="dot" style={{ background: STREAM_COLORS[s.stream] }} />
                            stream {s.stream}
                          </th>
                          <td className="num">
                            median {fmt(s.stats.median, 1)} ms · maks {fmt(s.stats.max, 1)} ms
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </>
              )}
            </section>
          ))}
        </div>
      )}
      <section className="card theory">
        <b>Cara membaca:</b> pada TCP, lonjakan latency muncul sebagai “tiang” vertikal berisi titik dari semua warna
        sekaligus, karena message stream lain yang sudah tiba ikut tertahan di buffer penerima menunggu segmen yang
        hilang. Pada QUIC, lonjakan biasanya hanya satu warna. Angka “tersendat bersamaan” merangkum hal ini: tinggi
        pada TCP, rendah pada QUIC.
      </section>
    </>
  );
}
