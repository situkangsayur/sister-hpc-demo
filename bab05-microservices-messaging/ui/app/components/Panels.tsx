"use client";

import { useState } from "react";
import { Faults, Health, LabStats, Order, OSInfo, postJSON, ReplicaBreakers, rupiah, Saga, shortTime, usePoll } from "../lib";

export function HealthBar() {
  const { data, error } = usePoll<Health[]>("monitor/health", 2000);
  return (
    <section className="health">
      {error && <span className="err">monitor tidak terjangkau: {error}</span>}
      {data?.map((h) => (
        <div key={h.name} className={`hcard h-${h.status}`} title={h.detail ?? ""}>
          <div className="hname">{h.name}</div>
          <div className="hgroup">{h.group}</div>
          <div className="hstat">
            {h.status === "simulated-down" ? "DOWN (disuntik)" : h.status.toUpperCase()} · {h.latency_ms} ms
          </div>
          {h.detail && h.status === "up" && h.group.includes("replica") && <div className="hgroup">{h.detail}</div>}
        </div>
      ))}
    </section>
  );
}

const STEP_LABEL: Record<string, string> = {
  T1: "T1 buat pesanan",
  T2: "T2 pesan stok",
  T3: "T3 tagih",
  T4: "T4 konfirmasi",
  C2: "C2 lepas stok",
  C1: "C1 batalkan",
  N: "notifikasi",
};

export function SagaPanel({ onSelect, selected }: { onSelect: (id: string) => void; selected: string }) {
  const sagas = usePoll<Saga[]>("monitor/saga", 1000);
  const orders = usePoll<{ orders: Order[] }>("orders", 2000);
  const byId = new Map((orders.data?.orders ?? []).map((o) => [o.id, o]));
  return (
    <section className="panel wide">
      <h2>Status saga per order (choreography)</h2>
      <p className="hint">
        Jalur hijau: T1 → T2 → T3 → T4 → notifikasi. Bila T3 gagal, kompensasi berjalan terbalik: C2 (inventory melepas stok) lalu C1 (order dibatalkan). Klik
        baris untuk memfilter timeline.
      </p>
      <table className="saga">
        <thead>
          <tr>
            <th>order</th>
            {Object.keys(STEP_LABEL).map((s) => (
              <th key={s}>{STEP_LABEL[s]}</th>
            ))}
            <th>hasil</th>
            <th>dup/redeliv</th>
            <th>dilayani</th>
          </tr>
        </thead>
        <tbody>
          {(sagas.data ?? []).slice(0, 12).map((s) => {
            const o = byId.get(s.order_id);
            return (
              <tr key={s.order_id} className={selected === s.order_id ? "sel" : ""} onClick={() => onSelect(s.order_id)}>
                <td className="mono small">
                  {s.order_id}
                  <div className="faint">{shortTime(s.first_seen)}</div>
                </td>
                {Object.keys(STEP_LABEL).map((k) => (
                  <td key={k}>
                    <span className={`step st-${s.steps[k]}`}>{s.steps[k] === "ok" ? "✓" : s.steps[k] === "failed" ? "✗" : "·"}</span>
                  </td>
                ))}
                <td>
                  <b className={s.outcome.startsWith("CONFIRMED") ? "ok" : s.outcome.startsWith("CANCELLED") ? "bad" : "warn"}>{s.outcome}</b>
                  {o?.reason && <div className="small faint">{o.reason}</div>}
                </td>
                <td className="small">
                  {s.duplicates}/{s.redeliveries}
                </td>
                <td className="small">
                  {o?.served_by ?? "-"}
                  {o && <div className="faint">{rupiah(o.total)}</div>}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      {(sagas.data ?? []).length === 0 && <div className="small faint">belum ada order</div>}
    </section>
  );
}

export function BreakerPanel() {
  const { data } = usePoll<ReplicaBreakers[]>("monitor/breakers", 1000);
  return (
    <section className="panel">
      <h2>Circuit breaker (per replica order)</h2>
      <p className="hint">Breaker hidup di memori setiap replica; nginx membagi request, jadi kedua replica bisa berbeda state.</p>
      <div className="breakers">
        {data?.map((r) => (
          <div key={r.replica} className="brep">
            <div className="small">
              <b>{r.replica}</b> {r.up === false ? <span className="bad">(port publik ditutup)</span> : null} {r.error ? <span className="bad">{r.error}</span> : null}
            </div>
            {r.breakers?.map((b) => (
              <div key={b.name} className={`breaker b-${b.state.replace(" ", "-")}`}>
                <div className="bname">
                  → {b.name} <span className="bstate">{b.state.toUpperCase()}</span>
                </div>
                <div className="small">
                  timeout {b.timeout}, maks {b.attempts} percobaan · jendela: {b.requests} req, {b.failures} gagal, {b.successes} sukses · state sejak{" "}
                  {Math.round(b.since_ms / 1000)} s
                </div>
                {b.transitions.length > 0 && <div className="small faint mono">{b.transitions.slice(-3).join(" | ")}</div>}
              </div>
            ))}
          </div>
        ))}
      </div>
    </section>
  );
}

export function FaultPanel() {
  const { data, refresh } = usePoll<Faults>("monitor/faults", 2000);
  const [msg, setMsg] = useState("");
  const send = async (path: string, body: unknown, label: string) => {
    await postJSON(path, body);
    setMsg(label);
    refresh();
  };
  const burst = async () => {
    const r = await fetch("/api/burst", { method: "POST", body: JSON.stringify({ n: 30 }) });
    const j = (await r.json()) as { sent: number; by_status: Record<string, number> };
    setMsg(`burst ${j.sent} request pencarian: ` + Object.entries(j.by_status).map(([k, v]) => `HTTP ${k} × ${v}`).join(", "));
  };
  const p = data?.payment;
  const inv = data?.inventory;
  return (
    <section className="panel">
      <h2>Suntik kegagalan</h2>
      <div className="fault">
        <b>Payment</b> gagal: {p ? `${Math.round(p.fail_rate * 100)}%` : "?"}
        <div className="row wrap">
          {[0, 0.5, 1].map((v) => (
            <button key={v} className={p?.fail_rate === v ? "on" : ""} onClick={() => send("monitor/faults/payment", { fail_rate: v }, `payment fail_rate=${v}`)}>
              {v * 100}%
            </button>
          ))}
        </div>
      </div>
      <div className="fault">
        <b>Inventory lambat</b>: {inv ? `${inv.delay_ms} ms` : "?"} (timeout order→inventory 300 ms)
        <div className="row wrap">
          {[0, 800, 2000].map((v) => (
            <button key={v} className={inv?.delay_ms === v ? "on" : ""} onClick={() => send("monitor/faults/inventory", { delay_ms: v }, `inventory delay=${v} ms`)}>
              {v} ms
            </button>
          ))}
        </div>
      </div>
      <div className="fault">
        <b>Inventory: crash sebelum ack</b> (at-least-once → redelivery)
        <div className="row wrap">
          <button className={inv?.crash_before_ack ? "on" : ""} onClick={() => send("monitor/faults/inventory", { crash_before_ack: !inv?.crash_before_ack }, "crash_before_ack diubah")}>
            {inv?.crash_before_ack ? "AKTIF" : "mati"}
          </button>
          <span className="small">consumer idempotent (dedup):</span>
          <button className={inv?.idempotent ? "on" : "danger"} onClick={() => send("monitor/faults/inventory", { idempotent: !inv?.idempotent }, "idempotent diubah")}>
            {inv?.idempotent ? "ON" : "OFF (efek ganda!)"}
          </button>
        </div>
      </div>
      <div className="fault">
        <b>Replica order</b> (di belakang nginx)
        <div className="row wrap">
          {["order-1", "order-2"].map((n) => {
            const up = data?.replicas?.[n]?.up;
            return (
              <button key={n} className={up === false ? "danger" : ""} onClick={() => send(`monitor/faults/replica/${n}`, { down: up !== false }, `${n} down=${up !== false}`)}>
                {n}: {up === false ? "hidupkan" : "matikan"}
              </button>
            );
          })}
        </div>
      </div>
      <div className="fault">
        <b>Gateway</b>
        <div className="row wrap">
          <button onClick={burst}>Burst 30 pencarian (rate limit 20/10 s)</button>
        </div>
      </div>
      <div className="row wrap">
        <button className="ghost" onClick={() => send("monitor/faults/reset", {}, "semua fault dimatikan")}>Reset semua fault</button>
        <button className="ghost" onClick={() => send("monitor/faults/reset-stock", {}, "stok dikembalikan ke 10")}>Reset stok</button>
      </div>
      {msg && <div className="small meta">{msg}</div>}
    </section>
  );
}

export function OpenSearchPanel() {
  const { data } = usePoll<OSInfo>("monitor/opensearch", 3000);
  const h = data?.health;
  return (
    <section className="panel">
      <h2>OpenSearch (2 node)</h2>
      <div className="meta">
        cluster <b className={h?.status === "green" ? "ok" : h?.status === "yellow" ? "warn" : "bad"}>{h?.status ?? "tidak terjangkau"}</b>
        {h && (
          <>
            {" "}
            · {h.number_of_nodes} node · {h.active_shards} shard aktif · {h.unassigned_shards} unassigned
          </>
        )}
      </div>
      <table className="mini-t">
        <tbody>
          {data?.nodes?.map((n) => (
            <tr key={n.name}>
              <td>
                <b>{n.name}</b> {n.cluster_manager === "*" ? "(cluster manager)" : ""}
              </td>
              <td className="small">
                heap {n["heap.percent"]}% dari {n["heap.max"]}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="small">Index products: 2 primary shard × 1 replica. Primary (p) dan replica (r) dari shard yang sama selalu di node berbeda.</div>
      <div className="shards">
        {["opensearch-1", "opensearch-2"].map((node) => (
          <div key={node} className="shnode">
            <div className="small">
              <b>{node}</b>
            </div>
            {data?.shards
              ?.filter((s) => s.node === node)
              .map((s) => (
                <span key={s.shard + s.prirep} className={`shard ${s.prirep === "p" ? "pri" : "rep"}`}>
                  {s.shard}
                  {s.prirep} · {s.docs ?? 0}
                </span>
              ))}
          </div>
        ))}
        {data?.shards?.some((s) => !s.node) && <div className="small warn">ada shard UNASSIGNED (node mati?)</div>}
      </div>
    </section>
  );
}

export function LabPanel() {
  const { data, refresh } = usePoll<LabStats>("monitor/lab/stats", 1000);
  const send = async (path: string, body: unknown) => {
    await postJSON(path, body);
    refresh();
  };
  const rc = data?.receiver?.received ?? {};
  const offline = data?.receiver?.offline;
  const coreWorkers = (rc["worker-a"] ?? 0) + (rc["worker-b"] ?? 0);
  return (
    <section className="panel">
      <h2>Lab delivery semantics</h2>
      <p className="hint">
        Core NATS: fire-and-forget, tanpa penyimpanan (at-most-once). JetStream: disimpan di stream, consumer mengirim ack (at-least-once). Queue group membagi pesan;
        subscriber biasa menerima semuanya (fan-out).
      </p>
      <div className="row wrap">
        <button className={offline ? "danger" : ""} onClick={() => send("monitor/lab/offline", { offline: !offline })}>
          penerima: {offline ? "OFFLINE (klik untuk online)" : "online (klik untuk offline)"}
        </button>
        <button onClick={() => send("monitor/lab/publish", { mode: "core", n: 10 })}>Kirim 10 via core NATS</button>
        <button onClick={() => send("monitor/lab/publish", { mode: "js", n: 10 })}>Kirim 10 via JetStream</button>
        <button className="ghost" onClick={() => send("monitor/lab/reset", {})}>reset</button>
      </div>
      <table className="mini-t">
        <thead>
          <tr>
            <th></th>
            <th>dikirim</th>
            <th>diterima</th>
            <th>catatan</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <td>core · queue group lab-workers</td>
            <td>{data?.published.core ?? 0}</td>
            <td>
              {coreWorkers} (A {rc["worker-a"] ?? 0} / B {rc["worker-b"] ?? 0})
            </td>
            <td className="small">{(data?.published.core ?? 0) - coreWorkers > 0 ? <b className="bad">{(data?.published.core ?? 0) - coreWorkers} hilang</b> : "dibagi ke 2 worker"}</td>
          </tr>
          <tr>
            <td>core · subscriber audit-log</td>
            <td>{data?.published.core ?? 0}</td>
            <td>{rc["audit-log"] ?? 0}</td>
            <td className="small">fan-out: menerima semua salinan</td>
          </tr>
          <tr>
            <td>JetStream · durable lab-worker</td>
            <td>{data?.published.js ?? 0}</td>
            <td>{rc["js-worker"] ?? 0}</td>
            <td className="small">{data?.js_pending ? <b className="warn">{data.js_pending} menunggu di stream</b> : "tidak ada yang hilang"}</td>
          </tr>
        </tbody>
      </table>
    </section>
  );
}

export function NotificationPanel() {
  const { data } = usePoll<{ order_id: string; kind: string; message: string; at: string }[]>("monitor/notifications", 2000);
  return (
    <section className="panel">
      <h2>Surel terkirim (notification)</h2>
      <div className="notifs">
        {(data ?? []).slice(0, 8).map((n) => (
          <div key={n.order_id + n.kind} className={`small ${n.kind === "confirmed" ? "ok" : "bad"}`}>
            {shortTime(n.at)} · {n.message}
          </div>
        ))}
        {(data ?? []).length === 0 && <div className="small faint">belum ada</div>}
      </div>
    </section>
  );
}
