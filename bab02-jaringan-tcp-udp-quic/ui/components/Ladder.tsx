"use client";
// Diagram tangga (ladder): panggilan socket API di kiri/kanan, packet sebagai panah
// miring dari pengirim ke penerima. Packet yang dibuang netem berhenti di tengah (×).
import { Flow, TraceEvent } from "@/lib/api";

const W = 820;
const CL = 250; // lajur klien
const SV = 570; // lajur server
const CAP = 0.12; // jeda lebih lama dari ini dipadatkan (detik)
const PX = 800; // piksel per detik
const MIN_STEP = 15;

function short(s: string, n = 46) {
  return s.length > n ? s.slice(0, n - 1) + "…" : s;
}

export default function Ladder({ events, flows }: { events: TraceEvent[]; flows: Flow[] }) {
  const times: number[] = [];
  events.forEach((e) => times.push(e.ts));
  flows.forEach((f) => {
    if (f.send_ts) times.push(f.send_ts);
    if (f.recv_ts) times.push(f.recv_ts);
  });
  if (times.length === 0) return null;
  const sorted = Array.from(new Set(times)).sort((a, b) => a - b);
  const t0 = sorted[0];
  // pemetaan waktu -> y yang dipadatkan
  const ymap = new Map<number, number>();
  const breaks: { y: number; gap: number }[] = [];
  let y = 40;
  let prev = sorted[0];
  for (const t of sorted) {
    const gap = t - prev;
    if (gap > 0.0005) {
      let step = Math.min(gap, CAP) * PX;
      if (gap > CAP * 1.5) breaks.push({ y: y + step / 2, gap });
      step = Math.max(step, MIN_STEP);
      y += step;
    } else if (t !== prev) {
      y += 2;
    }
    ymap.set(t, y);
    prev = t;
  }
  const H = y + 40;
  const Y = (t: number) => ymap.get(t) ?? 0;
  const rel = (t: number) => ((t - t0) * 1000).toFixed(1);

  // label event di sisi yang sama dan y yang berdekatan digeser supaya tidak bertumpuk
  const lastY: Record<string, number> = { client: -100, server: -100 };
  const evPos = events.map((e) => {
    let yy = Y(e.ts);
    if (yy - lastY[e.side] < 13) yy = lastY[e.side] + 13;
    lastY[e.side] = yy;
    return yy;
  });

  return (
    <svg viewBox={`0 0 ${W} ${Math.max(H, ...evPos.map((v) => v + 30))}`} className="chart ladder">
      <defs>
        <marker id="arr" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
          <path d="M0,0 L10,5 L0,10 z" fill="#334155" />
        </marker>
        <marker id="arrR" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
          <path d="M0,0 L10,5 L0,10 z" fill="#ea580c" />
        </marker>
      </defs>
      <text x={CL} y={18} textAnchor="middle" fontWeight={700} fontSize={13}>
        Klien (prober)
      </text>
      <text x={SV} y={18} textAnchor="middle" fontWeight={700} fontSize={13}>
        Server (echo)
      </text>
      <text x={(CL + SV) / 2} y={18} textAnchor="middle" className="tick">
        router netem di tengah
      </text>
      <line x1={CL} x2={CL} y1={26} y2={H - 10} stroke="#94a3b8" strokeWidth={2} />
      <line x1={SV} x2={SV} y1={26} y2={H - 10} stroke="#94a3b8" strokeWidth={2} />
      <line x1={(CL + SV) / 2} x2={(CL + SV) / 2} y1={26} y2={H - 10} stroke="#e2e8f0" strokeDasharray="3 5" />
      {breaks.map((b, i) => (
        <g key={`b${i}`}>
          <text x={8} y={b.y + 4} className="tick" fill="#dc2626">
            ⋯ jeda {(b.gap * 1000).toFixed(0)} ms dipadatkan
          </text>
        </g>
      ))}
      {flows.map((f, i) => {
        const fromX = f.dir === "c2s" ? CL : SV;
        const toX = f.dir === "c2s" ? SV : CL;
        const label = `${f.kind}${f.len ? ` ${f.len}B` : ""}${f.retrans ? " (retransmission)" : ""}`;
        const color = f.retrans ? "#ea580c" : "#334155";
        if (f.send_ts && f.recv_ts) {
          const y1 = Y(f.send_ts);
          const y2 = Y(f.recv_ts);
          const mx = (fromX + toX) / 2;
          const my = (y1 + y2) / 2;
          return (
            <g key={i}>
              <line x1={fromX} y1={y1} x2={toX} y2={y2} stroke={color} strokeWidth={1.3} markerEnd={f.retrans ? "url(#arrR)" : "url(#arr)"} />
              <text x={mx + (i % 2 ? 40 : -40)} y={my - 3} textAnchor="middle" fill={color}>
                {label}
                <title>{f.info}</title>
              </text>
            </g>
          );
        }
        if (f.send_ts) {
          const y1 = Y(f.send_ts);
          const ex = fromX + (toX - fromX) * 0.55;
          const ey = y1 + 18;
          return (
            <g key={i}>
              <line x1={fromX} y1={y1} x2={ex} y2={ey} stroke="#dc2626" strokeWidth={1.3} strokeDasharray="4 3" />
              <text x={ex} y={ey + 5} textAnchor="middle" fill="#dc2626" fontSize={15} fontWeight={700}>
                ×
              </text>
              <text x={(fromX + ex) / 2} y={(y1 + ey) / 2 - 3} textAnchor="middle" fill="#dc2626">
                {label} hilang
                <title>{`dibuang netem: ${f.info}`}</title>
              </text>
            </g>
          );
        }
        if (f.recv_ts) {
          const y2 = Y(f.recv_ts);
          return <line key={i} x1={(fromX + toX) / 2} y1={y2 - 10} x2={toX} y2={y2} stroke={color} markerEnd="url(#arr)" />;
        }
        return null;
      })}
      {events.map((e, i) => {
        const left = e.side === "client";
        const x = left ? CL - 8 : SV + 8;
        return (
          <g key={`e${i}`}>
            <circle cx={left ? CL : SV} cy={Y(e.ts)} r={3} fill={left ? "#2563eb" : "#16a34a"} />
            <text x={x} y={evPos[i] + 4} textAnchor={left ? "end" : "start"} fill={left ? "#1e3a8a" : "#14532d"}>
              <tspan fontWeight={700}>{e.call}</tspan>
              <tspan className="tick"> {short(e.detail.replace(e.call, "").trim(), left ? 18 : 20)}</tspan>
              <title>{`${rel(e.ts)} ms · ${e.detail}`}</title>
            </text>
            <text x={left ? 6 : W - 6} y={evPos[i] + 4} textAnchor={left ? "start" : "end"} className="tick">
              {rel(e.ts)} ms
            </text>
          </g>
        );
      })}
    </svg>
  );
}
