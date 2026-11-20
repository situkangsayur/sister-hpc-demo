"use client";

// Grafik garis SVG sederhana (tanpa library) untuk data live.
export interface Series {
  name: string;
  color: string;
  values: number[];
}

interface Props {
  title: string;
  unit: string;
  xs: number[];
  series: Series[];
  height?: number;
}

function niceMax(v: number): number {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

function fmt(v: number): string {
  if (v >= 1000) return (v / 1000).toFixed(v >= 10000 ? 0 : 1) + "k";
  if (v >= 10) return v.toFixed(0);
  if (v >= 1) return v.toFixed(1);
  return v.toFixed(2);
}

export default function LineChart({ title, unit, xs, series, height = 200 }: Props) {
  const W = 600;
  const H = height;
  const pad = { l: 46, r: 12, t: 10, b: 24 };
  const iw = W - pad.l - pad.r;
  const ih = H - pad.t - pad.b;
  const maxY = niceMax(Math.max(0, ...series.flatMap((s) => s.values)));
  const maxX = Math.max(1, xs.length ? xs[xs.length - 1] : 1);
  const x = (t: number) => pad.l + (t / maxX) * iw;
  const y = (v: number) => pad.t + ih - (v / maxY) * ih;
  const ticks = [0, 0.25, 0.5, 0.75, 1].map((f) => f * maxY);
  const last = (s: Series) => (s.values.length ? s.values[s.values.length - 1] : 0);

  return (
    <div className="card">
      <div className="card-head">
        <span>
          {title} <span className="muted">({unit})</span>
        </span>
        <span className="legend">
          {series.map((s) => (
            <span key={s.name}>
              <i style={{ background: s.color }} />
              {s.name} <b>{fmt(last(s))}</b>
            </span>
          ))}
        </span>
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} className="chart" role="img" aria-label={title}>
        {ticks.map((v) => (
          <g key={v}>
            <line x1={pad.l} x2={W - pad.r} y1={y(v)} y2={y(v)} className="grid" />
            <text x={pad.l - 6} y={y(v) + 4} textAnchor="end" className="axis">
              {fmt(v)}
            </text>
          </g>
        ))}
        <text x={pad.l} y={H - 6} className="axis">0 s</text>
        <text x={W - pad.r} y={H - 6} textAnchor="end" className="axis">
          {maxX.toFixed(0)} s
        </text>
        {series.map((s) =>
          s.values.length > 0 ? (
            <polyline
              key={s.name}
              fill="none"
              stroke={s.color}
              strokeWidth={2}
              strokeLinejoin="round"
              points={s.values.map((v, i) => `${x(xs[i] ?? 0).toFixed(1)},${y(v).toFixed(1)}`).join(" ")}
            />
          ) : null,
        )}
        {xs.length === 0 && (
          <text x={W / 2} y={H / 2} textAnchor="middle" className="axis">
            belum ada data
          </text>
        )}
      </svg>
    </div>
  );
}
