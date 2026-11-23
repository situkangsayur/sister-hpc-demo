"use client";

import { useEffect, useRef, useState } from "react";
import { Ev, getJSON, shortTime } from "../lib";

const LANES = ["order", "inventory", "payment", "bank", "notification", "monitor"];

function laneIndex(s: string) {
  const i = LANES.indexOf(s);
  return i < 0 ? LANES.length - 1 : i;
}

function subjectClass(e: Ev) {
  if (e.kind === "lab") return "s-lab";
  if (e.kind === "audit") {
    if (e.action === "duplicate") return "s-dup";
    if (e.action === "crash" || e.action === "error") return "s-bad";
    if (e.action === "redelivered") return "s-warn";
    if (e.action === "breaker" || e.action === "replica") return "s-warn";
    return "s-audit";
  }
  if (e.subject.startsWith("bank.")) return "s-bank";
  if (/failed|rejected|cancelled|released/.test(e.subject)) return "s-bad";
  return "s-ok";
}

// Timeline event live: setiap publish ke JetStream dan setiap catatan consumer (audit).
export default function Timeline({ filter, setFilter }: { filter: string; setFilter: (s: string) => void }) {
  const [events, setEvents] = useState<Ev[]>([]);
  const [showLab, setShowLab] = useState(false);
  const [showAudit, setShowAudit] = useState(true);
  const [showShop, setShowShop] = useState(true);
  const [showBank, setShowBank] = useState(true);
  const [paused, setPaused] = useState(false);
  const last = useRef(0);

  useEffect(() => {
    if (paused) return;
    const tick = () =>
      getJSON<{ events: Ev[]; last_seq: number }>(`monitor/events?since=${last.current}`)
        .then((r) => {
          if (r.last_seq < last.current) last.current = 0; // monitor restart
          if (r.events.length) {
            last.current = r.last_seq;
            setEvents((p) => [...r.events.reverse(), ...p].slice(0, 400));
          }
        })
        .catch(() => {});
    tick();
    const t = setInterval(tick, 1000);
    return () => clearInterval(t);
  }, [paused]);

  const shown = events.filter(
    (e) =>
      (showLab || e.kind !== "lab") &&
      (showAudit || e.kind !== "audit") &&
      (showShop || !e.subject.startsWith("shop.")) &&
      (showBank || !(e.subject.startsWith("bank.") || e.from === "bank")) &&
      (!filter || e.order_id === filter || e.request_id === filter),
  );

  return (
    <section className="panel wide">
      <h2>Timeline event NATS (live)</h2>
      <p className="hint">
        Baris <span className="tag s-ok">publish</span> = event diterbitkan ke JetStream (dari outbox). Baris <span className="tag s-audit">audit</span> = consumer
        memprosesnya; <span className="tag s-warn">redelivered</span>, <span className="tag s-dup">duplicate</span>, <span className="tag s-bad">crash</span> memperlihatkan
        delivery semantics. <span className="tag s-bank">bank.*</span> = event stream BANK (transaksi, nasabah, rekening, akad); transaksi bayar_belanja ikut tersaring bersama
        order yang dibayarnya.
      </p>
      <div className="row wrap">
        <label>
          <input type="checkbox" checked={showAudit} onChange={(e) => setShowAudit(e.target.checked)} /> audit consumer
        </label>
        <label>
          <input type="checkbox" checked={showShop} onChange={(e) => setShowShop(e.target.checked)} /> event SHOP
        </label>
        <label>
          <input type="checkbox" checked={showBank} onChange={(e) => setShowBank(e.target.checked)} /> event BANK
        </label>
        <label>
          <input type="checkbox" checked={showLab} onChange={(e) => setShowLab(e.target.checked)} /> pesan lab
        </label>
        <button className="mini" onClick={() => setPaused((p) => !p)}>{paused ? "lanjutkan" : "jeda"}</button>
        <button className="mini" onClick={() => setEvents([])}>bersihkan</button>
        {filter && (
          <span className="filter">
            filter: <code>{filter}</code> <button className="mini" onClick={() => setFilter("")}>×</button>
          </span>
        )}
      </div>
      <div className="tl">
        <div className="tl-head">
          <span>waktu</span>
          <span>event</span>
          {LANES.map((l) => (
            <span key={l} className="lane-h">{l}</span>
          ))}
          <span>keterangan</span>
        </div>
        {shown.slice(0, 150).map((e) => {
          const from = laneIndex(e.from);
          const tos = (e.to ?? []).map(laneIndex);
          const all = [from, ...tos];
          const lo = Math.min(...all);
          const hi = Math.max(...all);
          return (
            <div key={e.seq} className="tl-row">
              <span className="small mono">{shortTime(e.at)}</span>
              <span>
                <span className={`tag ${subjectClass(e)}`}>{e.kind === "audit" ? e.action : e.kind}</span>{" "}
                <span className="mono small">{e.subject.replace(/^shop\./, "")}</span>
              </span>
              {LANES.map((l, i) => (
                <span key={l} className={`lane ${i >= lo && i <= hi && tos.length ? "bar" : ""} ${i === lo && tos.length ? "bar-start" : ""} ${i === hi && tos.length ? "bar-end" : ""}`}>
                  {i === from && <span className={`dot ${e.kind === "audit" ? "dot-audit" : "dot-pub"}`} title={e.replica ?? e.from} />}
                  {tos.includes(i) && i !== from && <span className="arrow">▼</span>}
                </span>
              ))}
              <span className="small">
                {e.order_id && (
                  <a className="link" onClick={() => setFilter(e.order_id!)}>
                    {e.order_id}
                  </a>
                )}{" "}
                {e.replica && <b>[{e.replica}]</b>} {e.delivered && e.delivered > 1 ? <b className="warn">pengiriman ke-{e.delivered} </b> : null}
                {e.note}
                {e.request_id && <span className="mono faint"> rid {e.request_id.slice(0, 8)}</span>}
              </span>
            </div>
          );
        })}
        {shown.length === 0 && <div className="small faint">belum ada event, lakukan checkout</div>}
      </div>
    </section>
  );
}
