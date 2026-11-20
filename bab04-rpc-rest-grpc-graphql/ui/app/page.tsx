"use client";
import { useEffect, useState } from "react";
import Batang from "@/components/Batang";

type Tampilan = { kepala: string; isi: string; biner: boolean };
type Anotasi = { hex: string; keterangan: string; kedalaman: number };
type Hasil = {
  protokol: string;
  label: string;
  format: string;
  transport: string;
  ok: boolean;
  status: string;
  error?: string;
  request: Tampilan;
  response: Tampilan;
  payload_req: number;
  payload_resp: number;
  wire_req: number;
  wire_resp: number;
  latensi: { n: number; min: number; p50: number; p90: number; p99: number; maks: number; rata: number };
  anotasi?: Anotasi[];
  catatan?: string;
};
type Respons = { operasi: string; id: number; ulang: number; jalur: string; hasil: Hasil[] };
type Entri = { ms: number; arah: string; pesan: string };
type Mode = Record<"unary" | "server_streaming" | "client_streaming" | "bidi_streaming", Entri[]>;

const judulMode: Record<keyof Mode, string> = {
  unary: "Unary: AmbilProduk",
  server_streaming: "Server streaming: PantauHarga",
  client_streaming: "Client streaming: CatatPenjualan",
  bidi_streaming: "Bidirectional: Tawar",
};

export default function Playground() {
  const [operasi, setOperasi] = useState("ambil");
  const [id, setId] = useState(42);
  const [ulang, setUlang] = useState(30);
  const [jalur, setJalur] = useState("gateway");
  const [data, setData] = useState<Respons | null>(null);
  const [sibuk, setSibuk] = useState(false);
  const [galat, setGalat] = useState("");
  const [mode, setMode] = useState<Mode | null>(null);

  async function jalankan() {
    setSibuk(true);
    setGalat("");
    try {
      const r = await fetch("/api/bandingkan", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ operasi, id, ulang, jalur }),
      });
      const j = await r.json();
      if (!r.ok) throw new Error(j.error ?? r.statusText);
      setData(j);
    } catch (e) {
      setGalat((e as Error).message);
    } finally {
      setSibuk(false);
    }
  }

  async function jalankanMode() {
    const r = await fetch("/api/grpc/mode", { method: "POST", body: "{}" });
    setMode(await r.json());
  }

  // ?jalankan=ambil atau ?jalankan=daftar: langsung jalan saat halaman dibuka
  useEffect(() => {
    const op = new URLSearchParams(location.search).get("jalankan");
    if (op) {
      fetch("/api/bandingkan", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ operasi: op, id: 42, ulang: 30, jalur: "gateway" }) })
        .then((r) => r.json())
        .then((j) => {
          setOperasi(op === "daftar" ? "daftar" : "ambil");
          setData(j);
        });
      fetch("/api/grpc/mode", { method: "POST", body: "{}" }).then((r) => r.json()).then(setMode);
    }
  }, []);

  const hasil = data?.hasil ?? [];
  return (
    <>
      <h1>Protocol Playground</h1>
      <p className="lead">
        Permintaan yang <b>sama</b> (data produk dari Postgres yang sama) dijalankan lewat REST JSON, REST XML, SOAP,
        GraphQL, gRPC, dan gRPC-Web. Service <code>klien</code> mengukur dari dalam jaringan Docker, lalu menampilkan
        payload mentah, ukuran byte, dan latency (P50/P99).
      </p>
      <div className="teori">
        Slide P04 &ldquo;Data yang sama, ukuran berbeda&rdquo;: produk 42 Kopi 25000 adalah <b>37 byte</b> sebagai JSON
        dan <b>12 byte</b> sebagai Protobuf (<code>08 2A 12 04 4B 6F 70 69 18 A8 C3 01</code>). Coba operasi
        &ldquo;daftar&rdquo; (50 produk) untuk melihat selisih yang membesar, dan bandingkan jalur gateway dengan langsung
        untuk melihat biaya hop APISIX.
      </div>

      <div className="kartu kontrol">
        <label className="f">
          Operasi
          <select value={operasi} onChange={(e) => setOperasi(e.target.value)}>
            <option value="ambil">Ambil satu produk</option>
            <option value="daftar">Daftar 50 produk</option>
          </select>
        </label>
        {operasi === "ambil" && (
          <label className="f">
            ID produk
            <input type="number" min={1} max={60} value={id} onChange={(e) => setId(Number(e.target.value))} />
          </label>
        )}
        <label className="f">
          Ulang (sampel latency)
          <input type="number" min={1} max={500} value={ulang} onChange={(e) => setUlang(Number(e.target.value))} />
        </label>
        <label className="f">
          Jalur
          <select value={jalur} onChange={(e) => setJalur(e.target.value)}>
            <option value="gateway">Lewat APISIX (gateway)</option>
            <option value="langsung">Langsung ke katalog-1</option>
          </select>
        </label>
        <button onClick={jalankan} disabled={sibuk}>
          {sibuk ? "Menjalankan…" : "Jalankan semua protokol"}
        </button>
        {galat && <span className="gagal">{galat}</span>}
      </div>

      {hasil.length > 0 && (
        <>
          <div className="grid grid-2" style={{ marginBottom: 14 }}>
            <div className="kartu">
              <Batang
                judul="Ukuran payload response"
                satuan="B"
                kategori={hasil.map((h) => h.label)}
                seri={[{ nama: "payload", warna: "var(--series-1)", nilai: hasil.map((h) => h.payload_resp) }]}
              />
            </div>
            <div className="kartu">
              <Batang
                judul={`Latency (${data?.ulang} sampel, ${data?.jalur})`}
                satuan="ms"
                kategori={hasil.map((h) => h.label)}
                seri={[
                  { nama: "P50", warna: "var(--series-1)", nilai: hasil.map((h) => h.latensi.p50) },
                  { nama: "P99", warna: "var(--series-2)", nilai: hasil.map((h) => h.latensi.p99) },
                ]}
              />
            </div>
          </div>

          <div className="kartu" style={{ marginBottom: 14, overflowX: "auto" }}>
            <table className="data">
              <thead>
                <tr>
                  <th>Protokol</th>
                  <th>Format</th>
                  <th>Transport</th>
                  <th>Status</th>
                  <th className="angka">Payload req (B)</th>
                  <th className="angka">Payload resp (B)</th>
                  <th className="angka">Wire resp (B)</th>
                  <th className="angka">P50 (ms)</th>
                  <th className="angka">P99 (ms)</th>
                  <th className="angka">Rata (ms)</th>
                </tr>
              </thead>
              <tbody>
                {hasil.map((h) => (
                  <tr key={h.protokol}>
                    <td><b>{h.label}</b></td>
                    <td>{h.format}</td>
                    <td>{h.transport}</td>
                    <td className={h.ok ? "ok" : "gagal"}>{h.error ?? h.status}</td>
                    <td className="angka">{h.payload_req}</td>
                    <td className="angka">{h.payload_resp}</td>
                    <td className="angka">{h.wire_resp}</td>
                    <td className="angka">{h.latensi.p50}</td>
                    <td className="angka">{h.latensi.p99}</td>
                    <td className="angka">{h.latensi.rata}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <p className="kecil" style={{ marginBottom: 0 }}>
              Wire = header + body seperti yang dikirim di kabel (HTTP/1.1 teks). Untuk gRPC, header HTTP/2 dikompres HPACK
              sehingga hanya payload + 5 byte prefix yang dihitung.
            </p>
          </div>

          <div className="grid grid-3">
            {hasil.map((h) => (
              <div className="kartu" key={h.protokol}>
                <div className="baris" style={{ justifyContent: "space-between" }}>
                  <h3>{h.label}</h3>
                  <span className="lencana">{h.transport}</span>
                </div>
                <div className="kecil">Request ({h.payload_req} B body)</div>
                <pre>{h.request.kepala}</pre>
                {h.request.isi && <pre>{h.request.isi}</pre>}
                <div className="kecil" style={{ marginTop: 8 }}>
                  Response ({h.payload_resp} B body){h.response.biner ? " · hex" : ""}
                </div>
                <details>
                  <summary>header response</summary>
                  <pre>{h.response.kepala}</pre>
                </details>
                <pre>{h.response.isi}</pre>
                {h.anotasi && h.anotasi.length > 0 && (
                  <details open={operasi === "ambil"}>
                    <summary>bongkar wire format Protobuf</summary>
                    <table className="data anotasi">
                      <tbody>
                        {h.anotasi.map((a, i) => (
                          <tr key={i}>
                            <td style={{ paddingLeft: 8 + a.kedalaman * 14 }}>{a.hex}</td>
                            <td>{a.keterangan}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </details>
                )}
                {h.catatan && <p className="kecil">{h.catatan}</p>}
              </div>
            ))}
          </div>
        </>
      )}

      <div className="kartu" style={{ marginTop: 18 }}>
        <div className="baris" style={{ justifyContent: "space-between" }}>
          <h2>Empat mode panggilan gRPC</h2>
          <button className="sekunder" onClick={jalankanMode}>Jalankan keempat mode</button>
        </div>
        <p className="kecil">
          Kontrak <code>katalog.proto</code>: unary, server streaming, client streaming, dan bidirectional streaming, semuanya
          lewat APISIX (HTTP/2). Waktu dihitung relatif dari awal setiap panggilan.
        </p>
        {mode && (
          <div className="grid grid-2">
            {(Object.keys(judulMode) as (keyof Mode)[]).map((k) => (
              <div key={k}>
                <h3>{judulMode[k]}</h3>
                <div className="log">
                  {(mode[k] ?? []).map((e, i) => (
                    <div key={i}>
                      <span className="redup">{e.ms.toFixed(1).padStart(7)} ms </span>
                      <span className={e.arah === "kirim" ? "peringatan" : "ok"}>{e.arah === "kirim" ? "→ kirim " : "← terima"}</span>{" "}
                      {e.pesan}
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </>
  );
}
