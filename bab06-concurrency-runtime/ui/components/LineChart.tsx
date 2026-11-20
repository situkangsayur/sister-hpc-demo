"use client";

import { useEffect, useMemo, useRef, useState } from "react";

export type Series = { name: string; color: string; points: [number, number][] };

type Props = {
  series: Series[];
  height?: number;
  xLabel?: string;
  yLabel?: string;
  yMin?: number;
  yMax?: number;
  logX?: boolean;
  xFmt?: (x: number) => string;
  yFmt?: (y: number) => string;
  markers?: boolean;
  ariaLabel: string;
};

const M = { l: 52, r: 14, t: 10, b: 34 };

function niceTicks(lo: number, hi: number, n = 5): number[] {
  if (!(hi > lo)) return [lo];
  const raw = (hi - lo) / n;
  const mag = Math.pow(10, Math.floor(Math.log10(raw)));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= raw) ?? raw;
  const out: number[] = [];
  for (let v = Math.ceil(lo / step) * step; v <= hi + 1e-9; v += step) out.push(+v.toFixed(10));
  return out;
}

export const fmtNum = (v: number) =>
  Math.abs(v) >= 10000 ? `${(v / 1000).toFixed(0)}k` : Math.abs(v) >= 100 ? v.toFixed(0) : Math.abs(v) >= 10 ? v.toFixed(1) : v.toFixed(2);

/** Line chart SVG sederhana dengan crosshair dan tooltip. Satu sumbu y saja. */
export default function LineChart({
  series, height = 240, xLabel, yLabel, yMin, yMax, logX, xFmt = fmtNum, yFmt = fmtNum, markers, ariaLabel,
}: Props) {
  const H = height;
  const ref = useRef<SVGSVGElement>(null);
  const box = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<number | null>(null);
  // Lebar viewBox mengikuti lebar container agar teks sumbu tetap 11px di kartu sempit.
  const [W, setW] = useState(640);
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setW(Math.max(260, Math.round(e.contentRect.width))));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const { xs, x0, x1, y0, y1 } = useMemo(() => {
    const all = series.flatMap((s) => s.points);
    const xsSet = Array.from(new Set(all.map((p) => p[0]))).sort((a, b) => a - b);
    const xVals = all.map((p) => p[0]);
    const yVals = all.map((p) => p[1]);
    const lo = yMin ?? Math.min(0, ...yVals);
    let hi = yMax ?? Math.max(1e-9, ...yVals);
    if (yMax === undefined) hi = hi * 1.08;
    return {
      xs: xsSet,
      x0: xVals.length ? Math.min(...xVals) : 0,
      x1: xVals.length ? Math.max(...xVals) : 1,
      y0: lo,
      y1: hi,
    };
  }, [series, yMin, yMax]);

  const tx = (x: number) => {
    const a = logX ? Math.log2(Math.max(x, 1e-9)) : x;
    const lo = logX ? Math.log2(Math.max(x0, 1e-9)) : x0;
    const hi = logX ? Math.log2(Math.max(x1, 1e-9)) : x1;
    return M.l + (hi > lo ? ((a - lo) / (hi - lo)) * (W - M.l - M.r) : (W - M.l - M.r) / 2);
  };
  const ty = (y: number) => M.t + (1 - (y - y0) / (y1 - y0 || 1)) * (H - M.t - M.b);
  const yTicks = niceTicks(y0, y1);
  const xTicks = logX ? xs : niceTicks(x0, x1, 6);

  function onMove(e: React.PointerEvent<SVGSVGElement>) {
    const svg = ref.current;
    if (!svg || xs.length === 0) return;
    const r = svg.getBoundingClientRect();
    const px = ((e.clientX - r.left) / r.width) * W;
    let best = 0;
    let bd = Infinity;
    xs.forEach((x, i) => {
      const d = Math.abs(tx(x) - px);
      if (d < bd) { bd = d; best = i; }
    });
    setHover(best);
  }

  const hx = hover !== null ? xs[hover] : null;
  const leftPct = hx !== null ? (tx(hx) / W) * 100 : 0;

  return (
    <div className="chart" ref={box}>
      {series.length > 1 && (
        <div className="legend">
          {series.map((s) => (
            <span key={s.name} style={{ ["--c" as string]: s.color }}>{s.name}</span>
          ))}
        </div>
      )}
      <svg
        ref={ref}
        viewBox={`0 0 ${W} ${H}`}
        role="img"
        aria-label={ariaLabel}
        onPointerMove={onMove}
        onPointerLeave={() => setHover(null)}
      >
        <g className="axis">
          {yTicks.map((t) => (
            <g key={`y${t}`}>
              <line x1={M.l} x2={W - M.r} y1={ty(t)} y2={ty(t)} />
              <text x={M.l - 6} y={ty(t) + 4} textAnchor="end">{yFmt(t)}</text>
            </g>
          ))}
          {xTicks.map((t) => (
            <text key={`x${t}`} x={tx(t)} y={H - M.b + 16} textAnchor="middle">{xFmt(t)}</text>
          ))}
          {xLabel && <text x={(M.l + W - M.r) / 2} y={H - 2} textAnchor="middle">{xLabel}</text>}
          {yLabel && (
            <text transform={`translate(12 ${(M.t + H - M.b) / 2}) rotate(-90)`} textAnchor="middle">{yLabel}</text>
          )}
        </g>
        {series.map((s) => (
          <g key={s.name}>
            <polyline
              fill="none"
              stroke={s.color}
              strokeWidth={2}
              strokeLinejoin="round"
              strokeLinecap="round"
              points={s.points.map(([x, y]) => `${tx(x)},${ty(y)}`).join(" ")}
            />
            {markers &&
              s.points.map(([x, y], i) => (
                <circle key={i} cx={tx(x)} cy={ty(y)} r={4} fill={s.color} stroke="var(--surface)" strokeWidth={2} />
              ))}
          </g>
        ))}
        {hx !== null && (
          <line x1={tx(hx)} x2={tx(hx)} y1={M.t} y2={H - M.b} stroke="var(--ink-3)" strokeDasharray="3 3" />
        )}
      </svg>
      {hx !== null && (
        <div className="tip" style={{ left: `min(calc(${leftPct}% + 10px), calc(100% - 170px))`, top: 28 }}>
          <div>{xLabel ?? "x"}: <b>{xFmt(hx)}</b></div>
          {series.map((s) => {
            const p = s.points.find((q) => q[0] === hx);
            return p ? (
              <div key={s.name}>
                <span style={{ color: s.color }}>■</span> {s.name}: <b>{yFmt(p[1])}</b>
              </div>
            ) : null;
          })}
        </div>
      )}
    </div>
  );
}
