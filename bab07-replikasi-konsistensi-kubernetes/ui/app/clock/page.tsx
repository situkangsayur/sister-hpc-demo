"use client";
import { useMemo, useState } from "react";
import { Down } from "@/components/Down";
import { fmtTime, post, usePoll } from "@/lib/poll";

type Ev = { id: string; proc: string; seq: number; kind: "local" | "send" | "recv"; lamport: number; vector: number[]; msg_id?: string; peer?: string; wall_ms: number; real_ms: number };
type St = { node: string; index: number; procs: string[]; skew_ms: number; lamport: number; vector: number[]; events: Ev[] };

const PROCS = ["P1", "P2", "P3"];
const COLORS: Record<string, string> = { P1: "var(--p1)", P2: "var(--p2)", P3: "var(--p3)" };
const api = (p: string) => `/api/clock-${p.toLowerCase()}`;

function compare(a: number[], b: number[]): "sama" | "sebelum" | "sesudah" | "concurrent" {
  let le = true, ge = true;
  a.forEach((x, i) => { if (x > b[i]) le = false; if (x < b[i]) ge = false; });
  if (le && ge) return "sama";
  if (le) return "sebelum";
  if (ge) return "sesudah";
  return "concurrent";
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

export default function ClockPage() {
  const p1 = usePoll<St>(api("P1") + "/state", 700);
  const p2 = usePoll<St>(api("P2") + "/state", 700);
  const p3 = usePoll<St>(api("P3") + "/state", 700);
  const [label, setLabel] = useState<"lamport" | "vector" | "wall">("vector");
  const [delay, setDelay] = useState(0);
  const [sel, setSel] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const states = [p1.data, p2.data, p3.data];
  const refresh = () => { p1.refresh(); p2.refresh(); p3.refresh(); };

  const events = useMemo(() => {
    const all: Ev[] = [];
    for (const s of [p1.data, p2.data, p3.data]) if (s) all.push(...s.events);
    return all.sort((a, b) => a.real_ms - b.real_ms || a.proc.localeCompare(b.proc) || a.seq - b.seq);
  }, [p1.data, p2.data, p3.data]);

  if (p1.down || p2.down || p3.down) return (<><h1>Lamport clock dan vector clock</h1><Down profile="clock" /></>);

  const run = async (f: () => Promise<void>) => {
    setBusy(true); setErr("");
    try { await f(); } catch (e) { setErr(String(e)); }
    setBusy(false); refresh();
  };
  const local = (p: string) => post(api(p) + "/local");
  const send = (from: string, to: string, d = delay) => post(`${api(from)}/send?to=${to}&delay_ms=${d}`);
  const resetAll = async () => { for (const p of PROCS) await post(api(p) + "/reset"); setSel([]); };

  const bookScenario = () => run(async () => {
    await resetAll();
    await local("P1"); await local("P2"); await local("P3");
    await send("P1", "P2", 0); await local("P1");
    await send("P2", "P3", 0); await local("P3");
  });
  const crossing = () => run(async () => {
    await resetAll();
    await local("P2");
    await send("P1", "P3", 1500); await sleep(150); await send("P3", "P1", 1500);
    await sleep(1800); await local("P1"); await local("P3");
  });
  const randomRun = () => run(async () => {
    for (let i = 0; i < 8; i++) {
      const a = PROCS[Math.floor(Math.random() * 3)];
      if (Math.random() < 0.35) await local(a);
      else { const b = PROCS.filter((x) => x !== a)[Math.floor(Math.random() * 2)]; await send(a, b, Math.random() < 0.5 ? 0 : 600 + Math.floor(Math.random() * 900)); }
      await sleep(120);
    }
  });

  // Geometri diagram ruang-waktu: sumbu x = urutan kejadian menurut jam host, satu garis per proses.
  const W = Math.max(720, 90 + events.length * 46), H = 250, y = (p: string) => 50 + PROCS.indexOf(p) * 80;
  const x = (i: number) => 70 + i * 46;
  const pos = new Map(events.map((e, i) => [e.id, { x: x(i), y: y(e.proc), e }]));
  const recvByMsg = new Map(events.filter((e) => e.kind === "recv").map((e) => [e.msg_id, e]));
  const sends = events.filter((e) => e.kind === "send");
  const lbl = (e: Ev) => label === "lamport" ? `C=${e.lamport}` : label === "vector" ? `[${e.vector.join(",")}]` : fmtTime(e.wall_ms).slice(3);

  const toggle = (id: string) => setSel((s) => (s.includes(id) ? s.filter((x) => x !== id) : [...s.slice(-1), id]));
  const [a, b] = sel.map((id) => pos.get(id)?.e).filter(Boolean) as Ev[];

  const lamportOrder = [...events].sort((p, q) => p.lamport - q.lamport || p.proc.localeCompare(q.proc));
  const wallOrder = [...events].sort((p, q) => p.wall_ms - q.wall_ms);
  const sendOf = new Map(sends.map((e) => [e.msg_id, e]));
  const anomalies = events.filter((e) => e.kind === "recv" && sendOf.get(e.msg_id) && e.wall_ms < sendOf.get(e.msg_id)!.wall_ms);

  return (
    <>
      <h1>Lamport clock dan vector clock</h1>
      <p className="lead">
        Bab 7 subbab Waktu dan Urutan (slide P07). Tiga proses Go (P1, P2, P3) masing-masing memegang Lamport clock,
        vector clock, dan jam dinding yang sengaja digeser. Klik dua titik untuk membandingkan kejadiannya.
      </p>
      <div className="card">
        <div className="row">
          <button className="primary" disabled={busy} onClick={bookScenario}>Skenario gambar buku</button>
          <button disabled={busy} onClick={crossing}>Pesan bersilangan (concurrent)</button>
          <button disabled={busy} onClick={randomRun}>8 kejadian acak</button>
          <button disabled={busy} onClick={() => run(resetAll)}>Reset</button>
          <span className="muted">Label titik:</span>
          <select value={label} onChange={(e) => setLabel(e.target.value as typeof label)}>
            <option value="vector">vector clock</option>
            <option value="lamport">Lamport clock</option>
            <option value="wall">jam dinding node</option>
          </select>
        </div>
        <div className="row" style={{ marginTop: 10 }}>
          {PROCS.map((p) => (
            <div key={p} className="row" style={{ border: "1px solid var(--line)", borderRadius: 8, padding: "4px 8px" }}>
              <b style={{ color: COLORS[p] }}>{p}</b>
              <button disabled={busy} onClick={() => run(async () => { await local(p); })}>lokal</button>
              {PROCS.filter((q) => q !== p).map((q) => (
                <button key={q} disabled={busy} onClick={() => run(async () => { await send(p, q); })}>kirim ke {q}</button>
              ))}
            </div>
          ))}
          <label className="row">delay pesan
            <select value={delay} onChange={(e) => setDelay(Number(e.target.value))}>
              <option value={0}>0 ms (langsung)</option><option value={1000}>1000 ms</option><option value={2500}>2500 ms</option>
            </select>
          </label>
        </div>
        {err && <p className="err">{err}</p>}
      </div>

      <div className="card">
        <h2>Diagram ruang-waktu</h2>
        <div className="tablewrap">
          <svg width={W} height={H} role="img" aria-label="diagram ruang-waktu tiga proses">
            <defs>
              <marker id="arr" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                <path d="M0,0 L10,5 L0,10 z" fill="var(--accent)" />
              </marker>
            </defs>
            {PROCS.map((p) => (
              <g key={p}>
                <text x={10} y={y(p) + 4} style={{ fill: COLORS[p], fontWeight: 700 }}>{p}</text>
                <line x1={40} x2={W - 10} y1={y(p)} y2={y(p)} stroke="var(--line)" strokeWidth={2} />
              </g>
            ))}
            {sends.map((s) => {
              const from = pos.get(s.id)!;
              const r = recvByMsg.get(s.msg_id);
              if (!r) return <line key={s.id} x1={from.x} y1={from.y} x2={W - 20} y2={y(s.peer!)} stroke="var(--accent)" strokeDasharray="4 4" markerEnd="url(#arr)" />;
              const to = pos.get(r.id)!;
              return <line key={s.id} x1={from.x} y1={from.y} x2={to.x} y2={to.y} stroke="var(--accent)" strokeWidth={1.5} markerEnd="url(#arr)" />;
            })}
            {events.map((e, i) => {
              const s = sel.includes(e.id);
              return (
                <g key={e.id} onClick={() => toggle(e.id)} style={{ cursor: "pointer" }}>
                  <circle cx={x(i)} cy={y(e.proc)} r={s ? 9 : 6} fill={COLORS[e.proc]} stroke={s ? "var(--fg)" : "none"} strokeWidth={2} />
                  <text x={x(i)} y={y(e.proc) + (PROCS.indexOf(e.proc) === 0 ? -14 : 22)} textAnchor="middle" fontSize={10.5}>{lbl(e)}</text>
                </g>
              );
            })}
          </svg>
        </div>
        <p className="muted">Sumbu horizontal: urutan kejadian menurut jam host (pengamat di luar sistem). Garis putus-putus: pesan masih di jalan.</p>
      </div>

      <div className="grid g2">
        <div className="card">
          <h2>Bandingkan dua kejadian</h2>
          {a && b ? (() => {
            const c = compare(a.vector, b.vector);
            const verdict = c === "sebelum" ? `${a.id} happens-before ${b.id}` : c === "sesudah" ? `${b.id} happens-before ${a.id}` : c === "sama" ? "kejadian yang sama" : `${a.id} dan ${b.id} concurrent`;
            const lamportMisleading = c === "concurrent" && a.lamport !== b.lamport;
            return (
              <>
                <table><tbody>
                  <tr><th></th><th>{a.id} ({a.kind})</th><th>{b.id} ({b.kind})</th></tr>
                  <tr><td>Lamport</td><td>{a.lamport}</td><td>{b.lamport}</td></tr>
                  <tr><td>Vector</td><td>[{a.vector.join(", ")}]</td><td>[{b.vector.join(", ")}]</td></tr>
                  <tr><td>Jam dinding</td><td>{fmtTime(a.wall_ms)}</td><td>{fmtTime(b.wall_ms)}</td></tr>
                </tbody></table>
                <p className="note"><b>{verdict}</b> (menurut vector clock).</p>
                {lamportMisleading && <p className="warn">Lamport {a.lamport} vs {b.lamport} tampak berurutan, padahal keduanya concurrent: bila a → b maka C(a) &lt; C(b), tetapi kebalikannya tidak berlaku.</p>}
              </>
            );
          })() : <p className="muted">Klik dua titik di diagram. Contoh: pada skenario buku, bandingkan kejadian ketiga P1 (C=3) dengan kejadian pertama P3 (C=1).</p>}
        </div>
        <div className="card">
          <h2>Jam di tiap node</h2>
          <table><thead><tr><th>Node</th><th>Skew jam dinding</th><th>Lamport</th><th>Vector</th></tr></thead><tbody>
            {states.map((s) => s && (
              <tr key={s.node}><td style={{ color: COLORS[s.node], fontWeight: 700 }}>{s.node}</td><td>{s.skew_ms > 0 ? "+" : ""}{s.skew_ms} ms</td><td>{s.lamport}</td><td>[{s.vector.join(", ")}]</td></tr>
            ))}
          </tbody></table>
          {anomalies.length > 0 ? (
            <p className="bad">Jam dinding menyesatkan: {anomalies.map((e) => `${e.id} (terima dari ${e.peer})`).join(", ")} tercatat lebih awal daripada pengirimannya. Jam fisik tiap mesin bergeser; sebab-akibat harus dilacak dengan jam logis.</p>
          ) : <p className="muted">Kirim pesan dari P1 ke P2 (P2 jamnya mundur) untuk melihat pesan yang menurut jam dinding diterima sebelum dikirim.</p>}
        </div>
      </div>

      <div className="grid g2">
        <div className="card">
          <h2>Total order dari Lamport (C, lalu id proses)</h2>
          <ol>{lamportOrder.map((e) => <li key={e.id}><code>C={e.lamport}</code> {e.id} {e.kind}{e.peer ? ` ${e.kind === "send" ? "→" : "←"} ${e.peer}` : ""}</li>)}</ol>
        </div>
        <div className="card">
          <h2>Urutan menurut jam dinding</h2>
          <ol>{wallOrder.map((e) => <li key={e.id} className={anomalies.includes(e) ? "bad" : ""}><code>{fmtTime(e.wall_ms)}</code> {e.id} {e.kind}{e.peer ? ` ${e.kind === "send" ? "→" : "←"} ${e.peer}` : ""}</li>)}</ol>
        </div>
      </div>
    </>
  );
}
