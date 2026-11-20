"use client";

import { useState } from "react";
import { NODE_WARNA, type Bucket, angka } from "@/lib/types";

const W = 640;
const H = 200;
const PAD = { l: 52, r: 8, t: 10, b: 22 };
const NODES = ["node1", "node2", "node3"];

function skalaMaks(v: number) {
  if (v <= 5) return 5;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  return Math.ceil(v / p) * p;
}

function jam(t: number) {
  return new Date(t * 1000).toLocaleTimeString("id-ID", { hour12: false });
}

// Distribusi request per node per detik (stacked bar), ditambah request gagal di puncak.
export function GrafikDistribusi({ seri }: { seri: Bucket[] }) {
  const [hover, setHover] = useState<number | null>(null);
  const data = seri.slice(-60);
  const maks = skalaMaks(Math.max(1, ...data.map((b) => b.sukses + b.gagal)));
  const lebar = (W - PAD.l - PAD.r) / 60;
  const y = (v: number) => PAD.t + (H - PAD.t - PAD.b) * (1 - v / maks);
  const b = hover !== null ? data[hover] : null;
  return (
    <div className="grafik">
      <Legenda item={[...NODES.map((n) => ({ nama: n, warna: NODE_WARNA[n] })), { nama: "gagal", warna: "var(--status-critical)" }]} />
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label="Request per detik per node" onMouseLeave={() => setHover(null)}>
        <Sumbu maks={maks} y={y} satuan="" />
        {data.map((bk, i) => {
          let dasar = 0;
          const x = PAD.l + i * lebar;
          const seg = [...NODES.map((n) => ({ k: n, v: bk.per_node[n] ?? 0, w: NODE_WARNA[n] })), { k: "gagal", v: bk.gagal, w: "var(--status-critical)" }];
          return (
            <g key={bk.t} onMouseEnter={() => setHover(i)}>
              <rect x={x} y={PAD.t} width={lebar} height={H - PAD.t - PAD.b} fill="transparent" />
              {seg.map((sg) => {
                if (sg.v === 0) return null;
                const y1 = y(dasar + sg.v);
                const h = y(dasar) - y1;
                dasar += sg.v;
                return <rect key={sg.k} x={x + 1} y={y1 + 1} width={Math.max(1, lebar - 2)} height={Math.max(0, h - 1)} rx={1.5} style={{ fill: sg.w }} />;
              })}
            </g>
          );
        })}
        {hover !== null && <rect x={PAD.l + hover * lebar} y={PAD.t} width={lebar} height={H - PAD.t - PAD.b} className="sorot" />}
      </svg>
      {b && (
        <div className="tooltip">
          <b>{jam(b.t)}</b> · {NODES.map((n) => `${n}: ${b.per_node[n] ?? 0}`).join(" · ")} · gagal: {b.gagal} · retry: {b.retry}
        </div>
      )}
    </div>
  );
}

// Latency p50 dan p95 per detik, diukur dari sisi klien (generator lalu lintas).
export function GrafikLatency({ seri }: { seri: Bucket[] }) {
  const [hover, setHover] = useState<number | null>(null);
  const data = seri.slice(-60);
  const maks = skalaMaks(Math.max(10, ...data.map((b) => b.p95)));
  const lebar = (W - PAD.l - PAD.r) / 59;
  const x = (i: number) => PAD.l + i * lebar;
  const y = (v: number) => PAD.t + (H - PAD.t - PAD.b) * (1 - v / maks);
  const garis = (k: "p50" | "p95") => data.map((b, i) => `${i ? "L" : "M"} ${x(i)} ${y(b[k])}`).join(" ");
  const b = hover !== null ? data[hover] : null;
  return (
    <div className="grafik">
      <Legenda item={[{ nama: "p50", warna: "var(--series-7)" }, { nama: "p95 (putus-putus)", warna: "var(--series-7)", putus: true }]} />
      <svg
        viewBox={`0 0 ${W} ${H}`}
        role="img"
        aria-label="Latency p50 dan p95 per detik"
        onMouseLeave={() => setHover(null)}
        onMouseMove={(e) => {
          const r = e.currentTarget.getBoundingClientRect();
          const px = ((e.clientX - r.left) / r.width) * W;
          const i = Math.round((px - PAD.l) / lebar);
          setHover(i >= 0 && i < data.length ? i : null);
        }}
      >
        <Sumbu maks={maks} y={y} satuan=" ms" />
        {data.length > 1 && (
          <>
            <path d={garis("p95")} className="garis" strokeDasharray="5 4" style={{ stroke: "var(--series-7)" }} />
            <path d={garis("p50")} className="garis" style={{ stroke: "var(--series-7)" }} />
          </>
        )}
        {hover !== null && b && (
          <>
            <line x1={x(hover)} x2={x(hover)} y1={PAD.t} y2={H - PAD.b} className="crosshair" />
            <circle cx={x(hover)} cy={y(b.p50)} r={4} className="titik-hover" />
            <circle cx={x(hover)} cy={y(b.p95)} r={4} className="titik-hover" />
          </>
        )}
      </svg>
      {b && (
        <div className="tooltip">
          <b>{jam(b.t)}</b> · p50 {angka(b.p50)} ms · p95 {angka(b.p95)} ms · maks {angka(b.maks)} ms
        </div>
      )}
    </div>
  );
}

function Sumbu({ maks, y, satuan }: { maks: number; y: (v: number) => number; satuan: string }) {
  const tick = [0, maks / 2, maks];
  return (
    <g>
      {tick.map((t) => (
        <g key={t}>
          <line x1={PAD.l} x2={W - PAD.r} y1={y(t)} y2={y(t)} className="grid" />
          <text x={PAD.l - 4} y={y(t) + 4} className="label-sumbu">
            {Number.isInteger(t) ? t : angka(t)}
            {t === maks ? satuan : ""}
          </text>
        </g>
      ))}
      <text x={PAD.l} y={H - 6} className="label-sumbu-kiri">−60 s</text>
      <text x={W - PAD.r} y={H - 6} className="label-sumbu">sekarang</text>
    </g>
  );
}

function Legenda({ item }: { item: { nama: string; warna: string; putus?: boolean }[] }) {
  return (
    <div className="legenda">
      {item.map((it) => (
        <span key={it.nama}>
          <svg width="18" height="10" aria-hidden>
            {it.putus ? (
              <line x1="0" x2="18" y1="5" y2="5" style={{ stroke: it.warna }} strokeWidth="2" strokeDasharray="4 3" />
            ) : (
              <rect width="18" height="10" rx="2" style={{ fill: it.warna }} />
            )}
          </svg>
          {it.nama}
        </span>
      ))}
    </div>
  );
}
