"use client";
import { useEffect, useRef, useState } from "react";

type Perubahan = { id: number; nama: string; harga: number; waktu_ms: number; oleh?: string };
type Panel = { harga: number | null; request: number; update: number; telat: number | null; node: string; status: string };
type Chat = { nama: string; teks: string; node: string; node_penerima: string; waktu_ms: number; jenis: string };

const kosong: Panel = { harga: null, request: 0, update: 0, telat: null, node: "-", status: "menyambung" };

function useGateway() {
  const [gw, setGw] = useState<string | null>(null);
  useEffect(() => {
    fetch("/api/config")
      .then((r) => r.json())
      .then((c) => setGw(`${location.protocol}//${location.hostname}:${c.gatewayPort}`))
      .catch(() => setGw(`${location.protocol}//${location.hostname}:50401`));
  }, []);
  return gw;
}

export default function RealtimePage() {
  const gw = useGateway();
  const [poll, setPoll] = useState<Panel>(kosong);
  const [lp, setLp] = useState<Panel>(kosong);
  const [sse, setSse] = useState<Panel>(kosong);
  const [gs, setGs] = useState<Panel>(kosong);
  const [log, setLog] = useState<string[]>([]);
  const [harga, setHarga] = useState(25000);
  const [protokol, setProtokol] = useState("rest");
  const [ticker, setTicker] = useState(false);

  const tambahLog = (s: string) => setLog((l) => [`${new Date().toLocaleTimeString("id-ID")} ${s}`, ...l].slice(0, 60));

  // 1. Polling: tanya setiap 2 detik, ada perubahan atau tidak.
  useEffect(() => {
    if (!gw) return;
    let terakhir: number | null = null;
    const t = setInterval(async () => {
      try {
        const r = await fetch(`${gw}/v1/produk/42`, { cache: "no-store" });
        const p = await r.json();
        setPoll((s) => {
          const berubah = terakhir !== null && p.harga !== terakhir;
          terakhir = p.harga;
          return { ...s, harga: p.harga, request: s.request + 1, update: s.update + (berubah ? 1 : 0), node: r.headers.get("x-node") ?? "-", status: "aktif" };
        });
      } catch {
        setPoll((s) => ({ ...s, status: "gagal" }));
      }
    }, 2000);
    return () => clearInterval(t);
  }, [gw]);

  // 2. Long polling: server menahan request sampai ada perubahan (maks 25 detik).
  useEffect(() => {
    if (!gw) return;
    let hidup = true;
    const ctrl = new AbortController();
    (async () => {
      let setelah = 0;
      while (hidup) {
        try {
          const r = await fetch(`${gw}/v1/harga/tunggu?id=42&setelah=${setelah}`, { cache: "no-store", signal: ctrl.signal });
          setLp((s) => ({ ...s, request: s.request + 1, status: "menunggu", node: r.headers.get("x-node") ?? s.node }));
          if (r.status === 204) continue;
          const j = await r.json();
          const daftar: Perubahan[] = j.perubahan ?? [];
          if (setelah === 0) {
            setelah = j.sekarang_ms;
            continue;
          }
          if (daftar.length) {
            const akhir = daftar[daftar.length - 1];
            setelah = akhir.waktu_ms;
            setLp((s) => ({ ...s, harga: akhir.harga, update: s.update + daftar.length, telat: Date.now() - akhir.waktu_ms }));
          }
        } catch {
          if (!hidup) return;
          setLp((s) => ({ ...s, status: "gagal, coba lagi" }));
          await new Promise((res) => setTimeout(res, 2000));
        }
      }
    })();
    return () => {
      hidup = false;
      ctrl.abort();
    };
  }, [gw]);

  // 3. SSE: satu response HTTP panjang; browser reconnect otomatis.
  useEffect(() => {
    if (!gw) return;
    const es = new EventSource(`${gw}/v1/harga/stream?id=42,1,2,3`);
    es.addEventListener("halo", (e) => {
      const d = JSON.parse((e as MessageEvent).data);
      setSse((s) => ({ ...s, request: s.request + 1, node: d.node, status: "tersambung" }));
    });
    es.addEventListener("harga", (e) => {
      const d = JSON.parse((e as MessageEvent).data);
      const p: Perubahan = d.perubahan;
      if (p.id === 42) setSse((s) => ({ ...s, harga: p.harga, update: s.update + 1, telat: Date.now() - p.waktu_ms }));
      setLog((l) => [`${new Date().toLocaleTimeString("id-ID")} SSE: produk ${p.id} ${p.nama} → ${p.harga} (diubah lewat ${p.oleh ?? "?"}, dikirim ${d.node})`, ...l].slice(0, 60));
    });
    es.onerror = () => setSse((s) => ({ ...s, status: "putus, reconnect…" }));
    return () => es.close();
  }, [gw]);

  // 4. gRPC server streaming (PantauHarga) di backend, diteruskan ke browser lewat SSE.
  useEffect(() => {
    const es = new EventSource(`/api/grpc/pantau?id=42`);
    es.addEventListener("halo", (e) => {
      const d = JSON.parse((e as MessageEvent).data);
      setGs((s) => ({ ...s, request: s.request + 1, node: d.node, status: "stream terbuka" }));
    });
    es.addEventListener("harga", (e) => {
      const p = JSON.parse((e as MessageEvent).data);
      setGs((s) => ({ ...s, harga: p.harga, update: s.update + (s.harga === null ? 0 : 1) }));
    });
    es.onerror = () => setGs((s) => ({ ...s, status: "putus, reconnect…" }));
    return () => es.close();
  }, []);

  useEffect(() => {
    fetch("/api/ticker")
      .then((r) => r.json())
      .then((t) => setTicker(Boolean(t.aktif)))
      .catch(() => {});
  }, []);

  async function ubah() {
    const r = await fetch("/api/harga", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ id: 42, harga, protokol }),
    });
    const j = await r.json();
    tambahLog(`ubah harga lewat ${j.protokol ?? protokol}: ${j.error ?? "status " + (j.status ?? "OK")}`);
  }

  async function aturTicker(aktif: boolean) {
    const r = await fetch("/api/ticker", { method: "PUT", headers: { "content-type": "application/json" }, body: JSON.stringify({ aktif, interval_ms: 1500 }) });
    const t = await r.json();
    setTicker(Boolean(t.aktif));
  }

  const panel = (judul: string, p: Panel, ket: string) => (
    <div className="kartu">
      <h3>{judul}</h3>
      <p className="kecil" style={{ minHeight: 40 }}>{ket}</p>
      <div className="baris" style={{ gap: 18 }}>
        <div className="stat"><b>{p.harga ?? "–"}</b><span>harga Kopi</span></div>
        <div className="stat"><b>{p.request}</b><span>request/koneksi</span></div>
        <div className="stat"><b>{p.update}</b><span>update diterima</span></div>
        <div className="stat"><b>{p.telat === null ? "–" : `${p.telat} ms`}</b><span>telat terakhir</span></div>
      </div>
      <p className="kecil" style={{ marginBottom: 0 }}>node: {p.node} · {p.status}</p>
    </div>
  );

  return (
    <>
      <h1>Real-time ke klien</h1>
      <p className="lead">
        Empat cara mengirim perubahan harga ke browser (tabel cara mengirim data tanpa ditanya di Subbab Real-time). Ubah harga
        Kopi lewat protokol mana pun, lalu amati panel mana yang tahu lebih dulu dan berapa request yang dihabiskannya.
      </p>
      <div className="teori">
        Koneksi SSE dan WebSocket menempel pada satu replica. Perubahan dari replica lain tetap sampai karena setiap replica
        berlangganan Postgres <code>LISTEN/NOTIFY</code> (peran pub-sub pada gambar scale-out WebSocket di buku). Perhatikan
        kolom node: pengubah dan pengirim bisa berbeda.
      </div>

      <div className="kartu kontrol">
        <label className="f">
          Harga baru Kopi (id 42)
          <input type="number" value={harga} step={500} onChange={(e) => setHarga(Number(e.target.value))} />
        </label>
        <label className="f">
          Lewat protokol
          <select value={protokol} onChange={(e) => setProtokol(e.target.value)}>
            <option value="rest">REST (PUT /v1/produk/42/harga)</option>
            <option value="soap">SOAP (UbahHarga)</option>
            <option value="graphql">GraphQL (mutation ubahHarga)</option>
            <option value="grpc">gRPC (UbahHarga)</option>
          </select>
        </label>
        <button onClick={ubah}>Ubah harga</button>
        <button className="sekunder" onClick={() => aturTicker(!ticker)}>
          {ticker ? "Matikan ticker otomatis" : "Nyalakan ticker otomatis (1,5 detik)"}
        </button>
      </div>

      <div className="grid grid-2" style={{ marginBottom: 14 }}>
        {panel("1. Polling (setiap 2 detik)", poll, "Klien bertanya terus, ada perubahan atau tidak. Banyak request terbuang; telat sampai 2 detik.")}
        {panel("2. Long polling", lp, "Server menahan request sampai ada perubahan (maks 25 detik), lalu klien langsung bertanya lagi.")}
        {panel("3. SSE (text/event-stream)", sse, "Satu response HTTP yang tidak pernah selesai. Arah server ke klien saja; reconnect otomatis.")}
        {panel("4. gRPC server streaming", gs, "Backend klien membuka stream PantauHarga ke APISIX (HTTP/2), lalu meneruskannya ke browser sebagai SSE.")}
      </div>

      <div className="grid grid-2">
        <ChatPanel gw={gw} />
        <div className="kartu">
          <h3>Log event</h3>
          <div className="log">
            {log.length === 0 && <div className="redup">Belum ada event. Ubah harga atau nyalakan ticker.</div>}
            {log.map((l, i) => (
              <div key={i}>{l}</div>
            ))}
          </div>
        </div>
      </div>
    </>
  );
}

function ChatPanel({ gw }: { gw: string | null }) {
  const [nama, setNama] = useState("mahasiswa");
  const [teks, setTeks] = useState("");
  const [pesan, setPesan] = useState<Chat[]>([]);
  const [status, setStatus] = useState("belum tersambung");
  const ws = useRef<WebSocket | null>(null);

  function sambung() {
    if (!gw) return;
    ws.current?.close();
    const url = gw.replace(/^http/, "ws") + `/v1/chat?nama=${encodeURIComponent(nama)}`;
    const s = new WebSocket(url);
    ws.current = s;
    setStatus("membuka (HTTP Upgrade: websocket)…");
    s.onopen = () => setStatus("tersambung (101 Switching Protocols)");
    s.onclose = () => setStatus("tertutup");
    s.onerror = () => setStatus("error");
    s.onmessage = (e) => setPesan((p) => [JSON.parse(e.data) as Chat, ...p].slice(0, 80));
  }

  useEffect(() => () => ws.current?.close(), []);

  function kirim() {
    if (ws.current?.readyState === WebSocket.OPEN && teks.trim()) {
      ws.current.send(JSON.stringify({ teks }));
      setTeks("");
    }
  }

  return (
    <div className="kartu">
      <h3>WebSocket chat (dua arah)</h3>
      <p className="kecil">
        Buka halaman ini di dua tab atau dua laptop. Setiap tab bisa tersambung ke replica berbeda; pesan menyeberang lewat
        pub-sub Postgres.
      </p>
      <div className="baris" style={{ marginBottom: 8 }}>
        <input value={nama} onChange={(e) => setNama(e.target.value)} placeholder="nama" style={{ width: 140 }} />
        <button className="sekunder" onClick={sambung} disabled={!gw}>Sambung</button>
        <span className="kecil">{status}</span>
      </div>
      <div className="baris" style={{ marginBottom: 8 }}>
        <input value={teks} onChange={(e) => setTeks(e.target.value)} onKeyDown={(e) => e.key === "Enter" && kirim()} placeholder="tulis pesan, Enter" style={{ flex: 1, minWidth: 160 }} />
        <button onClick={kirim}>Kirim</button>
      </div>
      <div className="log">
        {pesan.map((m, i) => (
          <div key={i}>
            <span className="redup">{new Date(m.waktu_ms).toLocaleTimeString("id-ID")} </span>
            {m.jenis === "chat" ? (
              <>
                <b>{m.nama}</b>: {m.teks}
              </>
            ) : (
              <i className="redup">
                {m.nama} {m.jenis}
              </i>
            )}{" "}
            <span className="redup">[{m.node} → {m.node_penerima}]</span>
          </div>
        ))}
      </div>
    </div>
  );
}
