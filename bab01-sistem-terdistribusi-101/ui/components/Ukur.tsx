"use client";

import { useState } from "react";
import { angka, kirim } from "@/lib/types";

type Ringkas = { sampel: number; gagal: number; min_ms: number; p50_ms: number; p95_ms: number; maks_ms: number };
type HasilRTT = { sampel_per_target: number; hasil: { nama: string; tcp_connect: Ringkas; http_ping: Ringkas }[] };
type HasilTP = {
  durasi_s: number; konkurensi: number; metode: string; sukses: number; gagal: number; rps: number;
  latency: Ringkas; p99_ms: number; per_node: Record<string, number>;
};
type HasilBanding = { n: number; hasil: { nama: string; total_ms: number; per_call_us: number; jumlah: number; error?: string }[] };

const ms = (r: Ringkas, d = 3) => (r.sampel > 0 ? `${angka(r.p50_ms, d)} ms` : "–");

function fmtUs(us: number) {
  if (us < 1) return `${angka(us * 1000, 0)} ns`;
  if (us < 1000) return `${angka(us, 1)} µs`;
  return `${angka(us / 1000, 2)} ms`;
}

// Panel "Mengukur Sendiri" (subbab terakhir Bab 1): setiap klaim harus disertai angka.
export default function Ukur() {
  const [sibuk, setSibuk] = useState<string | null>(null);
  const [galat, setGalat] = useState<string | null>(null);
  const [rtt, setRtt] = useState<HasilRTT | null>(null);
  const [browser, setBrowser] = useState<number[] | null>(null);
  const [tp, setTp] = useState<HasilTP | null>(null);
  const [kon, setKon] = useState(8);
  const [banding, setBanding] = useState<HasilBanding | null>(null);
  const [n, setN] = useState(100);

  const jalankan = async (nama: string, f: () => Promise<void>) => {
    setSibuk(nama);
    setGalat(null);
    try {
      await f();
    } catch (e) {
      setGalat((e as Error).message);
    } finally {
      setSibuk(null);
    }
  };

  const ukurBrowser = async () => {
    const s: number[] = [];
    for (let i = 0; i < 11; i++) {
      const t0 = performance.now();
      await fetch("/api/ping", { cache: "no-store" });
      if (i > 0) s.push(performance.now() - t0);
    }
    setBrowser(s.sort((a, b) => a - b));
  };

  const lokal = banding?.hasil[0]?.per_call_us ?? 0;
  const tcpNode = rtt?.hasil[0]?.tcp_connect.p50_ms;

  return (
    <div className="ukur">
      {galat && <p className="galat">Gagal: {galat}</p>}
      <section>
        <h3>1. RTT di dalam &quot;data center&quot; (jaringan Docker)</h3>
        <div className="tombol-baris">
          <button disabled={!!sibuk} onClick={() => jalankan("rtt", async () => setRtt(await kirim("ukur/rtt")))}>
            {sibuk === "rtt" ? "mengukur…" : "Ukur RTT monitor → node"}
          </button>
          <button disabled={!!sibuk} onClick={() => jalankan("browser", ukurBrowser)}>
            {sibuk === "browser" ? "mengukur…" : "Ukur RTT browser → server"}
          </button>
        </div>
        {rtt && (
          <table className="tabel">
            <thead>
              <tr><th>target</th><th className="kanan">TCP connect p50</th><th className="kanan">HTTP p50</th><th className="kanan">HTTP p95</th><th className="kanan">gagal</th></tr>
            </thead>
            <tbody>
              {rtt.hasil.map((h) => (
                <tr key={h.nama}>
                  <td>{h.nama}</td>
                  <td className="kanan">{ms(h.tcp_connect)}</td>
                  <td className="kanan">{ms(h.http_ping)}</td>
                  <td className="kanan">{h.http_ping.sampel > 0 ? `${angka(h.http_ping.p95_ms, 3)} ms` : "–"}</td>
                  <td className="kanan">{h.tcp_connect.gagal + h.http_ping.gagal}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {browser && (
          <p className="kecil">
            Browser → UI (Next.js) → monitor, 10 sampel: min {angka(browser[0], 2)} ms, p50 {angka(browser[4], 2)} ms, maks{" "}
            {angka(browser[browser.length - 1], 2)} ms. Bandingkan dengan RTT antar-container di atas.
          </p>
        )}
      </section>

      <section>
        <h3>2. Throughput sederhana lewat nginx (closed loop, 3 detik)</h3>
        <div className="tombol-baris">
          <label>
            konkurensi
            <select value={kon} onChange={(e) => setKon(Number(e.target.value))}>
              {[1, 2, 4, 8, 16, 32].map((k) => <option key={k} value={k}>{k}</option>)}
            </select>
          </label>
          <button disabled={!!sibuk} onClick={() => jalankan("tp", async () => setTp(await kirim("ukur/throughput", { durasi_s: 3, konkurensi: kon, metode: "baca" })))}>
            {sibuk === "tp" ? "menguji 3 detik…" : "Uji throughput"}
          </button>
        </div>
        {tp && (
          <p className="kecil">
            <b>{angka(tp.rps, 0)} req/s</b> dengan {tp.konkurensi} worker · sukses {tp.sukses}, gagal {tp.gagal} · p50 {angka(tp.latency.p50_ms, 2)} ms, p95{" "}
            {angka(tp.latency.p95_ms, 2)} ms, p99 {angka(tp.p99_ms, 2)} ms · per node: {Object.entries(tp.per_node).sort().map(([k, v]) => `${k} ${v}`).join(", ")}.
            Naikkan konkurensi: throughput naik sampai titik jenuh, lalu yang naik hanya latency.
          </p>
        )}
      </section>

      <section>
        <h3>3. Monolith vs terdistribusi: fungsi yang sama, dipanggil {n} kali</h3>
        <div className="tombol-baris">
          <label>
            n
            <select value={n} onChange={(e) => setN(Number(e.target.value))}>
              {[10, 100, 1000].map((k) => <option key={k} value={k}>{k}</option>)}
            </select>
          </label>
          <button disabled={!!sibuk} onClick={() => jalankan("banding", async () => setBanding(await kirim("ukur/bandingkan", { n })))}>
            {sibuk === "banding" ? "mengukur…" : "Bandingkan"}
          </button>
        </div>
        {banding && (
          <table className="tabel">
            <thead>
              <tr><th>cara memanggil</th><th className="kanan">total</th><th className="kanan">per panggilan</th><th className="kanan">× lokal</th></tr>
            </thead>
            <tbody>
              {banding.hasil.map((h) => (
                <tr key={h.nama}>
                  <td>{h.nama}{h.error ? ` (error: ${h.error})` : ""}</td>
                  <td className="kanan">{fmtUs(h.total_ms * 1000)}</td>
                  <td className="kanan">{fmtUs(h.per_call_us)}</td>
                  <td className="kanan">{lokal > 0 ? `${Math.round(h.per_call_us / lokal).toLocaleString("id-ID")}×` : "–"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <p className="kecil muted">
          Hasilnya sama persis (kolom jumlah di JSON identik), tetapi panggilan jaringan ribuan kali lebih lambat. Ini contoh
          buku: 100 panggilan fungsi menjadi 100 panggilan jaringan menambah puluhan milidetik per request.
        </p>
      </section>

      <section>
        <h3>4. Latency numbers vs hasil ukur Anda</h3>
        <table className="tabel">
          <thead>
            <tr><th>operasi</th><th className="kanan">kira-kira (Dean &amp; Norvig)</th><th className="kanan">terukur di demo</th></tr>
          </thead>
          <tbody>
            <tr><td>panggilan fungsi kecil (64 iterasi)</td><td className="kanan">puluhan–ratusan ns</td><td className="kanan">{lokal ? fmtUs(lokal) : "jalankan 3"}</td></tr>
            <tr><td>akses main memory (DRAM)</td><td className="kanan">100 ns</td><td className="kanan">–</td></tr>
            <tr><td>round trip dalam satu data center</td><td className="kanan">500 µs</td><td className="kanan">{tcpNode !== undefined ? `${angka(tcpNode * 1000, 0)} µs (TCP connect, satu host)` : "jalankan 1"}</td></tr>
            <tr><td>round trip Jakarta–Eropa–Jakarta</td><td className="kanan">150–200 ms</td><td className="kanan">coba <code>ping www.tudelft.nl</code></td></tr>
          </tbody>
        </table>
        <p className="kecil muted">
          Semua container berada di satu mesin, jadi RTT di sini lebih kecil dari RTT antar-server sungguhan. Coba juga skrip
          buku: <code>ping</code>, <code>traceroute</code>, dan <code>curl -w</code> ke tiga benua.
        </p>
      </section>
    </div>
  );
}
