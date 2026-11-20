"use client";
import { useEffect, useState } from "react";

type Percobaan = { ke: number; mulai_ms: number; durasi_ms: number; hasil: string; status: string; dari_cache: boolean; tunggu_ms: number };
type Hasil = {
  protokol: string;
  kunci: string;
  berhasil: boolean;
  percobaan: Percobaan[];
  total_ms: number;
  saldo_sebelum: number;
  saldo_sesudah: number;
  dipotong_kali: number;
  jumlah: number;
  error?: string;
};
type Run = Hasil & { label: string; idempotency: boolean; timeout_ms: number };

const warnaHasil: Record<string, string> = { ok: "var(--good)", timeout: "var(--warn)", gagal: "var(--bad)", ditolak: "var(--bad)" };
const rp = (n: number) => "Rp" + n.toLocaleString("id-ID");

function Timeline({ run }: { run: Run }) {
  const total = Math.max(run.total_ms, ...run.percobaan.map((p) => p.mulai_ms + Math.max(p.durasi_ms, run.timeout_ms))) * 1.05;
  const W = 640;
  const kiri = 90;
  const plot = W - kiri - 10;
  const x = (ms: number) => kiri + (ms / total) * plot;
  const H = run.percobaan.length * 24 + 30;
  const ticks = Array.from({ length: 6 }, (_, i) => (i * total) / 5);
  return (
    <svg viewBox={`0 0 ${W} ${H}`} width="100%" style={{ maxWidth: 820, display: "block" }} role="img" aria-label={`Timeline percobaan ${run.label}`}>
      {ticks.map((t, i) => (
        <g key={i}>
          <line x1={x(t)} x2={x(t)} y1={0} y2={H - 18} stroke="var(--grid)" />
          <text x={x(t)} y={H - 4} fontSize={10} textAnchor="middle" fill="var(--muted)">{t.toFixed(0)} ms</text>
        </g>
      ))}
      {run.percobaan.map((p, i) => {
        const y = i * 24 + 4;
        const w = Math.max(3, x(p.mulai_ms + p.durasi_ms) - x(p.mulai_ms));
        return (
          <g key={p.ke}>
            <line x1={x(p.mulai_ms + run.timeout_ms)} x2={x(p.mulai_ms + run.timeout_ms)} y1={y - 2} y2={y + 18} stroke="var(--warn)" strokeDasharray="3 2" strokeWidth={1.5} />
            <text x={kiri - 8} y={y + 12} fontSize={11} textAnchor="end" fill="var(--text-2)">percobaan {p.ke}</text>
            <rect x={x(p.mulai_ms)} y={y} width={w} height={16} rx={4} fill={warnaHasil[p.hasil] ?? "var(--muted)"}>
              <title>{`#${p.ke}: ${p.hasil} (${p.status}) ${p.durasi_ms.toFixed(1)} ms${p.dari_cache ? ", hasil dari cache idempotency" : ""}`}</title>
            </rect>
            {p.tunggu_ms > 0 && (
              <line x1={x(p.mulai_ms + p.durasi_ms)} x2={x(p.mulai_ms + p.durasi_ms + p.tunggu_ms)} y1={y + 8} y2={y + 8} stroke="var(--muted)" strokeDasharray="2 3" strokeWidth={2}>
                <title>{`backoff ${p.tunggu_ms} ms`}</title>
              </line>
            )}
            <text x={x(p.mulai_ms) + w + 4} y={y + 12} fontSize={10.5} fill="var(--text)">
              {p.hasil}{p.dari_cache ? " (replay)" : ""}{p.tunggu_ms > 0 ? ` · tunggu ${p.tunggu_ms} ms` : ""}
            </text>
          </g>
        );
      })}
    </svg>
  );
}

export default function AndalPage() {
  const [f, setF] = useState({
    protokol: "rest",
    timeout_ms: 400,
    maks_percobaan: 4,
    backoff_ms: 100,
    jitter: true,
    idempotency: false,
    jumlah: 25000,
    lambat_ms: 800,
    lambat_kali: 2,
    gagal_persen: 0,
  });
  const [runs, setRuns] = useState<Run[]>([]);
  const [sibuk, setSibuk] = useState(false);
  const [saldo, setSaldo] = useState<{ saldo: number; jumlah_pembayaran: number } | null>(null);

  const muatSaldo = () => fetch("/api/saldo").then((r) => r.json()).then(setSaldo).catch(() => {});
  useEffect(() => {
    muatSaldo();
    // ?jalankan=bandingkan: langsung bandingkan tanpa vs dengan kunci
    if (new URLSearchParams(location.search).get("jalankan") === "bandingkan") bandingkan();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function satu(idempotency: boolean, label: string): Promise<Run> {
    const r = await fetch("/api/andal", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ ...f, idempotency }),
    });
    const j = (await r.json()) as Hasil;
    return { ...j, label, idempotency, timeout_ms: f.timeout_ms };
  }

  async function jalankan() {
    setSibuk(true);
    const run = await satu(f.idempotency, `${f.protokol.toUpperCase()} ${f.idempotency ? "dengan" : "tanpa"} idempotency key`);
    setRuns((r) => [run, ...r].slice(0, 6));
    await muatSaldo();
    setSibuk(false);
  }

  async function bandingkan() {
    setSibuk(true);
    await fetch("/api/saldo/reset", { method: "POST", body: "{}" });
    const a = await satu(false, `${f.protokol.toUpperCase()} tanpa idempotency key`);
    const b = await satu(true, `${f.protokol.toUpperCase()} dengan idempotency key`);
    setRuns((r) => [b, a, ...r].slice(0, 6));
    await muatSaldo();
    setSibuk(false);
  }

  async function reset() {
    await fetch("/api/saldo/reset", { method: "POST", body: "{}" });
    await fetch("/api/gangguan", { method: "PUT", headers: { "content-type": "application/json" }, body: JSON.stringify({ lambat_ms: 0, lambat_kali: 0, gagal_persen: 0 }) });
    setRuns([]);
    muatSaldo();
  }

  const num = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement>) => setF({ ...f, [k]: Number(e.target.value) });

  return (
    <>
      <h1>Panggilan yang andal: timeout, retry, idempotency</h1>
      <p className="lead">
        Klien membayar Rp25.000 dari akun <b>budi</b> lewat <code>POST /v1/bayar</code> (REST) atau <code>Bayar</code> (gRPC).
        Server dibuat lambat <b>sesudah</b> pembayaran di-commit: pekerjaan selesai, tetapi response tidak sampai sebelum timeout
        (kasus 3 di subbab semantik pemanggilan). Klien lalu retry dengan exponential backoff.
      </p>
      <div className="teori">
        Tanpa idempotency key, retry seperti ini adalah <b>at-least-once</b>: saldo terpotong beberapa kali. Dengan
        <code> Idempotency-Key</code> yang sama di setiap retry, server menyimpan kunci dan hasil dalam satu transaksi
        (unique constraint) dan mengembalikan hasil lama tanpa memproses ulang, sehingga efeknya <b>exactly-once</b>.
      </div>

      <div className="kartu" style={{ marginBottom: 14 }}>
        <div className="kontrol" style={{ marginBottom: 8 }}>
          <label className="f">
            Protokol
            <select value={f.protokol} onChange={(e) => setF({ ...f, protokol: e.target.value })}>
              <option value="rest">REST (header Idempotency-Key)</option>
              <option value="grpc">gRPC (deadline + metadata idempotency-key)</option>
            </select>
          </label>
          <label className="f">Timeout klien (ms)<input type="number" value={f.timeout_ms} onChange={num("timeout_ms")} /></label>
          <label className="f">Maks percobaan<input type="number" min={1} max={8} value={f.maks_percobaan} onChange={num("maks_percobaan")} /></label>
          <label className="f">Backoff dasar (ms)<input type="number" value={f.backoff_ms} onChange={num("backoff_ms")} /></label>
          <label className="f" style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
            <input type="checkbox" checked={f.jitter} onChange={(e) => setF({ ...f, jitter: e.target.checked })} /> full jitter
          </label>
          <label className="f" style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
            <input type="checkbox" checked={f.idempotency} onChange={(e) => setF({ ...f, idempotency: e.target.checked })} /> pakai idempotency key
          </label>
        </div>
        <div className="kontrol" style={{ marginBottom: 8 }}>
          <b className="kecil" style={{ alignSelf: "center" }}>Gangguan di server:</b>
          <label className="f">Lambat sesudah commit (ms)<input type="number" value={f.lambat_ms} onChange={num("lambat_ms")} /></label>
          <label className="f">Untuk N request berikutnya (0 = semua)<input type="number" min={0} value={f.lambat_kali} onChange={num("lambat_kali")} /></label>
          <label className="f">Gagal 503 sebelum proses (%)<input type="number" min={0} max={100} value={f.gagal_persen} onChange={num("gagal_persen")} /></label>
        </div>
        <div className="baris">
          <button onClick={jalankan} disabled={sibuk}>Jalankan sekali</button>
          <button onClick={bandingkan} disabled={sibuk}>Bandingkan tanpa vs dengan kunci</button>
          <button className="sekunder" onClick={reset} disabled={sibuk}>Reset saldo dan gangguan</button>
          {saldo && (
            <span className="kecil">
              Saldo budi sekarang: <b>{rp(saldo.saldo)}</b> · {saldo.jumlah_pembayaran} pembayaran tercatat
            </span>
          )}
        </div>
      </div>

      {runs.map((r, i) => (
        <div className="kartu" key={i} style={{ marginBottom: 12 }}>
          <div className="baris" style={{ justifyContent: "space-between" }}>
            <h3>{r.label}</h3>
            <span className="kecil">{r.kunci ? `Idempotency-Key: ${r.kunci}` : "tanpa kunci"}</span>
          </div>
          {r.error ? (
            <p className="gagal">{r.error}</p>
          ) : (
            <>
              <div className="baris" style={{ gap: 26, marginBottom: 8 }}>
                <div className="stat"><b className={r.berhasil ? "ok" : "gagal"}>{r.berhasil ? "berhasil" : "gagal"}</b><span>hasil akhir di klien</span></div>
                <div className="stat"><b>{r.percobaan.length}</b><span>percobaan</span></div>
                <div className="stat">
                  <b className={r.dipotong_kali > 1 ? "gagal" : "ok"}>{r.dipotong_kali}×</b>
                  <span>saldo benar-benar dipotong</span>
                </div>
                <div className="stat"><b>{rp(r.saldo_sebelum - r.saldo_sesudah)}</b><span>total terpotong</span></div>
                <div className="stat"><b>{r.total_ms.toFixed(0)} ms</b><span>waktu total</span></div>
              </div>
              <Timeline run={r} />
              <p className="kecil" style={{ marginBottom: 0 }}>
                Garis putus kuning: batas timeout setiap percobaan. Hijau = ok, kuning = timeout, merah = gagal/ditolak, titik-titik = jeda backoff.
              </p>
            </>
          )}
        </div>
      ))}
    </>
  );
}
