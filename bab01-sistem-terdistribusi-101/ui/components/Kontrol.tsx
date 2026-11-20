"use client";

import { useState } from "react";
import { NODE_WARNA, STATUS_INFO, type Fault, type NodeState, type Snapshot, kirim } from "@/lib/types";

const NORMAL: Fault = { mode: "normal", latency_ms: 0, jitter_ms: 0, drop_request_pct: 0, drop_response_pct: 0, skew_ms: 0 };

const PRESET: { nama: string; judul: string; f: Partial<Fault> }[] = [
  { nama: "Lambat", judul: "Tambah latency 400 ms (fallacy 2: latency itu nol)", f: { latency_ms: 400 } },
  { nama: "Jitter", judul: "Latency 50 ms + acak 0–600 ms", f: { latency_ms: 50, jitter_ms: 600 } },
  { nama: "Drop req 30%", judul: "30% request diputus sebelum diproses (fallacy 1: jaringan andal)", f: { drop_request_pct: 30 } },
  { nama: "Drop resp 30%", judul: "30% request diproses tetapi jawabannya hilang (kasus c)", f: { drop_response_pct: 30 } },
  { nama: "Hang", judul: "Menerima koneksi tetapi tidak pernah menjawab", f: { mode: "hang" } },
  { nama: "Mati", judul: "Port ditutup: connection refused", f: { mode: "mati" } },
  { nama: "Jam +5 s", judul: "Geser jam node 5 detik ke depan (tidak ada jam bersama)", f: { skew_ms: 5000 } },
];

function ringkasFault(f: Fault | null) {
  if (!f) return "admin tidak menjawab (restart?)";
  const bag: string[] = [];
  if (f.mode !== "normal") bag.push(`mode ${f.mode}`);
  if (f.latency_ms) bag.push(`+${f.latency_ms} ms`);
  if (f.jitter_ms) bag.push(`jitter ${f.jitter_ms} ms`);
  if (f.drop_request_pct) bag.push(`drop req ${f.drop_request_pct}%`);
  if (f.drop_response_pct) bag.push(`drop resp ${f.drop_response_pct}%`);
  if (f.skew_ms) bag.push(`jam ${f.skew_ms > 0 ? "+" : ""}${f.skew_ms} ms`);
  return bag.length ? bag.join(", ") : "normal";
}

export function KontrolNode({ n, onPesan }: { n: NodeState; onPesan: (m: string) => void }) {
  const [buka, setBuka] = useState(false);
  const [form, setForm] = useState<Fault>(NORMAL);
  const st = STATUS_INFO[n.status];
  const terapkan = async (f: Fault) => {
    try {
      await kirim(`fault/${n.nama}`, f);
      onPesan(`${n.nama}: ${ringkasFault(f)}`);
    } catch (e) {
      onPesan(`${n.nama}: ${(e as Error).message}`);
    }
  };
  const dasar = n.fault ?? NORMAL;
  return (
    <div className="kartu-node" style={{ borderTopColor: NODE_WARNA[n.nama] }}>
      <div className="kartu-node-judul">
        <b>{n.nama}</b>
        <span className="status" style={{ color: "var(--text-primary)" }}>
          <span style={{ color: st.warna }} aria-hidden>{st.ikon}</span> {st.label}
        </span>
      </div>
      <div className="kecil muted">{ringkasFault(n.fault)}</div>
      <div className="tombol-baris">
        <button onClick={() => terapkan(NORMAL)} title="Hapus semua gangguan">Pulihkan</button>
        {PRESET.map((p) => (
          <button key={p.nama} title={p.judul} onClick={() => terapkan({ ...dasar, ...p.f, mode: p.f.mode ?? "normal" })}>
            {p.nama}
          </button>
        ))}
        <button
          className="bahaya"
          title="Proses keluar (exit 1); Docker menyalakannya lagi. Amati waktu pulih (MTTR)."
          onClick={async () => {
            try {
              await kirim(`crash/${n.nama}`);
              onPesan(`${n.nama}: crash, menunggu Docker restart`);
            } catch (e) {
              onPesan(`${n.nama}: ${(e as Error).message}`);
            }
          }}
        >
          Crash
        </button>
      </div>
      <button className="tautan" onClick={() => { setForm(dasar); setBuka(!buka); }}>{buka ? "Tutup atur manual" : "Atur manual…"}</button>
      {buka && (
        <form
          className="form-fault"
          onSubmit={(e) => {
            e.preventDefault();
            terapkan(form);
          }}
        >
          <label>
            mode
            <select value={form.mode} onChange={(e) => setForm({ ...form, mode: e.target.value as Fault["mode"] })}>
              <option value="normal">normal</option>
              <option value="hang">hang</option>
              <option value="mati">mati</option>
            </select>
          </label>
          {(["latency_ms", "jitter_ms", "drop_request_pct", "drop_response_pct", "skew_ms"] as const).map((k) => (
            <label key={k}>
              {k}
              <input type="number" value={form[k]} onChange={(e) => setForm({ ...form, [k]: Number(e.target.value) })} />
            </label>
          ))}
          <button type="submit">Terapkan</button>
        </form>
      )}
    </div>
  );
}

export function KontrolLaluLintas({ s, onPesan }: { s: Snapshot; onPesan: (m: string) => void }) {
  const ll = s.lalu_lintas;
  const atur = async (ubah: Partial<Snapshot["lalu_lintas"]>) => {
    try {
      await kirim("lalu-lintas", { ...ll, ...ubah });
    } catch (e) {
      onPesan((e as Error).message);
    }
  };
  return (
    <div className="kontrol-ll">
      <button onClick={() => atur({ aktif: !ll.aktif })}>{ll.aktif ? "⏸ Hentikan lalu lintas" : "▶ Jalankan lalu lintas"}</button>
      <label>
        laju <b>{ll.rps}</b> req/s
        <input type="range" min={1} max={50} value={ll.rps} onChange={(e) => atur({ rps: Number(e.target.value) })} />
      </label>
      <fieldset>
        <legend>metode</legend>
        <label>
          <input type="radio" checked={ll.metode === "baca"} onChange={() => atur({ metode: "baca" })} /> baca (GET, idempotent)
        </label>
        <label>
          <input type="radio" checked={ll.metode === "tulis"} onChange={() => atur({ metode: "tulis" })} /> tulis (POST, tidak di-retry)
        </label>
      </fieldset>
      <button
        onClick={async () => {
          const r = await kirim("reset").catch((e) => ({ error: (e as Error).message }));
          onPesan(r.error ? r.error : "statistik dan tabel kunjungan dikosongkan");
        }}
      >
        Reset statistik + data
      </button>
    </div>
  );
}
