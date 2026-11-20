"use client";

import { useState } from "react";
import LineChart from "./LineChart";
import { setKnob, useStream } from "./lib";

type Snap = {
  t_ms: number; produced: number; sunk: number; processed: number[]; busy: number[];
  jobs_len: number; jobs_cap: number; outs_len: number[]; results_len: number; results_cap: number;
  produce_rate: number; sink_rate: number; producer_blocked: number; latency_ms: number;
  produce_ms: number; work_ms: number; sink_ms: number;
};

function Queue({ len, cap, label }: { len: number; cap: number; label: string }) {
  const shown = Math.min(cap, 32);
  return (
    <div>
      <div className="queue" aria-label={`${label}: ${len} dari ${cap}`}>
        {cap === 0 ? <span className="muted">unbuffered</span> :
          Array.from({ length: shown }, (_, i) => <div key={i} className={`slot ${i < Math.round((len / cap) * shown) ? "full" : ""}`} />)}
      </div>
      <div className="qlabel">{label} {len}/{cap}</div>
    </div>
  );
}

function Knob({ label, value, max, onChange }: { label: string; value: number; max: number; onChange: (v: number) => void }) {
  return (
    <label>{label}: <b>{value} ms</b>
      <input type="range" min={label.startsWith("jeda generator") ? 1 : 0} max={max} value={value} onChange={(e) => onChange(+e.target.value)} />
    </label>
  );
}

export default function PipelinePanel() {
  const [workers, setWorkers] = useState(3);
  const [buffer, setBuffer] = useState(4);
  const [knobs, setKnobs] = useState({ produce_ms: 20, work_ms: 50, sink_ms: 10 });
  const [session, setSession] = useState<string | null>(null);
  const [snap, setSnap] = useState<Snap | null>(null);
  const [hist, setHist] = useState<Snap[]>([]);

  const { running, start, stop } = useStream({
    start: (d) => setSession((d as { session: string }).session),
    state: (d) => { const s = d as Snap; setSnap(s); setHist((h) => [...h.slice(-299), s]); },
    done: () => setSession(null),
  });

  function run() {
    setHist([]); setSnap(null);
    const q = new URLSearchParams({ workers: String(workers), buffer: String(buffer), seconds: "90", ...Object.fromEntries(Object.entries(knobs).map(([k, v]) => [k, String(v)])) });
    start(`pipeline/stream?${q}`);
  }
  function change(k: keyof typeof knobs, v: number) {
    setKnobs((o) => ({ ...o, [k]: v }));
    void setKnob(session, { [k]: v });
  }
  const pts = (f: (s: Snap) => number) => hist.map((s) => [s.t_ms / 1000, f(s)] as [number, number]);
  const capacity = knobs.work_ms > 0 ? (1000 / knobs.work_ms) * workers : Infinity;

  return (
    <div className="panel">
      <div className="card">
        <h2>4. CSP: pipeline goroutine + channel, fan-out/fan-in, backpressure</h2>
        <p className="theory">
          <em>Do not communicate by sharing memory; share memory by communicating.</em> Generator mengirim item ke channel
          <code>jobs</code>; N worker mengambil darinya (fan-out); setiap worker punya channel keluaran yang digabung
          (fan-in) menjadi <code>results</code>; sink mengonsumsi. Channel ber-buffer terbatas memberi <b>backpressure</b>:
          bila sink lambat, buffer penuh, dan generator tertahan di <code>jobs &lt;- item</code> sehingga lajunya turun
          mengikuti konsumen. Bab 6 subbab &ldquo;CSP di Go&rdquo; dan &ldquo;Backpressure dan Reactive Streams&rdquo;.
        </p>
        <div className="controls">
          <label>Worker (fan-out)<input type="number" min={1} max={16} value={workers} disabled={running} onChange={(e) => setWorkers(+e.target.value)} /></label>
          <label>Buffer tiap channel<input type="number" min={0} max={256} value={buffer} disabled={running} onChange={(e) => setBuffer(+e.target.value)} /></label>
          <button className="btn" onClick={run} disabled={running}>{running ? "Berjalan…" : "Mulai pipeline"}</button>
          {running && <button className="btn ghost" onClick={stop}>Hentikan</button>}
        </div>
        <div className="controls">
          <Knob label="jeda generator" value={knobs.produce_ms} max={200} onChange={(v) => change("produce_ms", v)} />
          <Knob label="kerja per item di worker" value={knobs.work_ms} max={500} onChange={(v) => change("work_ms", v)} />
          <Knob label="kerja di sink" value={knobs.sink_ms} max={500} onChange={(v) => change("sink_ms", v)} />
        </div>
        <p className="muted">
          Laju generator maksimal ≈ {(1000 / knobs.produce_ms).toFixed(0)}/s · kapasitas worker ≈ {Number.isFinite(capacity) ? capacity.toFixed(0) : "∞"}/s ·
          kapasitas sink ≈ {knobs.sink_ms > 0 ? (1000 / knobs.sink_ms).toFixed(0) : "∞"}/s. Throughput = minimum ketiganya. Geser slider selagi berjalan.
        </p>
      </div>

      <div className="card">
        <h3>Aliran item (live)</h3>
        {snap ? (
          <div className="flow">
            <div className={`stage ${snap.producer_blocked > 0.3 ? "busy" : ""}`}>
              <div>Generator</div><div className="n">{snap.produced}</div>
              <div className="muted">tertahan {(snap.producer_blocked * 100).toFixed(0)}%</div>
            </div>
            <Queue len={snap.jobs_len} cap={snap.jobs_cap} label="jobs" />
            <div className="workers">
              {snap.processed.map((p, i) => (
                <div key={i} className="row" style={{ justifyContent: "center" }}>
                  <div className={`stage ${snap.busy[i] ? "busy" : ""}`} style={{ minWidth: 120 }}>
                    <div>worker {i + 1}</div>
                    <div className="n">{p}</div>
                    <div className="muted">{snap.busy[i] ? `item #${snap.busy[i]}` : "idle / menunggu kirim"}</div>
                  </div>
                  <Queue len={snap.outs_len[i]} cap={snap.jobs_cap} label={`out${i + 1}`} />
                </div>
              ))}
            </div>
            <Queue len={snap.results_len} cap={snap.results_cap} label="results (fan-in)" />
            <div className="stage"><div>Sink</div><div className="n">{snap.sunk}</div>
              <div className="muted">umur item {snap.latency_ms.toFixed(0)} ms</div></div>
          </div>
        ) : <p className="muted">Tekan Mulai pipeline.</p>}
      </div>

      {hist.length > 1 && (
        <div className="grid2">
          <div className="card">
            <h3>Laju (item/detik)</h3>
            <LineChart ariaLabel="Laju generator dan sink" xLabel="detik" yLabel="item/s"
              series={[
                { name: "generator", color: "var(--s1)", points: pts((s) => s.produce_rate) },
                { name: "sink", color: "var(--s2)", points: pts((s) => s.sink_rate) },
              ]} />
          </div>
          <div className="card">
            <h3>Generator tertahan (% waktu) dan umur item di sink</h3>
            <LineChart ariaLabel="Persentase waktu generator tertahan" xLabel="detik" yLabel="% tertahan" yMin={0} yMax={100}
              series={[{ name: "tertahan", color: "var(--s3)", points: pts((s) => s.producer_blocked * 100) }]} height={150} />
            <LineChart ariaLabel="Umur item saat tiba di sink" xLabel="detik" yLabel="ms"
              series={[{ name: "umur item", color: "var(--s4)", points: pts((s) => s.latency_ms) }]} height={150} />
          </div>
        </div>
      )}
    </div>
  );
}
