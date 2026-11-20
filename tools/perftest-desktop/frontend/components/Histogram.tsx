"use client";

import type { Bin } from "@/lib/types";

// Histogram latency: batang linear dari min sampai p99.9 (ekor masuk batang terakhir).
export default function Histogram({ bins, p50, p95, p99 }: { bins: Bin[]; p50?: number; p95?: number; p99?: number }) {
  const W = 600;
  const H = 200;
  const pad = { l: 46, r: 12, t: 10, b: 28 };
  const iw = W - pad.l - pad.r;
  const ih = H - pad.t - pad.b;
  const max = Math.max(1, ...bins.map((b) => b.count));
  const lo = bins.length ? bins[0].fromMs : 0;
  const hi = bins.length ? bins[bins.length - 1].fromMs + (bins[0].toMs - bins[0].fromMs) : 1;
  const bw = bins.length ? iw / bins.length : 0;
  const xOf = (ms: number) => pad.l + ((ms - lo) / Math.max(hi - lo, 1e-9)) * iw;
  const marks = [
    { n: "p50", v: p50, c: "var(--c1)" },
    { n: "p95", v: p95, c: "var(--c2)" },
    { n: "p99", v: p99, c: "var(--c3)" },
  ].filter((m) => m.v !== undefined && m.v >= lo && m.v <= hi);

  return (
    <div className="card">
      <div className="card-head">
        <span>Histogram latency (request sukses)</span>
        <span className="muted">{bins.length ? `${lo.toFixed(2)}–${bins[bins.length - 1].toMs.toFixed(1)} ms` : ""}</span>
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} className="chart" role="img" aria-label="histogram latency">
        <line x1={pad.l} x2={W - pad.r} y1={pad.t + ih} y2={pad.t + ih} className="grid" />
        <text x={pad.l - 6} y={pad.t + 8} textAnchor="end" className="axis">{max}</text>
        <text x={pad.l - 6} y={pad.t + ih} textAnchor="end" className="axis">0</text>
        {bins.map((b, i) => {
          const h = (b.count / max) * ih;
          return (
            <rect key={i} x={pad.l + i * bw + 1} y={pad.t + ih - h} width={Math.max(bw - 2, 1)} height={h} className="bar">
              <title>{`${b.fromMs.toFixed(2)}–${b.toMs.toFixed(2)} ms: ${b.count}`}</title>
            </rect>
          );
        })}
        {marks.map((m) => (
          <g key={m.n}>
            <line x1={xOf(m.v!)} x2={xOf(m.v!)} y1={pad.t} y2={pad.t + ih} stroke={m.c} strokeDasharray="4 3" strokeWidth={1.5} />
            <text x={xOf(m.v!) + 3} y={pad.t + 10} fill={m.c} className="axis">{m.n}</text>
          </g>
        ))}
        {bins.length > 0 && (
          <>
            <text x={pad.l} y={H - 8} className="axis">{lo.toFixed(1)} ms</text>
            <text x={W - pad.r} y={H - 8} textAnchor="end" className="axis">{bins[bins.length - 1].toMs.toFixed(1)} ms</text>
          </>
        )}
        {bins.length === 0 && (
          <text x={W / 2} y={H / 2} textAnchor="middle" className="axis">histogram muncul setelah run selesai</text>
        )}
      </svg>
    </div>
  );
}
