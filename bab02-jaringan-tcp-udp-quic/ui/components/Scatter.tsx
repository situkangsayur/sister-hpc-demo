"use client";
// Grafik titik sederhana dalam SVG (tanpa library chart agar image ramping).

export type Pt = { x: number; y: number; lost?: boolean; label?: string };
export type Series = { name: string; color: string; points: Pt[]; line?: boolean };

function niceTicks(max: number, count = 5): number[] {
  if (max <= 0) return [0];
  const raw = max / count;
  const mag = Math.pow(10, Math.floor(Math.log10(raw)));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= raw) ?? raw;
  const ticks: number[] = [];
  // tick terakhir selalu >= max agar tidak ada titik yang keluar dari grafik
  for (let v = 0; ; v += step) {
    ticks.push(Number(v.toPrecision(6)));
    if (v >= max - 1e-9) break;
  }
  return ticks;
}

export default function Scatter({
  series,
  xLabel,
  yLabel,
  height = 280,
  threshold,
}: {
  series: Series[];
  xLabel: string;
  yLabel: string;
  height?: number;
  threshold?: number;
}) {
  const W = 760;
  const H = height;
  const m = { l: 56, r: 12, t: 26, b: 38 };
  const all = series.flatMap((s) => s.points);
  const ok = all.filter((p) => !p.lost);
  const xMax = Math.max(1, ...all.map((p) => p.x));
  const yMaxRaw = Math.max(1, ...ok.map((p) => p.y), threshold ?? 0);
  const yTicks = niceTicks(yMaxRaw);
  const yMax = yTicks[yTicks.length - 1] || 1;
  const xTicks = niceTicks(xMax, 8);
  const xm = xTicks[xTicks.length - 1] || 1;
  const px = (x: number) => m.l + (x / xm) * (W - m.l - m.r);
  const py = (y: number) => H - m.b - (y / yMax) * (H - m.t - m.b);
  const lostY = m.t - 12;

  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="chart" role="img" aria-label={yLabel}>
      {yTicks.map((t) => (
        <g key={`y${t}`}>
          <line x1={m.l} x2={W - m.r} y1={py(t)} y2={py(t)} className="grid" />
          <text x={m.l - 6} y={py(t) + 4} textAnchor="end" className="tick">
            {t}
          </text>
        </g>
      ))}
      {xTicks.map((t) => (
        <text key={`x${t}`} x={px(t)} y={H - m.b + 16} textAnchor="middle" className="tick">
          {t}
        </text>
      ))}
      <text x={m.l + (W - m.l - m.r) / 2} y={H - 4} textAnchor="middle" className="axis">
        {xLabel}
      </text>
      <text x={14} y={m.t + (H - m.t - m.b) / 2} transform={`rotate(-90 14 ${m.t + (H - m.t - m.b) / 2})`} textAnchor="middle" className="axis">
        {yLabel}
      </text>
      <text x={m.l - 6} y={lostY + 4} textAnchor="end" className="tick lost-label">
        hilang
      </text>
      {threshold !== undefined && (
        <g>
          <line x1={m.l} x2={W - m.r} y1={py(threshold)} y2={py(threshold)} className="threshold" />
          <text x={W - m.r} y={py(threshold) - 4} textAnchor="end" className="tick">
            ambang tersendat {threshold} ms
          </text>
        </g>
      )}
      {series.map((s) => {
        const pts = s.points.filter((p) => !p.lost).sort((a, b) => a.x - b.x);
        return (
          <g key={s.name}>
            {s.line && pts.length > 1 && (
              <polyline
                fill="none"
                stroke={s.color}
                strokeOpacity={0.35}
                strokeWidth={1.2}
                points={pts.map((p) => `${px(p.x)},${py(p.y)}`).join(" ")}
              />
            )}
            {pts.map((p, i) => (
              <circle key={i} cx={px(p.x)} cy={py(p.y)} r={2.8} fill={s.color}>
                <title>{p.label ?? `${s.name}: ${p.y} ms`}</title>
              </circle>
            ))}
            {s.points
              .filter((p) => p.lost)
              .map((p, i) => (
                <text key={`l${i}`} x={px(p.x)} y={lostY + 4} textAnchor="middle" fill={s.color} className="lost-mark">
                  ×<title>{p.label ?? `${s.name}: hilang`}</title>
                </text>
              ))}
          </g>
        );
      })}
    </svg>
  );
}

export function Legend({ items }: { items: { name: string; color: string }[] }) {
  return (
    <div className="legend">
      {items.map((i) => (
        <span key={i.name}>
          <i style={{ background: i.color }} /> {i.name}
        </span>
      ))}
    </div>
  );
}
