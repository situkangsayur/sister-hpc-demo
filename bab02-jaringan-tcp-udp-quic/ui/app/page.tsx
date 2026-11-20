"use client";
// Halaman perbandingan TCP, UDP, QUIC: RTT per message, loss, throughput, handshake.
import { useCallback, useEffect, useRef, useState } from "react";
import NetemPanel from "@/components/NetemPanel";
import Scatter, { Legend } from "@/components/Scatter";
import { api, COLORS, fmt, Handshake, Run, RunSummary } from "@/lib/api";

const LABEL: Record<string, string> = { tcp: "TCP", udp: "UDP", quic: "QUIC" };

export default function TransportPage() {
  const [run, setRun] = useState<Run | null>(null);
  const [history, setHistory] = useState<RunSummary[]>([]);
  const [count, setCount] = useState(100);
  const [interval, setIntervalMs] = useState(20);
  const [bulk, setBulk] = useState(1024);
  const [err, setErr] = useState("");
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const poll = useCallback(async () => {
    try {
      const r = await api<Run>("run");
      setRun(r);
      if (r.status === "running") {
        timer.current = setTimeout(poll, 500);
      } else {
        setHistory(await api<RunSummary[]>("history"));
      }
    } catch (e) {
      setErr(String((e as Error).message));
    }
  }, []);

  useEffect(() => {
    poll();
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
  }, [poll]);

  async function start() {
    setErr("");
    try {
      await api<Run>("run", { count, interval_ms: interval, bulk_kb: bulk });
      poll();
    } catch (e) {
      setErr(String((e as Error).message));
    }
  }

  const running = run?.status === "running";
  const protos = run?.protocols ?? [];

  return (
    <>
      <section className="card">
        <h1>Perbandingan transport: TCP, UDP, QUIC</h1>
        <p className="muted">
          Prober mengirim message ke server echo melewati router yang memasang <code>tc netem</code>. Ini adalah
          eksperimen “TCP vs UDP saat jaringan buruk” dari Bab 2 (subbab Mengamati Packet) dan praktikum slide P02,
          ditambah QUIC.
        </p>
      </section>

      <NetemPanel />

      <section className="card">
        <h2>Jalankan uji</h2>
        <div className="row">
          <label>
            message per protokol <input type="number" min={10} max={500} value={count} onChange={(e) => setCount(Number(e.target.value))} />
          </label>
          <label>
            jeda (ms) <input type="number" min={0} max={1000} value={interval} onChange={(e) => setIntervalMs(Number(e.target.value))} />
          </label>
          <label>
            data throughput (KB) <input type="number" min={0} max={8192} value={bulk} onChange={(e) => setBulk(Number(e.target.value))} />
          </label>
          <button onClick={start} disabled={running}>
            {running ? `Berjalan: fase ${run?.phase}...` : "Jalankan uji"}
          </button>
          {run && run.id > 0 && (
            <span className="muted">
              putaran #{run.id} · kondisi: <b>{run.netem_text}</b>
            </span>
          )}
        </div>
        {err && <p className="error">{err}</p>}
      </section>

      {protos.length > 0 && (
        <section className="card">
          <h2>RTT per message</h2>
          <Legend items={protos.map((p) => ({ name: LABEL[p.proto], color: COLORS[p.proto] }))} />
          <Scatter
            xLabel="nomor message"
            yLabel="RTT (ms)"
            series={protos.map((p) => ({
              name: LABEL[p.proto],
              color: COLORS[p.proto],
              line: true,
              points: p.samples.map((s) => ({
                x: s.seq,
                y: s.rtt_ms,
                lost: s.lost,
                label: s.lost ? `${LABEL[p.proto]} #${s.seq}: hilang` : `${LABEL[p.proto]} #${s.seq}: ${s.rtt_ms} ms`,
              })),
            }))}
          />
          <div className="theory">
            <b>Yang diamati:</b> saat ada loss, TCP dan QUIC tetap mengembalikan semua balasan, tetapi sebagian titik
            melonjak (menunggu retransmission). UDP tidak pernah tersendat, tetapi sebagian message hilang (tanda ×
            di atas grafik) dan tidak dikirim ulang.
          </div>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Protokol</th>
                  <th className="num">terkirim</th>
                  <th className="num">kembali</th>
                  <th className="num">hilang</th>
                  <th className="num">min</th>
                  <th className="num">median</th>
                  <th className="num">p95</th>
                  <th className="num">maks</th>
                  <th className="num">throughput</th>
                  <th>catatan</th>
                </tr>
              </thead>
              <tbody>
                {protos.map((p) => (
                  <tr key={p.proto}>
                    <td>
                      <span className="dot" style={{ background: COLORS[p.proto] }} />
                      {LABEL[p.proto]} <span className="muted">({p.status})</span>
                    </td>
                    <td className="num">{p.sent}</td>
                    <td className="num">{p.status === "done" ? p.received : p.samples.filter((s) => !s.lost).length}</td>
                    <td className="num">{p.status === "done" ? `${fmt(p.loss_pct)}%` : "-"}</td>
                    <td className="num">{fmt(p.stats.min, 2)} ms</td>
                    <td className="num">{fmt(p.stats.median, 2)} ms</td>
                    <td className="num">{fmt(p.stats.p95, 2)} ms</td>
                    <td className="num">{fmt(p.stats.max, 2)} ms</td>
                    <td className="num">
                      {p.throughput ? (p.throughput.error ? "gagal" : `${fmt(p.throughput.mbps, 2)} Mbit/s`) : "-"}
                    </td>
                    <td className="muted">
                      {p.error ?? ""}
                      {p.throughput &&
                        `${p.throughput.note ?? ""}${p.throughput.loss_pct ? ` · hilang ${fmt(p.throughput.loss_pct)}%` : ""}${
                          p.throughput.error ? ` · ${p.throughput.error}` : ""
                        }`}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}

      {run?.handshakes && run.handshakes.length > 0 && <Handshakes hs={run.handshakes} base={protos.find((p) => p.proto === "udp")?.stats.median} />}

      <section className="card">
        <h2>Catatan hasil (templat Tabel praktikum Bab 2)</h2>
        <p className="muted">Setiap putaran yang selesai masuk ke tabel ini. Jalankan sekali dalam kondisi normal, sekali dengan preset praktikum.</p>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>#</th>
                <th>Kondisi</th>
                <th className="num">TCP median</th>
                <th className="num">TCP maks</th>
                <th className="num">UDP median</th>
                <th className="num">UDP hilang</th>
                <th className="num">QUIC median</th>
                <th className="num">QUIC maks</th>
                <th className="num">TCP / UDP / QUIC Mbit/s</th>
              </tr>
            </thead>
            <tbody>
              {history.map((h) => (
                <tr key={h.id}>
                  <td>{h.id}</td>
                  <td>{h.netem_text}</td>
                  <td className="num">{fmt(h.protos.tcp?.median, 2)}</td>
                  <td className="num">{fmt(h.protos.tcp?.max, 2)}</td>
                  <td className="num">{fmt(h.protos.udp?.median, 2)}</td>
                  <td className="num">{fmt(h.protos.udp?.loss_pct)}%</td>
                  <td className="num">{fmt(h.protos.quic?.median, 2)}</td>
                  <td className="num">{fmt(h.protos.quic?.max, 2)}</td>
                  <td className="num">
                    {fmt(h.protos.tcp?.mbps)} / {fmt(h.protos.udp?.mbps)} / {fmt(h.protos.quic?.mbps)}
                  </td>
                </tr>
              ))}
              {history.length === 0 && (
                <tr>
                  <td colSpan={9} className="muted">
                    belum ada putaran yang selesai
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>
    </>
  );
}

function Handshakes({ hs, base }: { hs: Handshake[]; base?: number }) {
  const max = Math.max(1, ...hs.map((h) => h.first_reply_ms));
  const rtt = base && base > 0 ? base : undefined;
  return (
    <section className="card">
      <h2>Handshake: kapan data pertama boleh dikirim?</h2>
      <p className="muted">
        Median dari tiga percobaan. Batang gelap = waktu sampai handshake selesai (data aplikasi boleh dikirim);
        batang terang = sampai balasan pertama tiba. {rtt ? `Satu RTT ≈ ${fmt(rtt, 1)} ms (median ping UDP).` : ""}
      </p>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Transport</th>
              <th>teori (Gambar RTT di Bab 2)</th>
              <th style={{ width: "38%" }}>waktu</th>
              <th className="num">handshake</th>
              <th className="num">balasan pertama</th>
            </tr>
          </thead>
          <tbody>
            {hs.map((h) => (
              <tr key={h.name}>
                <td>
                  {h.name}
                  {h.note && <div className="muted">{h.note}</div>}
                  {h.error && <div className="error">{h.error}</div>}
                </td>
                <td className="muted">{h.theory_rtt}</td>
                <td>
                  <div style={{ position: "relative", height: 18 }}>
                    <div className="bar" style={{ position: "absolute", top: 2, width: `${(h.first_reply_ms / max) * 100}%`, background: "#99f6e4" }} />
                    <div className="bar" style={{ position: "absolute", top: 2, width: `${(h.handshake_ms / max) * 100}%`, background: "#0f766e" }} />
                  </div>
                </td>
                <td className="num">
                  {fmt(h.handshake_ms, 1)} ms{rtt ? <div className="muted">≈ {fmt(h.handshake_ms / rtt, 1)} RTT</div> : null}
                </td>
                <td className="num">
                  {fmt(h.first_reply_ms, 1)} ms{rtt ? <div className="muted">≈ {fmt(h.first_reply_ms / rtt, 1)} RTT</div> : null}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="theory">
        Pada koneksi ulang, QUIC 0-RTT mengirim data di packet pertama. Data 0-RTT dapat di-<i>replay</i> penyerang, jadi
        hanya aman untuk request idempotent (lihat kotak “0-RTT dan replay” di Bab 2).
      </div>
    </section>
  );
}
