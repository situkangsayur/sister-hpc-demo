"use client";
// Grafik batang horizontal sederhana (SVG). Satu atau dua seri, satu sumbu,
// label nilai di ujung batang, tooltip saat hover.
import { useState } from "react";

export type Seri = { nama: string; warna: string; nilai: number[] };

export default function Batang({
  kategori,
  seri,
  satuan,
  judul,
}: {
  kategori: string[];
  seri: Seri[];
  satuan: string;
  judul: string;
}) {
  const [hover, setHover] = useState<{ k: number; s: number } | null>(null);
  const maks = Math.max(1, ...seri.flatMap((s) => s.nilai));
  const tinggiBatang = 14;
  const jarak = 2;
  const tinggiGrup = seri.length * (tinggiBatang + jarak) + 12;
  const kiri = 92;
  const lebar = 520;
  const kanan = 70;
  const plot = lebar - kiri - kanan;
  const tinggi = kategori.length * tinggiGrup + 24;
  const ticks = [0, 0.25, 0.5, 0.75, 1].map((t) => t * maks);
  const fmt = (v: number) => (Number.isInteger(v) ? String(v) : v >= 100 ? v.toFixed(0) : v >= 10 ? v.toFixed(1) : v.toFixed(2));
  return (
    <figure style={{ margin: 0 }}>
      <figcaption className="kecil" style={{ marginBottom: 4 }}>
        <b style={{ color: "var(--text)" }}>{judul}</b>
        {seri.length > 1 && (
          <span style={{ marginLeft: 12 }}>
            {seri.map((s) => (
              <span key={s.nama} style={{ marginRight: 12 }}>
                <span style={{ display: "inline-block", width: 10, height: 10, borderRadius: 2, background: s.warna, marginRight: 4 }} />
                {s.nama}
              </span>
            ))}
          </span>
        )}
      </figcaption>
      <svg viewBox={`0 0 ${lebar} ${tinggi}`} width="100%" role="img" aria-label={judul} style={{ display: "block" }}>
        {ticks.map((t, i) => {
          const x = kiri + (t / maks) * plot;
          return (
            <g key={i}>
              <line x1={x} x2={x} y1={0} y2={tinggi - 20} stroke="var(--grid)" strokeWidth={1} />
              <text x={x} y={tinggi - 6} fontSize={10} fill="var(--muted)" textAnchor="middle">
                {fmt(t)}
              </text>
            </g>
          );
        })}
        {kategori.map((k, ki) => {
          const y0 = ki * tinggiGrup + 4;
          return (
            <g key={k}>
              <text x={kiri - 8} y={y0 + (seri.length * (tinggiBatang + jarak)) / 2 + 4} fontSize={11.5} fill="var(--text-2)" textAnchor="end">
                {k}
              </text>
              {seri.map((s, si) => {
                const v = s.nilai[ki] ?? 0;
                const w = Math.max(1, (v / maks) * plot);
                const y = y0 + si * (tinggiBatang + jarak);
                const aktif = hover && hover.k === ki && hover.s === si;
                return (
                  <g key={s.nama} onMouseEnter={() => setHover({ k: ki, s: si })} onMouseLeave={() => setHover(null)}>
                    <rect x={kiri} y={y - 1} width={plot + kanan} height={tinggiBatang + 2} fill="transparent" />
                    <path
                      d={`M${kiri},${y} h${Math.max(0, w - 4)} a4,4 0 0 1 4,4 v${tinggiBatang - 8} a4,4 0 0 1 -4,4 h${-Math.max(0, w - 4)} z`}
                      fill={s.warna}
                      opacity={hover && !aktif ? 0.55 : 1}
                    />
                    <text x={kiri + w + 6} y={y + tinggiBatang - 3} fontSize={11} fill="var(--text)">
                      {fmt(v)} {satuan}
                    </text>
                    <title>{`${k} · ${s.nama}: ${fmt(v)} ${satuan}`}</title>
                  </g>
                );
              })}
            </g>
          );
        })}
        <line x1={kiri} x2={kiri} y1={0} y2={tinggi - 20} stroke="var(--border)" />
      </svg>
    </figure>
  );
}
