"use client";
// Socket API dan packet: timeline satu koneksi + tcpdump di kedua sisi.
import { useEffect, useState } from "react";
import NetemPanel from "@/components/NetemPanel";
import Ladder from "@/components/Ladder";
import { api, fmt, TraceResult } from "@/lib/api";

const THEORY: Record<string, string> = {
  tcp: "TCP: connect() memicu three-way handshake (SYN, SYN-ACK, ACK). Server baru mendapat socket dari accept() setelah handshake selesai. close() mengirim FIN; recv() = 0 di sisi lain berarti FIN tiba. Data yang hilang dikirim ulang (retransmission, panah oranye).",
  udp: "UDP: tidak ada handshake. connect() pada socket UDP hanya mencatat alamat tujuan. Setiap sendto() menjadi tepat satu datagram; bila hilang, tidak ada yang mengirim ulang, dan klien hanya tahu lewat timeout.",
  quic: "QUIC: packet Initial membawa ClientHello TLS 1.3; handshake transport dan encryption selesai dalam 1 RTT. Stream dibuka tanpa handshake tambahan. Semua isi packet 1-RTT terenkripsi, jadi tcpdump hanya melihat UDP dan header QUIC.",
};

export default function SocketPage() {
  const [proto, setProto] = useState("tcp");
  const [messages, setMessages] = useState(3);
  const [burst, setBurst] = useState(false);
  const [res, setRes] = useState<TraceResult | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [ss, setSs] = useState("");

  // ?auto=1 langsung menjalankan uji saat halaman dibuka (berguna di kelas dan untuk tangkapan layar)
  useEffect(() => {
    if (new URLSearchParams(window.location.search).get("auto") === "1") start();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function start() {
    setBusy(true);
    setErr("");
    try {
      setRes(await api<TraceResult>("trace", { proto, messages, burst }));
    } catch (e) {
      setErr(String((e as Error).message));
    } finally {
      setBusy(false);
    }
  }

  async function loadSS() {
    try {
      const r = await api<{ cmd: string; out: string }>("ss");
      setSs(`$ ${r.cmd}\n${r.out}`);
    } catch (e) {
      setSs(String((e as Error).message));
    }
  }

  const flows = res?.flows ?? [];
  const t0 = Math.min(
    ...flows.flatMap((f) => [f.send_ts ?? Infinity, f.recv_ts ?? Infinity]),
    ...(res?.events ?? []).map((e) => e.ts),
  );

  return (
    <>
      <section className="card">
        <h1>Socket API dan mengamati packet</h1>
        <p className="muted">
          Prober membuka satu koneksi, mengirim beberapa message, lalu menutupnya. Setiap panggilan socket API
          (socket, connect, accept, send, recv, close) dicatat di kedua sisi, dan <code>tcpdump</code> berjalan di
          klien dan di server. Karena router netem ada di tengah, packet yang dibuang terlihat di tangkapan pengirim
          tetapi tidak pernah tiba di penerima (Bab 2: Socket API, Three-way handshake, Mengamati Packet).
        </p>
      </section>
      <NetemPanel />
      <section className="card">
        <h2>Rekam satu koneksi</h2>
        <div className="row">
          <label>
            protokol
            <select value={proto} onChange={(e) => setProto(e.target.value)}>
              <option value="tcp">TCP (port 9000)</option>
              <option value="udp">UDP (port 9001)</option>
              <option value="quic">QUIC (port 9002/udp)</option>
            </select>
          </label>
          <label>
            message <input type="number" min={1} max={5} value={messages} onChange={(e) => setMessages(Number(e.target.value))} />
          </label>
          {proto === "tcp" && (
            <label title="dua send() berturut-turut tanpa menunggu balasan: lihat apakah server menerimanya dalam satu recv()">
              <input type="checkbox" checked={burst} onChange={(e) => setBurst(e.target.checked)} /> send() beruntun (uji framing)
            </label>
          )}
          <button onClick={start} disabled={busy}>
            {busy ? "Merekam..." : "Rekam"}
          </button>
        </div>
        <p className="theory">{THEORY[proto]}</p>
        {err && <p className="error">{err}</p>}
      </section>

      {res && (
        <section className="card">
          <h2>
            Timeline {res.proto.toUpperCase()} {res.client} → {res.server}
          </h2>
          <div className="row">
            <span className="pill">kondisi: {res.netem_text}</span>
            <span>packet: {flows.length}</span>
            <span style={{ color: res.lost_packets ? "#dc2626" : undefined }}>hilang di jalan: {res.lost_packets}</span>
            <span style={{ color: res.retrans_packets ? "#ea580c" : undefined }}>retransmission: {res.retrans_packets}</span>
            {res.error && <span className="error">klien: {res.error}</span>}
          </div>
          <p className="muted">
            Titik biru = panggilan socket API di klien, titik hijau = di server. Panah = packet dari tangkapan tcpdump
            (kemiringan = waktu perjalanan). Arahkan kursor ke label untuk melihat baris tcpdump lengkap.
          </p>
          <Ladder events={res.events ?? []} flows={flows} />
          <h3>Daftar packet</h3>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>#</th>
                  <th>arah</th>
                  <th>jenis</th>
                  <th className="num">len</th>
                  <th>seq / ack</th>
                  <th className="num">dikirim (ms)</th>
                  <th className="num">tiba (ms)</th>
                  <th className="num">perjalanan</th>
                </tr>
              </thead>
              <tbody>
                {flows.map((f, i) => (
                  <tr key={i} style={{ color: !f.recv_ts || !f.send_ts ? "#dc2626" : f.retrans ? "#ea580c" : undefined }}>
                    <td>{i + 1}</td>
                    <td>{f.dir === "c2s" ? "klien → server" : "server → klien"}</td>
                    <td>
                      {f.kind}
                      {f.flags ? ` [${f.flags}]` : ""}
                      {f.retrans ? " retransmission" : ""}
                    </td>
                    <td className="num">{f.len}</td>
                    <td className="muted">
                      {f.seq ? `seq ${f.seq}` : ""} {f.ack ? `ack ${f.ack}` : ""}
                    </td>
                    <td className="num">{f.send_ts ? fmt((f.send_ts - t0) * 1000, 2) : "-"}</td>
                    <td className="num">{f.recv_ts ? fmt((f.recv_ts - t0) * 1000, 2) : "hilang"}</td>
                    <td className="num">{f.send_ts && f.recv_ts ? `${fmt((f.recv_ts - f.send_ts) * 1000, 2)} ms` : "-"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <details>
            <summary>Keluaran tcpdump mentah (klien dan server)</summary>
            <pre>{`$ ${res.tcpdump_cmd}   # di container prober\n${(res.client_tcpdump ?? []).join("\n")}`}</pre>
            <pre>{`# di container echo (sisi server)\n${(res.server_tcpdump ?? []).join("\n")}`}</pre>
          </details>
        </section>
      )}

      <section className="card">
        <h2>ss -tni di server</h2>
        <p className="muted">
          Menampilkan rtt, cwnd, dan retrans per koneksi TCP yang sedang terbuka di container echo. Klik saat uji di
          halaman Perbandingan transport atau Head-of-line blocking sedang berjalan (buka di tab lain).
        </p>
        <button className="ghost" onClick={loadSS}>
          Ambil ss -tni
        </button>
        {ss && <pre>{ss}</pre>}
      </section>
    </>
  );
}
