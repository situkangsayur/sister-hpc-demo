"use client";

import { useState } from "react";
import LineChart from "./LineChart";
import { COLORS, LogLine, setKnob, useStream } from "./lib";

type ActorSnap = {
  t_ms: number; mailbox: number[]; handled: number[]; balance: number[]; restarts: number[]; crashed: boolean[];
  sent: number; in_flight: number; total: number; rate: number; process_ms: number; hot: number;
};
type LoopSnap = { t_ms: number; lag_avg_ms: number; lag_max_ms: number; queue: number; ticks: number; requests: number; req_p95_ms: number };

function ActorLab() {
  const [k, setK] = useState(4);
  const [cap, setCap] = useState(500);
  const [knobs, setKnobs] = useState({ rate: 100, process_ms: 5, hot: 0 });
  const [session, setSession] = useState<string | null>(null);
  const [snap, setSnap] = useState<ActorSnap | null>(null);
  const [hist, setHist] = useState<ActorSnap[]>([]);
  const [log, setLog] = useState<LogLine[]>([]);
  const { running, start, stop } = useStream({
    start: (d) => setSession((d as { session: string }).session),
    state: (d) => { const s = d as ActorSnap; setSnap(s); setHist((h) => [...h.slice(-239), s]); },
    log: (d) => setLog((l) => [...l.slice(-100), d as LogLine]),
    done: () => setSession(null),
  });
  function change(name: keyof typeof knobs, v: number) {
    setKnobs((o) => ({ ...o, [name]: v }));
    void setKnob(session, { [name]: v });
  }
  function run() {
    setHist([]); setLog([]); setSnap(null);
    start(`actor/stream?actors=${k}&mailbox=${cap}&rate=${knobs.rate}&process_ms=${knobs.process_ms}&hot=${knobs.hot}&seconds=120`);
  }
  const capPerActor = knobs.process_ms > 0 ? 1000 / knobs.process_ms : Infinity;

  return (
    <div className="card">
      <h2>6a. Actor model: mailbox per actor, supervisor, &ldquo;let it crash&rdquo;</h2>
      <p className="theory">
        Setiap actor rekening adalah satu goroutine dengan mailbox (channel) dan saldo privat. Tidak ada lock: pesan
        diproses satu per satu. Risiko khasnya mailbox menumpuk bila satu actor &ldquo;panas&rdquo;. Pesan racun membuat
        actor panic; supervisor menangkapnya dan memulai ulang actor (strategi one-for-one). Bab 6 subbab &ldquo;Actor Model&rdquo;.
      </p>
      <div className="controls">
        <label>Jumlah actor<input type="number" min={2} max={8} value={k} disabled={running} onChange={(e) => setK(+e.target.value)} /></label>
        <label>Kapasitas mailbox<input type="number" min={10} max={10000} value={cap} disabled={running} onChange={(e) => setCap(+e.target.value)} /></label>
        <button className="btn" onClick={run} disabled={running}>{running ? "Berjalan…" : "Mulai"}</button>
        {running && <button className="btn ghost" onClick={stop}>Hentikan</button>}
      </div>
      <div className="controls">
        <label>Laju pesan: <b>{knobs.rate}/s</b><input type="range" min={10} max={1000} step={10} value={knobs.rate} onChange={(e) => change("rate", +e.target.value)} /></label>
        <label>Proses per pesan: <b>{knobs.process_ms} ms</b><input type="range" min={0} max={50} value={knobs.process_ms} onChange={(e) => change("process_ms", +e.target.value)} /></label>
        <label className="row" style={{ display: "flex" }}>
          <input type="checkbox" checked={knobs.hot === 1} onChange={(e) => change("hot", e.target.checked ? 1 : 0)} /> actor 0 &ldquo;panas&rdquo; (70% pesan)
        </label>
      </div>
      <p className="muted">Kapasitas satu actor ≈ {Number.isFinite(capPerActor) ? capPerActor.toFixed(0) : "∞"} pesan/s. Bila pesan masuk ke satu actor lebih cepat dari itu, mailbox-nya menumpuk walau actor lain menganggur.</p>
      {snap && (
        <div className="grid2">
          <div>
            <div className="bars">
              {snap.mailbox.map((m, i) => (
                <div key={i} className="bar" style={{ gridTemplateColumns: "70px 1fr 64px auto" }}>
                  <span>actor {i}{snap.crashed[i] ? " (crash)" : ""}</span>
                  <div className="track"><div className={`fill ${i === 0 && snap.hot ? "hot" : ""}`} style={{ width: `${(m / cap) * 100}%` }} /></div>
                  <span className="val">{m}</span>
                  <button className="btn ghost" style={{ padding: "2px 8px", fontSize: 12 }} onClick={() => setKnob(session, { poison: i })} disabled={!running}>racun</button>
                </div>
              ))}
            </div>
            <p className="muted">Panjang mailbox (pesan antre) per actor.</p>
            <table className="data">
              <thead><tr><th>Actor</th><th>Diproses</th><th>Saldo</th><th>Restart</th></tr></thead>
              <tbody>
                {snap.handled.map((h, i) => (
                  <tr key={i}><td>{i}</td><td className="num">{h}</td><td className="num">{snap.balance[i]}</td><td className="num">{snap.restarts[i]}</td></tr>
                ))}
                <tr><th>Total</th><td className="num">terkirim {snap.sent}</td><td className="num">{snap.total}</td><td className="num">di mailbox {snap.in_flight}</td></tr>
              </tbody>
            </table>
            <p className="muted">Invarian: terkirim = jumlah saldo + pesan di mailbox, tanpa satu pun lock.</p>
          </div>
          <div>
            <LineChart ariaLabel="Panjang mailbox per actor" xLabel="detik" yLabel="pesan antre" height={220}
              series={snap.mailbox.map((_, i) => ({ name: `actor ${i}`, color: COLORS[i % COLORS.length], points: hist.map((h) => [h.t_ms / 1000, h.mailbox[i]] as [number, number]) })).slice(0, 5)} />
            <div className="log" style={{ marginTop: 8 }}>
              {log.map((l, i) => <div key={i}><span className="t">{(l.t_ms / 1000).toFixed(2)}s</span>{l.msg}</div>)}
              {log.length === 0 && <span className="muted">Log supervisor muncul di sini.</span>}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function EventLoopLab() {
  const [session, setSession] = useState<string | null>(null);
  const [hist, setHist] = useState<LoopSnap[]>([]);
  const [log, setLog] = useState<LogLine[]>([]);
  const [heavy, setHeavy] = useState(400);
  const { running, start, stop } = useStream({
    start: (d) => setSession((d as { session: string }).session),
    state: (d) => setHist((h) => [...h.slice(-299), d as LoopSnap]),
    log: (d) => setLog((l) => [...l.slice(-100), d as LogLine]),
    done: () => setSession(null),
  });
  const last = hist[hist.length - 1];
  const pts = (f: (s: LoopSnap) => number) => hist.map((s) => [s.t_ms / 1000, f(s)] as [number, number]);

  return (
    <div className="card">
      <h2>6b. Event loop: satu thread untuk semua callback</h2>
      <p className="theory">
        Satu goroutine menjalankan semua callback secara berurutan, seperti event loop Node.js/libuv atau
        <code>asyncio</code>. Timer 20 ms mengukur <b>event loop lag</b> (selisih jadwal dan saat callback benar-benar
        jalan); request masuk setiap 50 ms. Callback berat <b>di dalam</b> loop membekukan semuanya. Memindahkannya ke
        worker (seperti thread pool libuv) membuat loop tetap responsif. Bab 6 subbab &ldquo;Event Loop dan async/await&rdquo;.
      </p>
      <div className="controls">
        <button className="btn" onClick={() => { setHist([]); setLog([]); start("eventloop/stream?seconds=120"); }} disabled={running}>{running ? "Berjalan…" : "Mulai event loop"}</button>
        {running && <button className="btn ghost" onClick={stop}>Hentikan</button>}
        <label>Lama tugas berat (ms)<input type="number" min={10} max={3000} value={heavy} onChange={(e) => setHeavy(+e.target.value)} /></label>
        <button className="btn ghost" disabled={!running} onClick={() => setKnob(session, { block: heavy })}>Jalankan DI loop (blocking)</button>
        <button className="btn ghost" disabled={!running} onClick={() => setKnob(session, { offload: heavy })}>Pindah ke worker (offload)</button>
      </div>
      {last && (
        <p className="row">
          <span>lag maks <b>{last.lag_max_ms.toFixed(1)} ms</b></span>
          <span>p95 request <b>{last.req_p95_ms.toFixed(1)} ms</b></span>
          <span>antrean callback <b>{last.queue}</b></span>
          <span>timer <b>{last.ticks}</b></span>
          <span>request <b>{last.requests}</b></span>
        </p>
      )}
      {hist.length > 1 && (
        <div className="grid2">
          <LineChart ariaLabel="Event loop lag" xLabel="detik" yLabel="ms" height={220}
            series={[
              { name: "lag maks timer", color: "var(--s2)", points: pts((s) => s.lag_max_ms) },
              { name: "p95 latency request", color: "var(--s1)", points: pts((s) => s.req_p95_ms) },
            ]} />
          <div className="log">
            {log.map((l, i) => <div key={i}><span className="t">{(l.t_ms / 1000).toFixed(2)}s</span>{l.msg}</div>)}
          </div>
        </div>
      )}
    </div>
  );
}

export default function ModelsPanel() {
  return (
    <div className="panel">
      <ActorLab />
      <EventLoopLab />
    </div>
  );
}
