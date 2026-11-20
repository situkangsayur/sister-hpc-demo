"use client";
import { useRef, useState } from "react";
import { fmt, probe } from "../lib";

type CacheRes = {
  path: string;
  status: number;
  proto: string;
  wire_bytes: number;
  dur_ms: number;
  headers: Record<string, string>;
  origin_hits: number;
  origin_delta: number;
  sent_if_none_match?: string;
  sent_accept_encoding?: string;
  err?: string;
};
type Row = CacheRes & { t: number; note: string };
type HSTS = { http: CacheRes; https: CacheRes };
type Hit = { n: number; status: number; instance: string; upstream: string; dur_ms: number; err?: string };
type Stat = { instance: string; versi: number; sakit: boolean; hits: Record<string, number>; err?: string };
type LB = { hits: Hit[]; counts: Record<string, number>; stats: Stat[] };

function cachePill(s?: string) {
  if (!s) return <span className="muted">-</span>;
  const cls = s === "HIT" ? "hit" : s === "MISS" || s === "EXPIRED" ? "miss" : s === "REVALIDATED" ? "reval" : "";
  return <span className={`pill ${cls}`}>{s}</span>;
}

export default function DepanPage() {
  const [rows, setRows] = useState<Row[]>([]);
  const [gzip, setGzip] = useState(true);
  const [hsts, setHsts] = useState<HSTS | null>(null);
  const [lb, setLb] = useState<LB | null>(null);
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");
  const t0 = useRef<number | null>(null);

  const lastEtag = [...rows].reverse().find((r) => r.path === "/api/katalog" && r.headers?.ETag)?.headers.ETag ?? "";

  async function wrap(name: string, f: () => Promise<void>) {
    setBusy(name);
    setErr("");
    try {
      await f();
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy("");
    }
  }

  const get = (path: string, inm: string, note: string, gz = gzip) =>
    wrap("cache", async () => {
      const q = new URLSearchParams({ path, gzip: gz ? "1" : "0" });
      if (inm) q.set("inm", inm);
      const r = await probe<CacheRes>(`cache?${q}`);
      if (t0.current === null) t0.current = Date.now();
      setRows((rs) => [...rs, { ...r, t: (Date.now() - (t0.current ?? Date.now())) / 1000, note }]);
    });

  const compress = () =>
    wrap("cache", async () => {
      for (const gz of [false, true]) {
        const r = await probe<CacheRes>(`cache?path=/api/katalog&gzip=${gz ? 1 : 0}`);
        if (t0.current === null) t0.current = Date.now();
        setRows((rs) => [...rs, { ...r, t: (Date.now() - (t0.current ?? Date.now())) / 1000, note: gz ? "dengan gzip" : "tanpa gzip" }]);
      }
    });

  const bump = () =>
    wrap("cache", async () => {
      await probe("cache/bump", { method: "POST" });
      setRows((rs) => [
        ...rs,
        {
          path: "(origin)", status: 0, proto: "", wire_bytes: 0, dur_ms: 0, headers: {}, origin_hits: 0, origin_delta: 0,
          t: (Date.now() - (t0.current ?? Date.now())) / 1000, note: "versi katalog di origin dinaikkan: ETag berubah",
        },
      ]);
    });

  const runLB = () => wrap("lb", async () => setLb(await probe<LB>("lb?n=12")));
  const sick = (on: boolean) =>
    wrap("lb", async () => {
      await probe(`lb/sakit?instance=origin-b&on=${on ? 1 : 0}`, { method: "POST" });
      setLb(await probe<LB>("lb?n=12"));
    });

  const maxCount = lb ? Math.max(1, ...Object.values(lb.counts)) : 1;
  const bSick = lb?.stats.find((s) => s.instance === "origin-b")?.sakit;

  return (
    <>
      <h1>Di depan server: nginx sebagai reverse proxy, cache, dan load balancer</h1>
      <p className="muted">
        Bab 3 subbab &quot;Caching HTTP&quot; (RFC 9111), &quot;Reverse proxy&quot;, dan &quot;Load balancer L4 dan L7&quot;. nginx melakukan TLS
        termination (HTTPS + HTTP/2 di depan, HTTP/1.1 polos ke origin), menyimpan response di <code>proxy_cache</code>, mengompresi dengan gzip,
        menambah header HSTS, dan membagi request ke <code>origin-a</code> dan <code>origin-b</code> dengan <code>least_conn</code>.
      </p>

      <div className="card">
        <h2>Cache: hit, miss, dan validasi ulang dengan ETag</h2>
        <p className="muted">
          <code>/api/katalog</code> dikirim origin dengan <code>Cache-Control: public, max-age=5</code> dan <code>ETag: &quot;vN&quot;</code>. Urutan
          yang disarankan: (1) GET pertama → MISS; (2) GET lagi dalam 5 detik → HIT, origin tidak dipanggil; (3) GET dengan If-None-Match → 304 tanpa
          body; (4) tunggu lebih dari 5 detik lalu GET → REVALIDATED (nginx bertanya ke origin, origin menjawab 304); (5) Ubah versi, tunggu 5 detik,
          GET → EXPIRED dengan ETag baru.
        </p>
        <div className="row">
          <button disabled={!!busy} onClick={() => get("/api/katalog", "", "GET biasa")}>
            GET /api/katalog
          </button>
          <button className="alt" disabled={!!busy || !lastEtag} onClick={() => get("/api/katalog", lastEtag, `If-None-Match ${lastEtag}`)}>
            GET dengan If-None-Match {lastEtag || "(belum ada ETag)"}
          </button>
          <button className="alt" disabled={!!busy} onClick={() => get("/static/app.3f9a1c.js", "", "aset statis bersidik isi")}>
            GET /static/app.3f9a1c.js
          </button>
          <button className="alt" disabled={!!busy} onClick={compress}>
            Bandingkan tanpa/dengan gzip
          </button>
          <button className="warn" disabled={!!busy} onClick={bump}>
            Ubah versi katalog di origin
          </button>
          <label style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
            <input type="checkbox" checked={gzip} onChange={(e) => setGzip(e.target.checked)} /> kirim Accept-Encoding: gzip
          </label>
          <button className="alt" onClick={() => { setRows([]); t0.current = null; }}>
            Kosongkan log
          </button>
        </div>
        {err && <div className="err">{err}</div>}
        {rows.length > 0 && (
          <table style={{ marginTop: 10 }}>
            <thead>
              <tr>
                <th>t (s)</th>
                <th>Request</th>
                <th>Status</th>
                <th>X-Cache-Status</th>
                <th>ETag</th>
                <th>Cache-Control</th>
                <th>Encoding</th>
                <th>Byte di kabel</th>
                <th>Origin dipanggil?</th>
                <th>Waktu</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr key={i}>
                  <td>{r.t.toFixed(1)}</td>
                  <td>
                    <code>{r.path}</code>
                    <div className="muted">{r.note}</div>
                  </td>
                  <td>{r.status ? <strong className={r.status === 304 ? "ok" : ""}>{r.status}</strong> : ""}</td>
                  <td>{r.status ? cachePill(r.headers["X-Cache-Status"]) : ""}</td>
                  <td className="mono">{r.headers.ETag}</td>
                  <td className="mono">{r.headers["Cache-Control"]}</td>
                  <td>{r.headers["Content-Encoding"]}</td>
                  <td>{r.status ? r.wire_bytes : ""}</td>
                  <td>{r.status ? (r.origin_delta > 0 ? <span className="bad">ya (+{r.origin_delta})</span> : <span className="ok">tidak</span>) : ""}</td>
                  <td>{r.status ? `${fmt(r.dur_ms)} ms` : ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <p className="muted">
          ETag dari nginx bisa berawalan <code>W/</code> (weak) bila body dikompresi: isi byte berubah, tetapi maknanya sama. Aset statis memakai
          sidik isi di nama file dan <code>max-age=31536000, immutable</code>, sehingga tidak perlu di-invalidate.
        </p>
      </div>

      <div className="grid">
        <div className="card">
          <h2>HTTP ke HTTPS dan HSTS</h2>
          <button disabled={!!busy} onClick={() => wrap("hsts", async () => setHsts(await probe<HSTS>("hsts")))}>
            Minta lewat http:// dan https://
          </button>
          {hsts && (
            <table style={{ marginTop: 10 }}>
              <tbody>
                <tr>
                  <th>http://nginx/api/katalog</th>
                  <td>
                    <strong>{hsts.http.status}</strong> → <code>{hsts.http.headers.Location}</code>
                  </td>
                </tr>
                <tr>
                  <th>https://nginx/api/katalog</th>
                  <td>
                    <strong>{hsts.https.status}</strong> ({hsts.https.proto}) · <code>Strict-Transport-Security: {hsts.https.headers["Strict-Transport-Security"]}</code>
                  </td>
                </tr>
              </tbody>
            </table>
          )}
          <p className="muted">
            Redirect 301 masih membuka celah pada request pertama yang polos. HSTS menyuruh browser memakai HTTPS langsung untuk domain ini selama
            max-age (di demo hanya 300 detik agar aman dicoba).
          </p>
        </div>

        <div className="card">
          <h2>Load balancer L7: least_conn + health check pasif</h2>
          <div className="row">
            <button disabled={!!busy} onClick={runLB}>
              Kirim 12 request ke /api/siapa
            </button>
            {bSick ? (
              <button className="alt" disabled={!!busy} onClick={() => sick(false)}>
                Sembuhkan origin-b
              </button>
            ) : (
              <button className="warn" disabled={!!busy} onClick={() => sick(true)}>
                Buat origin-b sakit (503)
              </button>
            )}
          </div>
          {lb && (
            <>
              <h3>Distribusi</h3>
              {Object.entries(lb.counts).map(([k, v]) => (
                <div key={k} className="row" style={{ alignItems: "center", marginBottom: 4 }}>
                  <span style={{ width: 90 }}>{k}</span>
                  <div className="bar" style={{ width: `${(60 * v) / maxCount}%`, background: k === "origin-a" ? "var(--hijau)" : k === "origin-b" ? "var(--c-wait)" : "var(--merah)" }} />
                  <span>{v}</span>
                </div>
              ))}
              <p className="muted">
                Status origin: {lb.stats.map((s) => `${s.instance}${s.sakit ? " (sakit)" : ""}`).join(", ")}
              </p>
              <table>
                <thead>
                  <tr>
                    <th>#</th>
                    <th>Status</th>
                    <th>Dijawab</th>
                    <th>Upstream yang dicoba nginx</th>
                  </tr>
                </thead>
                <tbody>
                  {lb.hits.map((h) => (
                    <tr key={h.n}>
                      <td>{h.n}</td>
                      <td>{h.status || h.err}</td>
                      <td>{h.instance}</td>
                      <td className="mono">{h.upstream}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </>
          )}
          <p className="muted">
            Saat origin-b menjawab 503, nginx mencoba ulang ke origin-a (<code>proxy_next_upstream http_503</code>), sehingga klien tetap menerima 200.
            Sesudah 2 kegagalan (<code>max_fails=2</code>) origin-b diistirahatkan 10 detik (<code>fail_timeout=10s</code>) dan tidak dicoba lagi.
          </p>
        </div>
      </div>
    </>
  );
}
