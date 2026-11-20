"use client";

import { NODE_WARNA, type Snapshot, angka, persen } from "@/lib/types";

// Partial failure: apa yang dilihat klien vs apa yang tercatat di database.
export function PanelPartial({ s }: { s: Snapshot }) {
  const k = s.kumulatif;
  const tanpaKonfirmasi = s.db.ok ? Math.max(0, s.db.total - k.tulis_sukses) : 0;
  const jenis = Object.entries(k.gagal_jenis).sort((a, b) => b[1] - a[1]);
  return (
    <div>
      <div className="angka-baris">
        <Stat label="dikirim" v={k.kirim} />
        <Stat label="sukses" v={k.sukses} />
        <Stat label="gagal di klien" v={k.gagal} />
        <Stat label="sukses setelah retry nginx" v={k.retry} />
      </div>
      {jenis.length > 0 && (
        <p className="kecil">
          Jenis kegagalan: {jenis.map(([j, v]) => `${j} ${v}`).join(" · ")}
        </p>
      )}
      <table className="tabel">
        <thead>
          <tr><th>Tulis (POST /api/catat)</th><th className="kanan">jumlah</th></tr>
        </thead>
        <tbody>
          <tr><td>klien menerima &quot;201 sukses&quot;</td><td className="kanan">{k.tulis_sukses}</td></tr>
          <tr><td>klien menerima error / timeout</td><td className="kanan">{k.tulis_gagal}</td></tr>
          <tr><td>baris tercatat di Postgres</td><td className="kanan">{s.db.ok ? s.db.total : "?"}</td></tr>
          <tr className="sorot-baris"><td>tercatat <b>tanpa</b> konfirmasi ke klien</td><td className="kanan"><b>{tanpaKonfirmasi}</b></td></tr>
        </tbody>
      </table>
      <p className="kecil muted">
        Baris terakhir adalah kasus (c) pada Gambar &quot;tiga penyebab tidak ada jawaban&quot;: server sudah memproses, jawabannya
        yang hilang. Klien yang mengulang request akan mencatat dua kali, kecuali memakai <i>idempotency key</i> (Bab 4).
        Mulai dari Reset, pilih metode tulis, lalu suntik &quot;Drop resp 30%&quot;.
      </p>
    </div>
  );
}

function Stat({ label, v }: { label: string; v: number | string }) {
  return (
    <div className="stat">
      <div className="stat-v">{v}</div>
      <div className="stat-l">{label}</div>
    </div>
  );
}

// Matematika redundansi: A_paralel = 1 - prod(1 - a_i), dibandingkan dengan yang terukur.
export function PanelAvailability({ s }: { s: Snapshot }) {
  const a = s.availability;
  return (
    <div>
      <table className="tabel">
        <thead>
          <tr><th>komponen</th><th className="kanan">a terukur (60 s heartbeat)</th></tr>
        </thead>
        <tbody>
          {s.nodes.map((n) => (
            <tr key={n.nama}>
              <td><span className="kotak-warna" style={{ background: NODE_WARNA[n.nama] }} /> {n.nama}</td>
              <td className="kanan">{persen(a.per_node[n.nama] ?? 0)}</td>
            </tr>
          ))}
          <tr><td>nginx</td><td className="kanan">{persen(a.lb)}</td></tr>
          <tr><td>prediksi 3 node paralel: 1 − ∏(1 − aᵢ)</td><td className="kanan">{persen(a.prediksi_paralel, 3)}</td></tr>
          <tr><td>prediksi jalur seri: a<sub>nginx</sub> × A<sub>paralel</sub></td><td className="kanan">{persen(a.prediksi_jalur, 3)}</td></tr>
          <tr className="sorot-baris"><td>terukur dari klien (sukses / kirim, 60 s)</td><td className="kanan"><b>{persen(a.terukur_sistem, 2)}</b></td></tr>
        </tbody>
      </table>
      <p className="kecil muted">
        Rumus paralel hanya berlaku bila kegagalan saling bebas. Matikan dua node: prediksi tetap tinggi selama satu
        node hidup. Suntik &quot;Hang&quot; ke satu node: heartbeat node itu gagal, tetapi angka terukur klien turun lebih
        sedikit karena nginx mengulang GET ke node lain (failure transparency). Tombol Crash memperlihatkan MTTR: waktu
        dari mati sampai hidup lagi.
      </p>
    </div>
  );
}

export function PanelJam({ s }: { s: Snapshot }) {
  return (
    <div>
      <table className="tabel">
        <thead>
          <tr><th>node</th><th className="kanan">offset jam terhadap monitor</th></tr>
        </thead>
        <tbody>
          {s.nodes.map((n) => (
            <tr key={n.nama}>
              <td><span className="kotak-warna" style={{ background: NODE_WARNA[n.nama] }} /> {n.nama}</td>
              <td className="kanan">{n.rtt_ms > 0 ? `${n.offset_ms > 0 ? "+" : ""}${angka(n.offset_ms, 0)} ms` : "–"}</td>
            </tr>
          ))}
          <tr className="sorot-baris">
            <td>pasangan baris (urut id) yang jam node-nya mundur</td>
            <td className="kanan"><b>{s.db.ok ? s.db.urutan_jam_mundur : "?"}</b></td>
          </tr>
        </tbody>
      </table>
      <p className="kecil muted">
        Offset diperkirakan dengan cara Cristian: jam node − (waktu kirim + RTT/2). Geser jam satu node (&quot;Jam +5 s&quot;),
        pilih metode tulis, lalu pulihkan: baris yang ditulis belakangan oleh node lain tampak terjadi lebih dulu.
        Urutan tidak bisa dibaca dari timestamp; inilah alasan logical clock (Lamport).
      </p>
    </div>
  );
}

const FALLACY: [string, string][] = [
  ["Jaringan itu andal", "Drop req / Drop resp / Hang: request hilang, timeout di nginx dan di klien."],
  ["Latency itu nol", "Lambat / Jitter: p95 naik di grafik latency; Monolith vs terdistribusi."],
  ["Bandwidth tidak terbatas", "Uji throughput: req/s punya batas, latency naik saat konkurensi naik."],
  ["Jaringan itu aman", "Semua lalu lintas demo plaintext HTTP (TLS di Bab 3)."],
  ["Topologi tidak berubah", "Mati / Crash: node hilang dari pool, nginx mengeluarkannya (passive health check)."],
  ["Hanya ada satu administrator", "Port admin tiap node terpisah; siapa pun yang memegangnya bisa mengubah perilaku node."],
  ["Transport cost itu nol", "Setiap hop (klien → nginx → node → DB) menambah waktu; bandingkan RTT langsung vs lewat nginx."],
  ["Jaringan itu homogen", "Node lambat dan node cepat dilayani round robin yang sama; satu node lambat menaikkan p95 semua."],
];

export function PanelFallacies() {
  return (
    <ol className="fallacy">
      {FALLACY.map(([f, d]) => (
        <li key={f}>
          <b>{f}.</b> <span className="muted">{d}</span>
        </li>
      ))}
    </ol>
  );
}
