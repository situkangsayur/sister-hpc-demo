"use client";
// Panel pengatur `tc netem` di router (kedua arah).
import { useEffect, useState } from "react";
import { api, droppedFrom, Netem, NetemStatus } from "@/lib/api";

const PRESETS: { name: string; p: Netem }[] = [
  { name: "Normal", p: { delay_ms: 0, jitter_ms: 0, loss_pct: 0, rate_mbit: 0 } },
  { name: "Praktikum: 50 ms, loss 5%", p: { delay_ms: 50, jitter_ms: 0, loss_pct: 5, rate_mbit: 0 } },
  { name: "Seluler buruk", p: { delay_ms: 120, jitter_ms: 30, loss_pct: 8, rate_mbit: 5 } },
  { name: "Satelit (RTT ~600 ms)", p: { delay_ms: 300, jitter_ms: 0, loss_pct: 1, rate_mbit: 10 } },
  { name: "Loss parah 15%", p: { delay_ms: 30, jitter_ms: 0, loss_pct: 15, rate_mbit: 0 } },
];

export default function NetemPanel({ onChange }: { onChange?: (s: NetemStatus) => void }) {
  const [p, setP] = useState<Netem>(PRESETS[0].p);
  const [status, setStatus] = useState<NetemStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => {
    api<NetemStatus>("netem")
      .then((s) => {
        setStatus(s);
        setP(s.params);
        onChange?.(s);
      })
      .catch((e) => setErr(String(e.message ?? e)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function apply(next: Netem) {
    setBusy(true);
    setErr("");
    try {
      const s = await api<NetemStatus>("netem", next);
      setStatus(s);
      setP(s.params);
      onChange?.(s);
    } catch (e) {
      setErr(String((e as Error).message));
    } finally {
      setBusy(false);
    }
  }

  async function refresh() {
    try {
      setStatus(await api<NetemStatus>("netem"));
    } catch {
      /* abaikan */
    }
  }

  const set = (k: keyof Netem) => (e: React.ChangeEvent<HTMLInputElement>) => {
    const v = Number(e.target.value);
    setP((old) => {
      const n = { ...old, [k]: v };
      if (n.delay_ms === 0) n.jitter_ms = 0;
      return n;
    });
  };

  const dropC = status ? droppedFrom(status.c2s.tc) : null;
  const dropS = status ? droppedFrom(status.s2c.tc) : null;

  return (
    <section className="card">
      <h2>Kondisi jaringan (tc netem di router)</h2>
      <p className="muted">
        Aturan dipasang di <b>kedua arah</b> (klien→server dan server→klien), jadi RTT naik kira-kira 2 × delay dan
        peluang satu ping UDP gagal kembali ≈ 1 − (1 − loss)².
      </p>
      <div className="presets">
        {PRESETS.map((x) => (
          <button key={x.name} className="ghost" disabled={busy} onClick={() => apply(x.p)}>
            {x.name}
          </button>
        ))}
      </div>
      <div className="sliders">
        <label>
          delay <b>{p.delay_ms} ms</b>
          <input type="range" min={0} max={300} step={5} value={p.delay_ms} onChange={set("delay_ms")} />
        </label>
        <label>
          jitter <b>±{p.jitter_ms} ms</b>
          <input type="range" min={0} max={100} step={5} value={p.jitter_ms} disabled={p.delay_ms === 0} onChange={set("jitter_ms")} />
        </label>
        <label>
          loss <b>{p.loss_pct}%</b>
          <input type="range" min={0} max={30} step={0.5} value={p.loss_pct} onChange={set("loss_pct")} />
        </label>
        <label>
          rate <b>{p.rate_mbit === 0 ? "tanpa batas" : `${p.rate_mbit} Mbit/s`}</b>
          <input type="range" min={0} max={100} step={1} value={p.rate_mbit} onChange={set("rate_mbit")} />
        </label>
      </div>
      <div className="row">
        <button disabled={busy} onClick={() => apply(p)}>
          {busy ? "Menerapkan..." : "Terapkan netem"}
        </button>
        <span className="pill">aktif: {status?.summary ?? "?"}</span>
        {dropC !== null && (
          <span className="muted">
            dibuang netem: {dropC} packet (klien→server), {dropS} packet (server→klien){" "}
            <button className="link" onClick={refresh}>
              segarkan
            </button>
          </span>
        )}
      </div>
      {status && (
        <details>
          <summary>Perintah tc yang setara</summary>
          <pre>
            {status.params.delay_ms || status.params.loss_pct || status.params.rate_mbit
              ? `tc qdisc add dev ${status.c2s.iface} root netem ${status.summary}\ntc qdisc add dev ${status.s2c.iface} root netem ${status.summary}\n\n`
              : `tc qdisc del dev ${status.c2s.iface} root   # tanpa netem\n\n`}
            {`# tc -s qdisc show dev ${status.c2s.iface}\n${status.c2s.tc}\n# tc -s qdisc show dev ${status.s2c.iface}\n${status.s2c.tc}`}
          </pre>
        </details>
      )}
      {err && <p className="error">{err}</p>}
    </section>
  );
}
