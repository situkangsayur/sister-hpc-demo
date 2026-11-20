"use client";

import { NODE_WARNA, STATUS_INFO, type Snapshot, angka } from "@/lib/types";

// Topologi: generator klien -> nginx -> tiga node -> PostgreSQL, plus monitor heartbeat.
// Titik yang bergerak di setiap jalur sebanding dengan request yang dijawab node itu pada detik terakhir.
export default function Topologi({ s }: { s: Snapshot }) {
  const terakhir = s.seri.length > 1 ? s.seri[s.seri.length - 2] : s.seri[s.seri.length - 1];
  const posY = [70, 180, 290];
  const lb = { x: 250, y: 180 };
  const db = { x: 690, y: 180 };
  return (
    <svg viewBox="0 0 760 360" className="topologi" role="img" aria-label="Topologi cluster: klien, nginx, tiga node, PostgreSQL">
      {/* klien -> nginx */}
      <path id="j-klien" d={`M 90 180 L ${lb.x - 45} 180`} className="jalur" />
      {s.lalu_lintas.aktif && <Titik jalur="j-klien" n={Math.min(6, Math.ceil(s.lalu_lintas.rps / 3))} dur={0.8} warna="var(--text-secondary)" />}
      {s.nodes.map((n, i) => {
        const y = posY[i];
        const id = `j-${n.nama}`;
        const jumlah = terakhir?.per_node?.[n.nama] ?? 0;
        const bermasalah = n.status === "mati" || n.status === "dicurigai";
        return (
          <g key={n.nama}>
            <path id={id} d={`M ${lb.x + 45} ${lb.y} C 360 ${lb.y}, 360 ${y}, 440 ${y}`} className={bermasalah ? "jalur jalur-putus" : "jalur"} />
            {jumlah > 0 && <Titik jalur={id} n={Math.min(6, jumlah)} dur={n.status === "lambat" ? 2.4 : 0.9} warna={NODE_WARNA[n.nama]} />}
            <path d={`M 560 ${y} C 620 ${y}, 620 ${db.y}, ${db.x - 40} ${db.y}`} className="jalur jalur-tipis" />
            {/* heartbeat dari monitor (atas) */}
            <path d={`M 500 22 L 500 ${y - 34}`} className="jalur-hb" />
            <text x={300} y={y < 180 ? y - 8 : y + 18} className="label-kecil">{jumlah} req/s</text>
          </g>
        );
      })}
      <g>
        <rect x={20} y={155} width={70} height={50} rx={8} className="kotak" />
        <text x={55} y={177} className="label-tengah">klien</text>
        <text x={55} y={194} className="label-kecil-tengah">{s.lalu_lintas.aktif ? `${s.lalu_lintas.rps}/s ${s.lalu_lintas.metode}` : "berhenti"}</text>
      </g>
      <g>
        <rect x={lb.x - 45} y={150} width={90} height={60} rx={8} className="kotak kotak-lb" />
        <text x={lb.x} y={175} className="label-tengah">nginx</text>
        <text x={lb.x} y={193} className="label-kecil-tengah">round robin</text>
      </g>
      <g>
        <rect x={455} y={2} width={90} height={22} rx={6} className="kotak" />
        <text x={500} y={17} className="label-kecil-tengah">monitor ♥ 500 ms</text>
      </g>
      {s.nodes.map((n, i) => {
        const y = posY[i];
        const st = STATUS_INFO[n.status];
        return (
          <g key={n.nama}>
            <rect x={440} y={y - 34} width={120} height={68} rx={10} className="kotak" style={{ stroke: NODE_WARNA[n.nama], strokeWidth: 2 }} />
            <circle cx={458} cy={y - 16} r={7} style={{ fill: st.warna }} className={n.status === "lambat" ? "denyut" : undefined} />
            <text x={472} y={y - 12} className="label">{n.nama}</text>
            <text x={452} y={y + 6} className="label-kecil">{st.ikon} {st.label}</text>
            <text x={452} y={y + 22} className="label-kecil">{n.rtt_ms > 0 ? `RTT ${angka(n.rtt_ms)} ms` : `gagal ×${n.gagal_beruntun}`}</text>
          </g>
        );
      })}
      <g>
        <path d={`M ${db.x - 40} 160 a 40 10 0 0 0 80 0 v 40 a 40 10 0 0 1 -80 0 z`} className="kotak" />
        <ellipse cx={db.x} cy={160} rx={40} ry={10} className="kotak" />
        <text x={db.x} y={188} className="label-tengah">Postgres</text>
        <text x={db.x} y={226} className="label-kecil-tengah">{s.db.ok ? `${s.db.total} baris` : "?"}</text>
      </g>
    </svg>
  );
}

function Titik({ jalur, n, dur, warna }: { jalur: string; n: number; dur: number; warna: string }) {
  return (
    <>
      {Array.from({ length: n }, (_, i) => (
        <circle key={i} r={4} style={{ fill: warna }}>
          <animateMotion dur={`${dur}s`} repeatCount="indefinite" begin={`${(i * dur) / n}s`}>
            <mpath href={`#${jalur}`} />
          </animateMotion>
        </circle>
      ))}
    </>
  );
}
