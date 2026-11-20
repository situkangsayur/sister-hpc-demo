"use client";
import { useState } from "react";
import { fmt, probe } from "../lib";

type Cert = {
  role: string;
  subject: string;
  issuer: string;
  serial: string;
  not_before: string;
  not_after: string;
  dns_names?: string[];
  ips?: string[];
  is_ca: boolean;
  key_alg: string;
  sig_alg: string;
  sha256: string;
};
type Check = { name: string; ok: boolean; msg: string };
type Report = {
  target: string;
  addr: string;
  version: string;
  cipher: string;
  alpn: string;
  connect_ms: number;
  handshake_ms: number;
  peer_chain: Cert[];
  verified_chain: Cert[];
  checks: Check[];
  err?: string;
};
type Attempt = { name: string; ok: boolean; status: number; body: string; err?: string };

function CertCard({ c }: { c: Cert }) {
  return (
    <div className={`cert ${c.role}`}>
      <div>
        <span className="pill">{c.role}</span> <strong>{c.subject}</strong>
      </div>
      <div className="muted">
        diterbitkan oleh <strong>{c.issuer}</strong> · berlaku {c.not_before.slice(0, 10)} s.d. {c.not_after.slice(0, 10)}
      </div>
      <div className="mono">
        {c.key_alg} · tanda tangan {c.sig_alg} · CA: {c.is_ca ? "ya" : "tidak"} · SHA-256 {c.sha256}
      </div>
      {(c.dns_names?.length || c.ips?.length) && (
        <div className="mono">SAN: {[...(c.dns_names ?? []), ...(c.ips ?? [])].join(", ")}</div>
      )}
    </div>
  );
}

export default function TLSPage() {
  const [reps, setReps] = useState<Report[]>([]);
  const [mtls, setMtls] = useState<Attempt[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  async function inspect() {
    setBusy(true);
    setErr("");
    try {
      const out: Report[] = [];
      for (const target of ["origin", "nginx"])
        for (const max of ["1.3", "1.2"]) out.push(await probe<Report>(`tls?target=${target}&max=${max}`));
      setReps(out);
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  }

  async function tryMtls() {
    setErr("");
    try {
      setMtls((await probe<{ attempts: Attempt[] }>("mtls")).attempts);
    } catch (e) {
      setErr(String(e));
    }
  }

  const main = reps[0];
  return (
    <>
      <h1>TLS dan HTTPS: handshake, cipher suite, dan rantai certificate</h1>
      <p className="muted">
        Bab 3 subbab &quot;Handshake TLS 1.3&quot;, &quot;X.509 certificate dan rantai kepercayaan&quot;, dan &quot;mTLS&quot;. CA demo dan
        certificate dibuat saat container <code>certgen</code> start; tidak ada kunci di repo. Probe menyambung ke origin (Go{" "}
        <code>crypto/tls</code>) dan ke nginx (OpenSSL) dengan TLS 1.3 dan TLS 1.2.
      </p>
      <div className="card row">
        <button onClick={inspect} disabled={busy}>
          {busy ? "Menyambung..." : "Tengok TLS origin dan nginx"}
        </button>
        <button className="alt" onClick={tryMtls}>
          Uji mTLS (3 pemanggil)
        </button>
      </div>
      {err && <div className="err">{err}</div>}

      {reps.length > 0 && (
        <div className="card">
          <h2>Hasil negosiasi</h2>
          <table>
            <thead>
              <tr>
                <th>Server</th>
                <th>Versi TLS</th>
                <th>Cipher suite</th>
                <th>ALPN</th>
                <th>TCP connect</th>
                <th>TLS handshake</th>
              </tr>
            </thead>
            <tbody>
              {reps.map((r, i) => (
                <tr key={i}>
                  <td>
                    {r.target} <span className="muted mono">{r.addr}</span>
                  </td>
                  <td>
                    <strong>{r.version || "-"}</strong>
                  </td>
                  <td className="mono">{r.cipher || r.err}</td>
                  <td className="mono">{r.alpn || "-"}</td>
                  <td>{fmt(r.connect_ms)} ms</td>
                  <td>
                    <strong>{fmt(r.handshake_ms)} ms</strong>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="muted">
            Bandingkan kolom TLS handshake dengan TCP connect (≈ 1 RTT): TLS 1.3 selesai dalam ±1 RTT, TLS 1.2 butuh ±2 RTT. Nama cipher suite TLS
            1.3 hanya memuat AEAD dan hash (<code>TLS_AES_128_GCM_SHA256</code>) karena key exchange (ECDHE) dan tanda tangan dinegosiasikan terpisah;
            pada TLS 1.2 semuanya ada di nama (<code>TLS_ECDHE_ECDSA_WITH_..._GCM_SHA256</code>).
          </p>
        </div>
      )}

      {main && !main.err && (
        <div className="grid">
          <div className="card">
            <h2>Rantai yang dikirim server ({main.peer_chain.length} certificate)</h2>
            <div className="chain">
              {main.peer_chain.map((c, i) => (
                <div key={i}>
                  {i > 0 && <div className="arrow">↑ ditandatangani oleh</div>}
                  <CertCard c={c} />
                </div>
              ))}
            </div>
            <p className="muted">Server mengirim leaf dan intermediate. Root tidak dikirim: root harus sudah ada di trust store klien.</p>
          </div>
          <div className="card">
            <h2>Rantai yang diverifikasi klien (sampai root)</h2>
            <div className="chain">
              {main.verified_chain.map((c, i) => (
                <div key={i}>
                  {i > 0 && <div className="arrow">↓ dipercaya karena ditandatangani oleh</div>}
                  <CertCard c={c} />
                </div>
              ))}
            </div>
            <h3>Mengapa browser bisa menolak certificate yang sama</h3>
            <table>
              <tbody>
                {main.checks.map((c, i) => (
                  <tr key={i}>
                    <td className={c.ok ? "ok" : "bad"}>{c.ok ? "LOLOS" : "DITOLAK"}</td>
                    <td>
                      {c.name}
                      <div className="mono muted">{c.msg}</div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {mtls && (
        <div className="card">
          <h2>mTLS: server meminta certificate klien (origin :9443)</h2>
          <table>
            <thead>
              <tr>
                <th>Pemanggil</th>
                <th>Hasil</th>
                <th>Jawaban / error</th>
              </tr>
            </thead>
            <tbody>
              {mtls.map((a, i) => (
                <tr key={i}>
                  <td>{a.name}</td>
                  <td className={a.ok ? "ok" : "bad"}>{a.ok ? `diterima (HTTP ${a.status})` : "ditolak saat handshake"}</td>
                  <td className="mono">{a.ok ? a.body : a.err}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="muted">
            Server memakai <code>ClientAuth: tls.RequireAndVerifyClientCert</code> dan <code>ClientCAs</code> = CA demo. Penyusup yang sudah berada di
            jaringan internal tetap ditolak karena tidak memegang certificate yang diterbitkan CA tersebut (pola zero trust).
          </p>
        </div>
      )}
    </>
  );
}
