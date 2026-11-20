"use client";

import { useEffect, useState } from "react";
import DeadlockPanel from "./DeadlockPanel";
import ModelsPanel from "./ModelsPanel";
import PipelinePanel from "./PipelinePanel";
import PoolPanel from "./PoolPanel";
import RacePanel from "./RacePanel";
import RuntimePanel, { Metrics } from "./RuntimePanel";

const TABS = [
  { id: "race", label: "1 · Race condition", el: () => <RacePanel /> },
  { id: "deadlock", label: "2 · Deadlock", el: () => <DeadlockPanel /> },
  { id: "pool", label: "3 · Worker pool", el: () => <PoolPanel /> },
  { id: "pipeline", label: "4 · CSP pipeline", el: () => <PipelinePanel /> },
  { id: "runtime", label: "5 · Goroutine vs thread", el: (h: Metrics[]) => <RuntimePanel hist={h} /> },
  { id: "models", label: "6 · Actor & event loop", el: () => <ModelsPanel /> },
];

function Tile({ k, v, live }: { k: string; v: string; live?: boolean }) {
  return <div className={`tile ${live ? "live" : ""}`}><div className="k">{k}</div><div className="v">{v}</div></div>;
}

export default function Lab() {
  const [tab, setTab] = useState("race");
  const [hist, setHist] = useState<Metrics[]>([]);
  const [online, setOnline] = useState(false);

  useEffect(() => {
    const h = window.location.hash.slice(1);
    if (TABS.some((t) => t.id === h)) setTab(h);
  }, []);

  // Satu stream metrik runtime untuk seluruh halaman (dibuka ulang otomatis oleh EventSource).
  useEffect(() => {
    const es = new EventSource("/api/metrics/stream?interval_ms=500");
    es.addEventListener("metrics", (e) => {
      setOnline(true);
      const m = JSON.parse((e as MessageEvent).data) as Metrics;
      setHist((h) => [...h.slice(-239), m]);
    });
    es.onerror = () => setOnline(false);
    return () => es.close();
  }, []);

  const m = hist[hist.length - 1];
  const select = (id: string) => { setTab(id); history.replaceState(null, "", `#${id}`); };
  const current = TABS.find((t) => t.id === tab) ?? TABS[0];

  return (
    <div className="wrap">
      <header className="top">
        <h1>Concurrency Lab</h1>
        <span className="sub">Bab 6 Concurrency dan Runtime · slide P06 · Sistem Terdistribusi dan HPC, STMIK Tazkia</span>
      </header>
      <div className="metrics" aria-live="polite">
        <Tile live={online} k={online ? "runtime lab" : "lab offline"} v={m ? `${m.num_cpu} CPU` : "…"} />
        <Tile k="GOMAXPROCS" v={m ? String(m.gomaxprocs) : "…"} />
        <Tile k="Goroutine" v={m ? m.goroutines.toLocaleString("id-ID") : "…"} />
        <Tile k="Thread OS" v={m ? String(m.threads) : "…"} />
        <Tile k="Heap" v={m ? `${m.heap_alloc_mb.toFixed(1)} MB` : "…"} />
        <Tile k="Stack" v={m ? `${m.stack_inuse_mb.toFixed(1)} MB` : "…"} />
        <Tile k="RSS" v={m ? `${(m.rss_kb / 1024).toFixed(0)} MB` : "…"} />
        <Tile k="GC / jeda terakhir" v={m ? `${m.num_gc} / ${m.last_pause_us.toFixed(0)} µs` : "…"} />
      </div>
      <nav className="tabs" role="tablist">
        {TABS.map((t) => (
          <button key={t.id} role="tab" aria-selected={tab === t.id} onClick={() => select(t.id)}>{t.label}</button>
        ))}
      </nav>
      {current.el(hist)}
      <footer>Backend Go (goroutine, channel, sync, sync/atomic, runtime) · UI Next.js · data live lewat Server-Sent Events.</footer>
    </div>
  );
}
