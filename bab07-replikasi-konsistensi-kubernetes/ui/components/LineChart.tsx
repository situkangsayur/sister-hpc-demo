// Grafik garis SVG sederhana tanpa pustaka tambahan (hemat ukuran image).
export function LineChart({ points, height = 140, unit = "", color = "var(--accent)", label }: {
  points: { x: number; y: number }[]; height?: number; unit?: string; color?: string; label: string;
}) {
  const W = 600, H = height, pad = 34;
  if (points.length < 2) return <p className="muted">Menunggu data...</p>;
  const xs = points.map((p) => p.x), ys = points.map((p) => p.y);
  const x0 = Math.min(...xs), x1 = Math.max(...xs), ymax = Math.max(0.001, ...ys) * 1.15;
  const sx = (x: number) => pad + ((x - x0) / Math.max(1, x1 - x0)) * (W - pad - 8);
  const sy = (y: number) => H - 18 - (y / ymax) * (H - 30);
  const d = points.map((p, i) => `${i ? "L" : "M"}${sx(p.x).toFixed(1)},${sy(p.y).toFixed(1)}`).join(" ");
  return (
    <svg viewBox={`0 0 ${W} ${H}`} width="100%" role="img" aria-label={label}>
      <line x1={pad} x2={W - 8} y1={H - 18} y2={H - 18} stroke="var(--line)" />
      <line x1={pad} x2={pad} y1={8} y2={H - 18} stroke="var(--line)" />
      <text x={2} y={14} fontSize={10}>{ymax.toFixed(ymax < 10 ? 2 : 0)}{unit}</text>
      <text x={2} y={H - 18} fontSize={10}>0</text>
      <path d={d} fill="none" stroke={color} strokeWidth={2} />
    </svg>
  );
}
