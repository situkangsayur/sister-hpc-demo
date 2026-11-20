"use client";
import { useEffect, useState } from "react";
import Batang from "@/components/Batang";

type BarisKunci = { ke: number; status: number; sisa_kuota: string; batas: string; pesan: string; durasi_ms: number };
type RR = { http: string[]; hitung: Record<string, number>; grpc: string[] };
type Mode = { sql_queries: number; bytes: number; latensi: { p50: number; p99: number } };
type N1 = { query: string; naif: Mode; dataloader: Mode };

export default function GatewayPage() {
  const [cfg, setCfg] = useState<{ gw: string; grpcui: string } | null>(null);
  const [kunci, setKunci] = useState<{ dengan_kunci: boolean; hasil: BarisKunci[] } | null>(null);
  const [rr, setRr] = useState<RR | null>(null);
  const [n1, setN1] = useState<N1 | null>(null);
  const [sibuk, setSibuk] = useState("");

  useEffect(() => {
    fetch("/api/config")
      .then((r) => r.json())
      .then((c) => setCfg({ gw: `${location.protocol}//${location.hostname}:${c.gatewayPort}`, grpcui: `${location.protocol}//${location.hostname}:${c.grpcuiPort}` }));
    // ?jalankan=1: isi semua panel saat halaman dibuka
    if (new URLSearchParams(location.search).get("jalankan")) {
      const j = (u: string, b: object) => fetch(u, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b) }).then((r) => r.json());
      j("/api/nplus1", {}).then(setN1);
      j("/api/gateway/roundrobin", {}).then(setRr);
      j("/api/gateway/kunci", { dengan_kunci: true, jumlah: 8 }).then(setKunci);
    }
  }, []);

  async function post<T>(url: string, body: object, set: (v: T) => void, tanda: string) {
    setSibuk(tanda);
    try {
      const r = await fetch(url, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) });
      set(await r.json());
    } finally {
      setSibuk("");
    }
  }

  return (
    <>
      <h1>API gateway (APISIX) dan GraphQL N+1</h1>
      <p className="lead">
        APISIX berdiri di depan semua protokol (north-south): routing per path, key-auth, rate limit, request-id, CORS, proxy
        gRPC (HTTP/2), dan penerjemahan gRPC-Web. Konfigurasinya satu file YAML (mode standalone, tanpa etcd).
      </p>

      <div className="kartu" style={{ marginBottom: 14 }}>
        <h2>Alat interaktif per protokol</h2>
        {cfg && (
          <table className="data">
            <thead>
              <tr><th>Protokol</th><th>Alat</th><th>Kontrak</th></tr>
            </thead>
            <tbody>
              <tr><td>REST JSON/XML</td><td><a href={`${cfg.gw}/docs/swagger.html`} target="_blank">Swagger UI</a></td><td><a href={`${cfg.gw}/docs/openapi.yaml`} target="_blank">openapi.yaml</a></td></tr>
              <tr><td>SOAP 1.1</td><td><code>POST {cfg.gw}/soap</code></td><td><a href={`${cfg.gw}/soap?wsdl`} target="_blank">WSDL</a></td></tr>
              <tr><td>GraphQL</td><td><a href={`${cfg.gw}/docs/graphiql.html`} target="_blank">GraphiQL</a></td><td>introspection</td></tr>
              <tr><td>gRPC</td><td><a href={cfg.grpcui} target="_blank">grpcui</a> (lewat APISIX)</td><td>server reflection, <code>katalog.proto</code></td></tr>
            </tbody>
          </table>
        )}
      </div>

      <div className="grid grid-2" style={{ marginBottom: 14 }}>
        <div className="kartu">
          <h2>Key-auth dan rate limit</h2>
          <p className="kecil">
            Route <code>/publik/*</code> wajib header <code>apikey</code> dan dibatasi 5 request per 10 detik per consumer
            (plugin <code>key-auth</code> + <code>limit-count</code>). Kirim 8 request berturut-turut.
          </p>
          <div className="baris" style={{ marginBottom: 8 }}>
            <button className="sekunder" disabled={!!sibuk} onClick={() => post("/api/gateway/kunci", { dengan_kunci: false, jumlah: 3 }, setKunci, "k")}>Tanpa API key</button>
            <button disabled={!!sibuk} onClick={() => post("/api/gateway/kunci", { dengan_kunci: true, jumlah: 8 }, setKunci, "k")}>Dengan API key (8×)</button>
          </div>
          {kunci && (
            <table className="data">
              <thead><tr><th>#</th><th>Status</th><th className="angka">Sisa kuota</th><th>Isi</th></tr></thead>
              <tbody>
                {kunci.hasil.map((b) => (
                  <tr key={b.ke}>
                    <td>{b.ke}</td>
                    <td className={b.status === 200 ? "ok" : "gagal"}><b>{b.status}</b></td>
                    <td className="angka">{b.sisa_kuota || "–"}</td>
                    <td><code>{b.pesan.slice(0, 70)}</code></td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

        <div className="kartu">
          <h2>Load balancing ke dua replica</h2>
          <p className="kecil">
            Upstream APISIX round robin ke <code>katalog-1</code> dan <code>katalog-2</code>. Untuk gRPC, APISIX menyeimbangkan per
            request (L7), bukan per koneksi TCP: inilah alasan load balancer L4 saja tidak cukup untuk gRPC.
          </p>
          <button disabled={!!sibuk} onClick={() => post("/api/gateway/roundrobin", {}, setRr, "rr")}>Kirim 12 HTTP + 6 gRPC</button>
          {rr && (
            <>
              <p className="kecil">HTTP (header X-Node):</p>
              <div className="baris" style={{ gap: 4 }}>
                {rr.http.map((n, i) => (
                  <span key={i} className="lencana" style={{ borderColor: n === "katalog-1" ? "var(--series-1)" : "var(--series-2)" }}>{n}</span>
                ))}
              </div>
              <p className="kecil">gRPC unary di satu koneksi HTTP/2 (metadata x-node):</p>
              <div className="baris" style={{ gap: 4 }}>
                {rr.grpc.map((n, i) => (
                  <span key={i} className="lencana" style={{ borderColor: n === "katalog-1" ? "var(--series-1)" : "var(--series-2)" }}>{n}</span>
                ))}
              </div>
            </>
          )}
        </div>
      </div>

      <div className="kartu">
        <h2>GraphQL: masalah N+1 dan DataLoader</h2>
        <p className="kecil">
          Query <code>{"{ daftarProduk { id nama penjual { nama kota } } }"}</code> untuk 50 produk. Resolver naif menjalankan
          1 query produk + 1 query penjual per produk. DataLoader mengumpulkan semua id penjual dalam satu putaran lalu
          mengambilnya sekaligus dengan <code>WHERE id = ANY(...)</code>. Jumlah query dihitung di server.
        </p>
        <button disabled={!!sibuk} onClick={() => post("/api/nplus1", {}, setN1, "n1")}>{sibuk === "n1" ? "Mengukur…" : "Bandingkan (10 sampel)"}</button>
        {n1 && (
          <div className="grid grid-2" style={{ marginTop: 12 }}>
            <Batang judul="Jumlah query SQL per request" satuan="query" kategori={["naif", "DataLoader"]}
              seri={[{ nama: "query", warna: "var(--series-1)", nilai: [n1.naif.sql_queries, n1.dataloader.sql_queries] }]} />
            <Batang judul="Latency P50 (ms)" satuan="ms" kategori={["naif", "DataLoader"]}
              seri={[{ nama: "P50", warna: "var(--series-1)", nilai: [n1.naif.latensi.p50, n1.dataloader.latensi.p50] }]} />
          </div>
        )}
      </div>
    </>
  );
}
