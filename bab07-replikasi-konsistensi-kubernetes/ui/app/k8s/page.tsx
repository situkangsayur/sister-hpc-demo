"use client";
import { useState } from "react";
import { Down } from "@/components/Down";
import { fmtTime, post, usePoll } from "@/lib/poll";

type Pod = { name: string; phase: string; ready: boolean; deleting: boolean; version: string; ip: string; age_s: number; restarts: number; replicaset: string };
type Hit = { at_ms: number; ok: boolean; pod?: string; version?: string; err?: string; ms: number };
type St = {
  ready: boolean; init_error: string; target: string; hits: Hit[] | null; events: { at_ms: number; kind: string; msg: string }[] | null;
  pods?: Pod[] | null; error?: string; nodes?: { name: string; ready: boolean; version: string }[];
  deployment?: { desired: number; ready: number; updated: number; available: number; unavailable: number; version: string; readiness_probe: boolean };
  replicasets?: { name: string; version: string; desired: number; ready: number; revision: string }[] | null;
};

const VCOLOR: Record<string, string> = { v1: "var(--ok)", v2: "var(--info)", v3: "var(--p2)" };

export default function K8sPage() {
  const p = usePoll<St>("/api/k8s/status", 700);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  if (p.down) return (<><h1>Kubernetes dengan k3s</h1><Down profile="k8s" note="k3s butuh 30-60 detik untuk start dan menarik image pause; service k8s-watch lalu men-deploy aplikasi katalog." /></>);
  const s = p.data;
  const act = (body: object, done: string) => async () => {
    setBusy(true);
    try { await post("/api/k8s/action", body); setMsg(done); } catch (e) { setMsg("Gagal: " + String(e)); }
    setBusy(false); p.refresh();
  };
  if (s && !s.ready) return (<><h1>Kubernetes dengan k3s</h1><div className="card"><p>Menunggu k3s siap...</p><p className="muted">{s.init_error}</p></div></>);
  const hits = s?.hits ?? [];
  const now = hits.length ? hits[hits.length - 1].at_ms : 0;
  const recent = hits.filter((h) => h.at_ms > now - 10_000);
  const perPod = new Map<string, number>();
  recent.forEach((h) => h.ok && perPod.set(h.pod!, (perPod.get(h.pod!) ?? 0) + 1));
  const fails = hits.filter((h) => !h.ok).length;
  const d = s?.deployment;
  const nextVersion = d?.version === "v1" ? "v2" : d?.version === "v2" ? "v3" : "v1";

  return (
    <>
      <h1>Kubernetes dengan k3s</h1>
      <p className="lead">
        Bab 7 subbab Container dan Kubernetes serta Praktikum &quot;matikan pod, ukur waktu pulih&quot; (slide P07).
        k3s berjalan di dalam container. Service Go <code>k8s-watch</code> memakai client-go untuk membuat Deployment
        <code> katalog</code> dan Service NodePort, membaca status pod, dan mengirim beban 5 request/detik ke Service.
      </p>
      <div className="card">
        <div className="row">
          <b>Desired state:</b> <span>replicas {d?.desired}, versi {d?.version}, readinessProbe {d?.readiness_probe ? "on" : "off"}</span>
          <b style={{ marginLeft: 12 }}>Actual:</b> <span className={d && d.ready >= d.desired ? "ok" : "warn"}>{d?.ready ?? 0} ready, {d?.updated ?? 0} updated, {d?.unavailable ?? 0} unavailable</span>
          {s?.nodes?.map((n) => <span key={n.name} className="muted">node {n.name} ({n.version}) {n.ready ? "Ready" : "NotReady"}</span>)}
        </div>
        <div className="row" style={{ marginTop: 10 }}>
          <button className="danger" disabled={busy} onClick={act({ action: "delete-pod" }, "Satu pod dihapus. Amati pod pengganti muncul (Pending, lalu Ready) dan request yang tetap sukses.")}>Hapus satu pod</button>
          <span>Scale:</span>
          {[1, 2, 3, 5].map((n) => <button key={n} className={d?.desired === n ? "primary" : ""} disabled={busy} onClick={act({ action: "scale", replicas: n }, `Desired state diubah menjadi ${n} replica; controller menambah atau mengurangi pod.`)}>{n}</button>)}
          <button disabled={busy} onClick={act({ action: "rollout", version: nextVersion }, `Rolling update ke ${nextVersion}: pod baru dibuat satu per satu, pod lama dihentikan setelah pod baru Ready.`)}>Rolling update ke {nextVersion}</button>
          <button disabled={busy} onClick={act({ action: "readiness", enabled: !d?.readiness_probe }, d?.readiness_probe ? "readinessProbe dimatikan. Hapus pod lagi dan amati request gagal saat pod pengganti masih pemanasan." : "readinessProbe dinyalakan lagi.")}>readinessProbe: {d?.readiness_probe ? "matikan" : "nyalakan"}</button>
          <button disabled={busy} onClick={act({ action: "clear" }, "Riwayat request dikosongkan.")}>Kosongkan riwayat request</button>
        </div>
        {msg && <p className="note">{msg}</p>}
        {s?.error && <p className="err">{s.error}</p>}
      </div>

      <div className="card">
        <h2>Pod (label app=katalog)</h2>
        <div className="row" style={{ alignItems: "stretch" }}>
          {(s?.pods ?? []).map((pd) => {
            const st = pd.deleting ? "Terminating" : pd.ready ? "Ready" : pd.phase === "Running" ? "Running, belum Ready" : pd.phase;
            const c = pd.deleting ? "var(--bad)" : pd.ready ? VCOLOR[pd.version] ?? "var(--ok)" : "var(--warn)";
            return (
              <div key={pd.name} className="podcard" style={{ borderColor: c, borderWidth: 2 }}>
                <div><span className="dot" style={{ background: c }} /><b style={{ fontSize: 12 }}>{pd.name}</b></div>
                <div style={{ color: c, fontWeight: 600 }}>{st}</div>
                <div className="muted">versi {pd.version}, umur {pd.age_s}s, restart {pd.restarts}</div>
                <div className="muted">IP {pd.ip || "-"}</div>
                <div className="muted">request 10 dtk: {perPod.get(pd.name) ?? 0}</div>
                <button className="danger" style={{ marginTop: 4 }} disabled={busy || pd.deleting} onClick={act({ action: "delete-pod", pod: pd.name }, `Pod ${pd.name} dihapus.`)}>hapus</button>
              </div>
            );
          })}
        </div>
        {s?.replicasets && s.replicasets.length > 0 && (
          <div className="tablewrap"><table style={{ marginTop: 10 }}>
            <thead><tr><th>ReplicaSet</th><th>revision</th><th>versi</th><th>desired</th><th>ready</th></tr></thead>
            <tbody>{s.replicasets.map((r) => <tr key={r.name}><td>{r.name}</td><td>{r.revision}</td><td>{r.version}</td><td>{r.desired}</td><td>{r.ready}</td></tr>)}</tbody>
          </table></div>
        )}
      </div>

      <div className="card">
        <h2>Beban ke Service (5 request/detik, {s?.target})</h2>
        <p className="muted">Setiap batang adalah satu request, terbaru di kanan. Warna: versi pod yang menjawab (v1 hijau, v2 biru, v3 ungu); merah berarti gagal. Total gagal di riwayat: <b className={fails ? "bad" : "ok"}>{fails}</b> dari {hits.length}.</p>
        <div style={{ overflow: "hidden", whiteSpace: "nowrap", direction: "rtl" }}>
          <div style={{ direction: "ltr", display: "inline-block" }}>
            {hits.slice(-220).map((h, i) => <span key={i} className="tick" style={{ background: h.ok ? VCOLOR[h.version ?? ""] ?? "var(--ok)" : "var(--bad)" }} title={`${fmtTime(h.at_ms)} ${h.ok ? `${h.pod} ${h.version} ${h.ms} ms` : h.err}`} />)}
          </div>
        </div>
        {hits.filter((h) => !h.ok).slice(-3).map((h, i) => <div key={i} className="err" style={{ fontSize: 12 }}>{fmtTime(h.at_ms)} {h.err}</div>)}
      </div>

      <div className="card">
        <h2>Kejadian</h2>
        <ul>{[...(s?.events ?? [])].reverse().slice(0, 15).map((e, i) => <li key={i} className={e.kind === "pulih" ? "ok" : e.kind === "hapus" ? "bad" : ""}><code>{fmtTime(e.at_ms)}</code> {e.msg}</li>)}</ul>
      </div>
    </>
  );
}
