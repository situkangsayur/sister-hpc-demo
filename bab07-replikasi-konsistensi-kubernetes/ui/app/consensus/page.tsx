"use client";
import { useState } from "react";
import { Down } from "@/components/Down";
import { fmtTime, post, usePoll } from "@/lib/poll";

type Entry = { index: number; term: number; type: string; data: string };
type Node = {
  id: string; alive: boolean; state?: string; term?: string; leader_id?: string; commit_index?: string; applied_index?: string;
  last_log_index?: string; last_contact?: string; kv?: Record<string, string>; log?: Entry[];
  history: { at_ms: number; state: string; term: number }[];
};

const IDS = ["n1", "n2", "n3"];
const STATE_COLOR: Record<string, string> = { leader: "var(--accent)", follower: "var(--info)", candidate: "var(--warn)", shutdown: "var(--bad)", crash: "var(--bad)", start: "var(--muted)" };
const TERM_COLORS = ["#7f8c8d", "#2c6fbb", "#b8559a", "#d07a1c", "#1f8a4c", "#8e44ad", "#c0392b", "#16a085"];

export default function ConsensusPage() {
  const polls = [usePoll<Node>("/api/raft-n1/status", 500), usePoll<Node>("/api/raft-n2/status", 500), usePoll<Node>("/api/raft-n3/status", 500)];
  const [key, setKey] = useState("harga");
  const [val, setVal] = useState("12000");
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);
  if (polls.some((p) => p.down)) return (<><h1>Consensus dengan Raft</h1><Down profile="consensus" /></>);
  const nodes = polls.map((p) => p.data);
  const leader = nodes.find((n) => n?.alive && n.state === "leader");
  const alive = nodes.filter((n) => n?.alive).length;
  const run = async (f: () => Promise<string>) => { setBusy(true); try { setMsg(await f()); } catch (e) { setMsg("Gagal: " + String(e)); } setBusy(false); polls.forEach((p) => p.refresh()); };
  const maxIdx = Math.max(0, ...nodes.map((n) => Number(n?.last_log_index ?? 0)));
  const minIdx = Math.max(1, maxIdx - 24);
  const now = Date.now();
  const span = 90_000;

  const write = () => run(async () => {
    const target = nodes.find((n) => n?.alive && n.state !== "leader") ?? leader;
    if (!target) throw new Error("tidak ada node hidup");
    const r = await post<{ index: number; leader: string; via: string; commit_ms: number }>(`/api/raft-${target.id}/set`, { key, value: val });
    return `Ditulis lewat ${target.id}${r.via ? ` (diteruskan ke leader ${r.leader})` : ""}: entri log index ${r.index}, commit dalam ${r.commit_ms} ms setelah mayoritas mencatat.`;
  });

  return (
    <>
      <h1>Consensus dengan Raft</h1>
      <p className="lead">
        Bab 7 subbab Consensus dengan Raft (slide P07). Tiga node Go memakai <code>hashicorp/raft</code> untuk mereplikasi
        key-value store. Tulisan hanya masuk lewat leader, disalin ke follower, dan dianggap commit setelah mayoritas
        (2 dari 3) mencatatnya. Tombol &quot;matikan&quot; mensimulasikan crash: port Raft ditutup dan state di memori hilang;
        log tetap di disk.
      </p>
      <div className="card">
        <div className="row">
          <b>Kluster:</b>
          <span className={`badge ${alive >= 2 ? "ok" : "bad"}`}>{alive}/3 node hidup, quorum {alive >= 2 ? "terpenuhi" : "TIDAK terpenuhi"}</span>
          <span>leader: <b>{leader ? `${leader.id} (term ${leader.term})` : "belum ada, election berlangsung"}</b></span>
          <button className="danger" disabled={busy || !leader} onClick={() => run(async () => { await post(`/api/raft-${leader!.id}/admin/crash`); return `Leader ${leader!.id} dimatikan. Amati follower menjadi candidate lalu leader dengan term lebih tinggi.`; })}>Matikan leader</button>
        </div>
        <div className="row" style={{ marginTop: 10 }}>
          <span>Tulis:</span>
          <input value={key} onChange={(e) => setKey(e.target.value)} size={10} />
          <input value={val} onChange={(e) => setVal(e.target.value)} size={10} />
          <button className="primary" disabled={busy} onClick={write}>SET (kirim ke follower, diteruskan ke leader)</button>
        </div>
        {msg && <p className="note">{msg}</p>}
      </div>

      <div className="grid g3">
        {IDS.map((id, i) => {
          const n = nodes[i];
          const st = n?.alive ? n.state ?? "?" : "mati";
          return (
            <div className="card" key={id} style={{ borderColor: n?.alive ? STATE_COLOR[st] : "var(--bad)", borderWidth: 2 }}>
              <div className="row" style={{ justifyContent: "space-between" }}>
                <h2>{id}</h2>
                <span className="badge" style={{ color: n?.alive ? STATE_COLOR[st] : "var(--bad)" }}>{st}</span>
              </div>
              {n?.alive ? (
                <dl className="kv">
                  <dt>term</dt><dd>{n.term}</dd>
                  <dt>leader</dt><dd>{n.leader_id || "-"}</dd>
                  <dt>last log index</dt><dd>{n.last_log_index}</dd>
                  <dt>commit / applied</dt><dd>{n.commit_index} / {n.applied_index}</dd>
                  <dt>last contact</dt><dd>{n.last_contact}</dd>
                  <dt>state machine</dt><dd>{JSON.stringify(n.kv)}</dd>
                </dl>
              ) : <p className="muted">Node mati (crash). Log di disk masih ada.</p>}
              <div className="row" style={{ marginTop: 8 }}>
                <button className="danger" disabled={busy || !n?.alive} onClick={() => run(async () => { await post(`/api/raft-${id}/admin/crash`); return `${id} dimatikan.`; })}>matikan</button>
                <button disabled={busy || !!n?.alive} onClick={() => run(async () => { await post(`/api/raft-${id}/admin/restart`); return `${id} dihidupkan lagi dari log di disk; ia menjadi follower dan mengejar log leader.`; })}>hidupkan</button>
              </div>
            </div>
          );
        })}
      </div>

      <div className="card">
        <h2>Replicated log (index {minIdx} sampai {maxIdx})</h2>
        <p className="muted">Setiap kotak adalah satu entri; warna menandai term saat entri dibuat. Garis tebal di bawah kotak: sudah commit di node itu. Kotak kosong: node belum memiliki entri tersebut (tertinggal atau mati).</p>
        <div className="tablewrap">
          <table style={{ width: "auto" }}>
            <thead><tr><th>node</th>{Array.from({ length: maxIdx - minIdx + 1 }, (_, k) => <th key={k} style={{ textAlign: "center" }}>{minIdx + k}</th>)}</tr></thead>
            <tbody>
              {IDS.map((id, i) => {
                const n = nodes[i];
                const byIdx = new Map((n?.log ?? []).map((e) => [e.index, e]));
                const commit = Number(n?.commit_index ?? 0);
                return (
                  <tr key={id}>
                    <td><b>{id}</b></td>
                    {Array.from({ length: maxIdx - minIdx + 1 }, (_, k) => {
                      const e = byIdx.get(minIdx + k);
                      if (!e || !n?.alive) return <td key={k} style={{ textAlign: "center" }} className="muted">·</td>;
                      let cmd = e.type === "LogCommand" ? (() => { try { const c = JSON.parse(e.data); return `${c.key}=${c.value}`; } catch { return "cmd"; } })() : e.type === "LogNoop" ? "noop" : "config";
                      if (cmd.length > 14) cmd = cmd.slice(0, 13) + "…";
                      return (
                        <td key={k} style={{ textAlign: "center", padding: 2, minWidth: 70 }} title={`index ${e.index}, term ${e.term}, ${e.type} ${e.data}`}>
                          <div style={{ background: TERM_COLORS[e.term % TERM_COLORS.length], color: "#fff", borderRadius: 4, padding: "2px 4px", fontSize: 11, borderBottom: e.index <= commit ? "4px solid var(--fg)" : "4px solid transparent" }}>
                            t{e.term}<br />{cmd}
                          </div>
                        </td>
                      );
                    })}
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </div>

      <div className="card">
        <h2>Riwayat peran (90 detik terakhir)</h2>
        <svg viewBox="0 0 900 120" width="100%" role="img" aria-label="riwayat peran node">
          {IDS.map((id, i) => {
            const h = (nodes[i]?.history ?? []).filter((x) => x.at_ms > now - span * 3);
            return (
              <g key={id}>
                <text x={0} y={25 + i * 34} fontSize={12}>{id}</text>
                <rect x={40} y={12 + i * 34} width={850} height={20} fill="var(--line)" />
                {h.map((x, k) => {
                  const end = k + 1 < h.length ? h[k + 1].at_ms : now;
                  const a = Math.max(x.at_ms, now - span), b = end;
                  if (b < now - span) return null;
                  const px = 40 + ((a - (now - span)) / span) * 850, w = Math.max(2, ((b - a) / span) * 850);
                  const state = x.state === "start" ? "follower" : x.state;
                  return <rect key={k} x={px} y={12 + i * 34} width={w} height={20} fill={STATE_COLOR[state] ?? "var(--muted)"}><title>{`${state} sejak ${fmtTime(x.at_ms)}`}</title></rect>;
                })}
              </g>
            );
          })}
        </svg>
        <div className="row muted">
          {["follower", "candidate", "leader", "crash"].map((s) => <span key={s}><span className="dot" style={{ background: STATE_COLOR[s] }} />{s}</span>)}
        </div>
      </div>
    </>
  );
}
