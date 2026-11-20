"use client";

import { useEffect, useState } from "react";
import type { Snapshot } from "@/lib/types";
import Topologi from "./Topologi";
import { GrafikDistribusi, GrafikLatency } from "./Grafik";
import { KontrolLaluLintas, KontrolNode } from "./Kontrol";
import { PanelAvailability, PanelFallacies, PanelJam, PanelPartial } from "./Panel";
import Ukur from "./Ukur";

export default function Dashboard() {
  const [s, setS] = useState<Snapshot | null>(null);
  const [tersambung, setTersambung] = useState(false);
  const [pesan, setPesan] = useState<string[]>([]);

  useEffect(() => {
    // EventSource otomatis menyambung ulang bila koneksi SSE putus.
    const es = new EventSource("/api/stream");
    es.onmessage = (e) => {
      setS(JSON.parse(e.data));
      setTersambung(true);
    };
    es.onerror = () => setTersambung(false);
    return () => es.close();
  }, []);

  const onPesan = (m: string) =>
    setPesan((p) => [`${new Date().toLocaleTimeString("id-ID", { hour12: false })} ${m}`, ...p].slice(0, 6));

  return (
    <main>
      <header className="kepala">
        <div>
          <h1>Cluster Visualizer</h1>
          <p className="muted">
            Bab 1 Sistem Terdistribusi 101 · slide P01 · tiga node identik di belakang nginx, satu PostgreSQL bersama
          </p>
        </div>
        <span className={tersambung ? "sambung ok" : "sambung"} role="status">
          {tersambung ? "● SSE tersambung" : "○ menyambung ke monitor…"}
        </span>
      </header>

      {!s ? (
        <p>Menunggu data pertama dari monitor…</p>
      ) : (
        <>
          <div className="grid-2">
            <section className="kartu">
              <h2>Topologi (real-time)</h2>
              <Topologi s={s} />
              <KontrolLaluLintas s={s} onPesan={onPesan} />
            </section>
            <section className="kartu">
              <h2>Suntik kegagalan</h2>
              <div className="node-grid">
                {s.nodes.map((n) => (
                  <KontrolNode key={n.nama} n={n} onPesan={onPesan} />
                ))}
              </div>
              <ul className="log-pesan" aria-live="polite">
                {pesan.map((m, i) => (
                  <li key={i}>{m}</li>
                ))}
              </ul>
            </section>
          </div>

          <div className="grid-2">
            <section className="kartu">
              <h2>Request per detik per node (dijawab lewat nginx)</h2>
              <GrafikDistribusi seri={s.seri} />
            </section>
            <section className="kartu">
              <h2>Latency dilihat klien</h2>
              <GrafikLatency seri={s.seri} />
            </section>
          </div>

          <div className="grid-3">
            <section className="kartu">
              <h2>Partial failure: klien vs database</h2>
              <PanelPartial s={s} />
            </section>
            <section className="kartu">
              <h2>Matematika redundansi</h2>
              <PanelAvailability s={s} />
            </section>
            <section className="kartu">
              <h2>Tidak ada jam bersama</h2>
              <PanelJam s={s} />
            </section>
          </div>

          <div className="grid-2">
            <section className="kartu">
              <h2>Mengukur Sendiri</h2>
              <Ukur />
            </section>
            <section className="kartu">
              <h2>Delapan fallacies of distributed computing</h2>
              <PanelFallacies />
              <h2 className="sub">Monolith vs microservices (Tabel Bab 1)</h2>
              <table className="tabel">
                <thead>
                  <tr><th></th><th>Monolith</th><th>Layanan terdistribusi</th></tr>
                </thead>
                <tbody>
                  <tr><td>panggilan antar modul</td><td>fungsi: cepat, pasti</td><td>jaringan: lambat, bisa gagal (lihat Mengukur Sendiri 3)</td></tr>
                  <tr><td>failure</td><td>satu bug menjatuhkan semua</td><td>bisa diisolasi: matikan satu node, layanan tetap jalan</td></tr>
                  <tr><td>skala</td><td>seluruh aplikasi</td><td>tambah replica di balik load balancer</td></tr>
                  <tr><td>operasi</td><td>sederhana</td><td>butuh monitor, heartbeat, log terpusat (Grafana)</td></tr>
                </tbody>
              </table>
            </section>
          </div>
        </>
      )}
    </main>
  );
}
