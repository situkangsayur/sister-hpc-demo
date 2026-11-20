"use client";
// Waterfall per request: antre, DNS, connect, TLS, menunggu byte pertama (TTFB), dan unduh.
// Bentuknya sama dengan tab Network di DevTools browser dan gambar uraian waktu di buku Bab 3.

export type Timing = {
  url: string;
  proto: string;
  conn: number;
  reused: boolean;
  status: number;
  bytes: number;
  start: number;
  dns_start: number;
  dns_done: number;
  connect_start: number;
  connect_done: number;
  tls_start: number;
  tls_done: number;
  got_conn: number;
  ttfb: number;
  end: number;
  err?: string;
};

export const SEGMENTS = [
  { key: "queue", label: "antre (menunggu koneksi)", color: "var(--c-queue)" },
  { key: "dns", label: "DNS", color: "var(--c-dns)" },
  { key: "conn", label: "TCP connect", color: "var(--c-conn)" },
  { key: "tls", label: "TLS handshake (h3: QUIC+TLS)", color: "var(--c-tls)" },
  { key: "wait", label: "menunggu byte pertama (TTFB)", color: "var(--c-wait)" },
  { key: "dl", label: "unduh body", color: "var(--c-dl)" },
] as const;

type Seg = { key: string; from: number; to: number };

export function segments(t: Timing, isH3: boolean): Seg[] {
  const segs: Seg[] = [];
  const has = (x: number) => x >= 0;
  const clamp = (x: number) => Math.max(x, t.start);
  const opened = !t.reused && has(t.tls_start);
  let firstNet = t.got_conn >= 0 ? t.got_conn : t.end;
  if (opened) {
    const cands = [t.dns_start, t.connect_start, t.tls_start].filter(has);
    if (cands.length) firstNet = Math.min(firstNet, ...cands.map(clamp));
  }
  if (firstNet > t.start) segs.push({ key: "queue", from: t.start, to: firstNet });
  if (opened) {
    if (has(t.dns_start) && has(t.dns_done)) segs.push({ key: "dns", from: clamp(t.dns_start), to: clamp(t.dns_done) });
    if (!isH3 && has(t.connect_start) && has(t.connect_done))
      segs.push({ key: "conn", from: clamp(t.connect_start), to: clamp(t.connect_done) });
    if (has(t.tls_done)) segs.push({ key: "tls", from: clamp(t.tls_start), to: clamp(t.tls_done) });
  }
  const sent = Math.max(t.got_conn, opened ? t.tls_done : -1, t.start);
  const ttfb = t.ttfb >= 0 ? t.ttfb : t.end;
  if (ttfb > sent) segs.push({ key: "wait", from: sent, to: ttfb });
  if (t.end > ttfb) segs.push({ key: "dl", from: ttfb, to: t.end });
  return segs;
}

const color = (k: string) => SEGMENTS.find((s) => s.key === k)?.color ?? "#999";

function shortUrl(u: string) {
  try {
    const p = new URL(u);
    return p.pathname;
  } catch {
    return u;
  }
}

export function Legend() {
  return (
    <div className="legend">
      {SEGMENTS.map((s) => (
        <span key={s.key}>
          <i style={{ background: s.color }} />
          {s.label}
        </span>
      ))}
    </div>
  );
}

export function Waterfall({ requests, maxMs, isH3 }: { requests: Timing[]; maxMs: number; isH3: boolean }) {
  const scale = (x: number) => `${(100 * x) / maxMs}%`;
  const step = niceStep(maxMs);
  const ticks: number[] = [];
  for (let v = 0; v <= maxMs; v += step) ticks.push(v);
  return (
    <div className="wf">
      {requests.map((t, i) => {
        const segs = segments(t, isH3);
        const title = [
          `${shortUrl(t.url)}  koneksi #${t.conn}${t.reused ? " (dipakai ulang)" : " (baru)"}`,
          ...segs.map((s) => `${s.key}: ${(s.to - s.from).toFixed(1)} ms`),
          `total ${(t.end - t.start).toFixed(1)} ms, mulai ${t.start.toFixed(1)} ms`,
          t.err ? `error: ${t.err}` : "",
        ].join("\n");
        return (
          <div className="wf-row" key={i} title={title}>
            <div className="lbl">
              c{t.conn} {i === 0 ? "HTML /page" : shortUrl(t.url).replace("/asset/", "aset ")}
            </div>
            <div className="wf-track">
              {segs.map((s, j) => (
                <div
                  key={j}
                  className="wf-seg"
                  style={{ left: scale(s.from), width: `max(1px, ${scale(s.to - s.from)})`, background: color(s.key) }}
                />
              ))}
              {t.err && <span className="bad" style={{ position: "absolute", left: scale(t.end), fontSize: 10 }}>error</span>}
            </div>
          </div>
        );
      })}
      <div className="wf-axis">
        <div />
        <div className="ticks">
          {ticks.map((v) => (
            <span key={v} style={{ left: scale(v) }}>
              {v} ms
            </span>
          ))}
        </div>
      </div>
    </div>
  );
}

function niceStep(max: number) {
  const raw = max / 8;
  const p = Math.pow(10, Math.floor(Math.log10(Math.max(raw, 1))));
  for (const m of [1, 2, 5, 10]) if (raw <= m * p) return m * p;
  return 10 * p;
}
