"use client";
import { Fragment, useState } from "react";
import { probe } from "../lib";

type Rec = { dir: string; kind: string; detail?: string; length: number; hex: string; ascii: string; visible?: string };
type Sniff = {
  mode: string;
  url: string;
  status: number;
  response_body: string;
  bytes_out: number;
  bytes_in: number;
  secret_visible: boolean;
  sni?: string;
  records: Rec[];
  err?: string;
};

const SECRET = "rahasia123";

function highlight(text: string) {
  const parts = text.split(SECRET);
  return parts.map((p, i) => (
    <Fragment key={i}>
      {p}
      {i < parts.length - 1 && <mark>{SECRET}</mark>}
    </Fragment>
  ));
}

function Panel({ s, title }: { s: Sniff; title: string }) {
  return (
    <div className="card">
      <h2>{title}</h2>
      <p className="mono muted">
        {s.url} · HTTP {s.status} · keluar {s.bytes_out} B, masuk {s.bytes_in} B
      </p>
      {s.err && <div className="err">{s.err}</div>}
      <p>
        Password <code>{SECRET}</code> terbaca penyadap:{" "}
        <strong className={s.secret_visible ? "bad" : "ok"}>{s.secret_visible ? "YA" : "TIDAK"}</strong>
        {s.sni && (
          <>
            {" "}
            · SNI terbaca: <code>{s.sni}</code>
          </>
        )}
      </p>
      {s.mode === "http" ? (
        <pre className="dump">
          {s.records.map((r, i) => (
            <Fragment key={i}>
              <span className={r.dir === "out" ? "dir-out" : "dir-in"}>
                {r.dir === "out" ? "──▶ probe ke server" : "◀── server ke probe"} ({r.length} byte)
              </span>
              {"\n"}
              {highlight(r.visible ?? "")}
              {"\n\n"}
            </Fragment>
          ))}
        </pre>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Arah</th>
              <th>TLS record</th>
              <th>Byte</th>
              <th>Awal isi (hex)</th>
            </tr>
          </thead>
          <tbody>
            {s.records.map((r, i) => (
              <tr key={i}>
                <td className={r.dir === "out" ? "ok" : ""}>{r.dir === "out" ? "──▶" : "◀──"}</td>
                <td>
                  <strong>{r.kind}</strong> {r.detail && <span className="muted">{r.detail}</span>}
                </td>
                <td>{r.length}</td>
                <td className="mono" style={{ wordBreak: "break-all" }}>
                  {r.hex}
                  <div className="muted">{r.ascii}</div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <p className="muted">Jawaban yang diterima aplikasi: &quot;{s.response_body}&quot;</p>
    </div>
  );
}

export default function SniffPage() {
  const [plain, setPlain] = useState<Sniff | null>(null);
  const [tls, setTls] = useState<Sniff | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  async function run() {
    setBusy(true);
    setErr("");
    try {
      setPlain(await probe<Sniff>("sniff?mode=http"));
      setTls(await probe<Sniff>("sniff?mode=https"));
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <h1>Apa yang dilihat penyadap: HTTP polos vs HTTPS</h1>
      <p className="muted">
        Bab 3 subbab &quot;Tiga jaminan TLS&quot; dan Praktikum 2 (&quot;Bisakah Anda membaca isi response?&quot;). Probe mengirim form login{" "}
        <code>user=budi&amp;password={SECRET}</code> beserta cookie, sekali lewat <code>http://origin-a:8080</code> dan sekali lewat{" "}
        <code>https://origin-a:8443</code>, sambil mencatat setiap byte di koneksi TCP (seperti Wireshark yang sangat sederhana).
      </p>
      <div className="card row">
        <button onClick={run} disabled={busy}>
          {busy ? "Mengirim..." : "Kirim form login dan tangkap byte-nya"}
        </button>
      </div>
      {err && <div className="err">{err}</div>}
      <div className="grid">
        {plain && <Panel s={plain} title="HTTP polos (port 8080)" />}
        {tls && <Panel s={tls} title="HTTPS (TLS 1.3, port 8443)" />}
      </div>
      {tls && (
        <div className="card">
          <h2>Cara membaca</h2>
          <p>
            Pada HTTP polos, metode, path, header <code>Cookie</code>, dan body form terbaca utuh oleh siapa pun di jalur jaringan (kerahasiaan
            hilang), dan bisa diubah tanpa ketahuan (integritas hilang). Pada HTTPS, yang terbaca hanya ClientHello dan ServerHello: versi, cipher suite
            yang ditawarkan, dan nama server (SNI). Sesudah itu semua record bertipe <code>ApplicationData</code>, termasuk certificate server
            (TLS 1.3 mengenkripsinya juga). SNI yang masih polos inilah yang ingin ditutup oleh Encrypted Client Hello (ECH).
          </p>
        </div>
      )}
    </>
  );
}
