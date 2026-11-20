"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import LineChart from "@/components/LineChart";
import Histogram from "@/components/Histogram";
import { app, inWails, on } from "@/lib/bridge";
import type { Bin, Config, GRPCMethodInfo, Kind, Point, Preset, Report } from "@/lib/types";

const KINDS: { k: Kind; label: string }[] = [
  { k: "http", label: "REST/HTTP" },
  { k: "graphql", label: "GraphQL" },
  { k: "grpc", label: "gRPC" },
  { k: "ws", label: "WebSocket" },
];

const DEFAULT_CFG: Config = {
  name: "kustom",
  kind: "http",
  url: "http://${BIND_ADDR}:50101/ping",
  method: "GET",
  concurrency: 10,
  duration: "15s",
  requests: 0,
  rps: 0,
  timeout: "5s",
};

function headersToText(h?: Record<string, string>): string {
  return Object.entries(h ?? {}).map(([k, v]) => `${k}: ${v}`).join("\n");
}

function textToHeaders(t: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of t.split("\n")) {
    const i = line.indexOf(":");
    if (i > 0) out[line.slice(0, i).trim()] = line.slice(i + 1).trim();
  }
  return out;
}

// Gabungkan skenario dengan nilai default; skenario berbasis jumlah request tidak diberi durasi default.
function withDefaults(c: Config, name?: string): Config {
  const duration = c.duration ?? (c.requests ? "" : DEFAULT_CFG.duration);
  return { ...DEFAULT_CFG, method: "GET", ...c, duration, name: name ?? c.name ?? "kustom" };
}

function n(v: number, d = 1): string {
  if (!isFinite(v)) return "-";
  return v.toLocaleString("id-ID", { maximumFractionDigits: d, minimumFractionDigits: d });
}

export default function Page() {
  const [cfg, setCfg] = useState<Config>(DEFAULT_CFG);
  const [headersText, setHeadersText] = useState("");
  const [mode, setMode] = useState<"durasi" | "jumlah">("durasi");
  const [host, setHost] = useState("127.0.0.1");
  const [presets, setPresets] = useState<Preset[]>([]);
  const [presetIdx, setPresetIdx] = useState("");
  const [running, setRunning] = useState(false);
  const [points, setPoints] = useState<Point[]>([]);
  const [bins, setBins] = useState<Bin[]>([]);
  const [report, setReport] = useState<Report | null>(null);
  const [msg, setMsg] = useState<{ kind: "err" | "info"; text: string } | null>(null);
  const [methods, setMethods] = useState<GRPCMethodInfo[]>([]);
  const [desktop, setDesktop] = useState(true);

  useEffect(() => {
    if (!inWails()) {
      setDesktop(false);
      return;
    }
    app().Presets().then(setPresets).catch((e) => setMsg({ kind: "err", text: String(e) }));
    app().DefaultHost().then(setHost).catch(() => {});
    app()
      .InitialConfig()
      .then((ini) => {
        if (!ini) return;
        const c = withDefaults(ini.config);
        setCfg(c);
        setHeadersText(headersToText(c.headers));
        setMode(c.requests && !c.duration ? "jumlah" : "durasi");
        if (ini.autostart) {
          setRunning(true);
          app()
            .DefaultHost()
            .then((h) => app().Start(c, h))
            .catch((e) => {
              setRunning(false);
              setMsg({ kind: "err", text: String(e) });
            });
        }
      })
      .catch(() => {});
    const offs = [
      on<Point>("tick", (p) => setPoints((ps) => [...ps, p])),
      on<Bin[]>("hist", (b) => setBins(b)),
      on<Report>("done", (r) => {
        setReport(r);
        setBins(r.histogram);
        setRunning(false);
        setMsg({ kind: "info", text: r.stopped ? "Run dihentikan." : `Selesai dalam ${n(r.elapsedS)} detik.` });
      }),
      on<string>("failed", (e) => {
        setRunning(false);
        setMsg({ kind: "err", text: e });
      }),
    ];
    return () => offs.forEach((f) => f());
  }, []);

  const set = <K extends keyof Config>(k: K, v: Config[K]) => setCfg((c) => ({ ...c, [k]: v }));

  const applyPreset = (idx: string) => {
    setPresetIdx(idx);
    const p = presets[Number(idx)];
    if (!p) return;
    const c = withDefaults(p.config, p.name);
    setCfg(c);
    setHeadersText(headersToText(c.headers));
    setMode(c.requests && !c.duration ? "jumlah" : "durasi");
    setMethods([]);
  };

  const start = useCallback(async () => {
    const c: Config = { ...cfg, headers: textToHeaders(headersText) };
    if (mode === "durasi") c.requests = 0;
    else c.duration = "";
    setPoints([]);
    setBins([]);
    setReport(null);
    setMsg(null);
    try {
      setRunning(true);
      await app().Start(c, host);
    } catch (e) {
      setRunning(false);
      setMsg({ kind: "err", text: String(e) });
    }
  }, [cfg, headersText, mode, host]);

  const loadMethods = async () => {
    try {
      const ms = await app().GRPCMethods(cfg.target ?? "", !!cfg.tls, !!cfg.insecureSkipVerify, host);
      setMethods(ms);
      setMsg({ kind: "info", text: `${ms.length} method unary ditemukan lewat server reflection.` });
    } catch (e) {
      setMsg({ kind: "err", text: String(e) });
    }
  };

  const doExport = async (f: "csv" | "json") => {
    try {
      const path = await app().Export(f);
      if (path) setMsg({ kind: "info", text: `Tersimpan: ${path}` });
    } catch (e) {
      setMsg({ kind: "err", text: String(e) });
    }
  };

  const loadPresetFile = async () => {
    try {
      const ps = await app().LoadPresetFile();
      if (ps.length) {
        setPresets(ps);
        setPresetIdx("");
        setMsg({ kind: "info", text: `${ps.length} preset dimuat.` });
      }
    } catch (e) {
      setMsg({ kind: "err", text: String(e) });
    }
  };

  const xs = points.map((p) => p.t);
  const errTotals = useMemo(() => {
    if (report) return report.errorsByKind ?? {};
    const acc: Record<string, number> = {};
    for (const p of points) for (const [k, v] of Object.entries(p.errorsByKind ?? {})) acc[k] = (acc[k] ?? 0) + v;
    return acc;
  }, [points, report]);
  const last = points[points.length - 1];
  const total = report ? report.total : (last?.total ?? 0);
  const errs = report ? report.errors : (last?.errSum ?? 0);
  const groups = useMemo(() => {
    const g = new Map<number, { p: Preset; i: number }[]>();
    presets.forEach((p, i) => g.set(p.bab, [...(g.get(p.bab) ?? []), { p, i }]));
    return [...g.entries()].sort((a, b) => (a[0] || 99) - (b[0] || 99));
  }, [presets]);

  return (
    <>
      <header>
        <h1>
          Perftest<small>Sistem Terdistribusi · STMIK Tazkia</small>
        </h1>
        <span className="hdr-field">
          <span className="muted">Host (BIND_ADDR)</span>
          <input value={host} onChange={(e) => setHost(e.target.value)} style={{ width: 130 }} disabled={running} />
        </span>
        <span className="hdr-field">
          <span className="muted">Preset</span>
          <select value={presetIdx} onChange={(e) => applyPreset(e.target.value)} disabled={running}>
            <option value="">— pilih endpoint bab —</option>
            {groups.map(([bab, items]) => (
              <optgroup key={bab} label={bab ? `Bab ${bab}` : "Umum"}>
                {items.map(({ p, i }) => (
                  <option key={i} value={i}>
                    {p.name}
                  </option>
                ))}
              </optgroup>
            ))}
          </select>
        </span>
        <button className="btn" onClick={loadPresetFile} disabled={running || !desktop}>
          Muat preset YAML
        </button>
        <span style={{ flex: 1 }} />
        <span className={running ? "status run" : "status"}>{running ? "berjalan" : report ? "selesai" : "siap"}</span>
      </header>
      {!desktop && (
        <div className="msg err" style={{ margin: 12 }}>
          Halaman ini adalah frontend aplikasi desktop. Jalankan binary perftest-desktop (lihat README) agar terhubung ke mesin Go.
        </div>
      )}
      <main>
        <section className="card">
          <div className="card-head">Target</div>
          <div className="tabs">
            {KINDS.map(({ k, label }) => (
              <button key={k} className={cfg.kind === k ? "on" : ""} onClick={() => set("kind", k)} disabled={running}>
                {label}
              </button>
            ))}
          </div>

          {(cfg.kind === "http" || cfg.kind === "graphql" || cfg.kind === "ws") && (
            <>
              <label>URL</label>
              <input value={cfg.url ?? ""} onChange={(e) => set("url", e.target.value)} placeholder={cfg.kind === "ws" ? "ws://${BIND_ADDR}:50202/ws" : "http://${BIND_ADDR}:50401/v1/produk"} />
            </>
          )}
          {cfg.kind === "http" && (
            <>
              <label>Method</label>
              <select value={cfg.method ?? "GET"} onChange={(e) => set("method", e.target.value)}>
                {["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"].map((m) => (
                  <option key={m}>{m}</option>
                ))}
              </select>
            </>
          )}
          {cfg.kind !== "grpc" && (
            <>
              <label>Header (satu per baris, Nama: nilai)</label>
              <textarea rows={2} value={headersText} onChange={(e) => setHeadersText(e.target.value)} placeholder="X-Request-Id: perftest" />
            </>
          )}
          {cfg.kind === "http" && (
            <>
              <label>Body</label>
              <textarea rows={3} value={cfg.body ?? ""} onChange={(e) => set("body", e.target.value)} placeholder='{"nama":"buku"}' />
            </>
          )}
          {cfg.kind === "graphql" && (
            <>
              <label>Query</label>
              <textarea rows={4} value={cfg.query ?? ""} onChange={(e) => set("query", e.target.value)} placeholder="{ daftarProduk { id nama } }" />
              <label>Variables (JSON)</label>
              <textarea rows={2} value={cfg.variables ?? ""} onChange={(e) => set("variables", e.target.value)} placeholder='{"id": 1}' />
            </>
          )}
          {cfg.kind === "grpc" && (
            <>
              <label>Target (host:port)</label>
              <div className="row" style={{ gridTemplateColumns: "1fr auto" }}>
                <input value={cfg.target ?? ""} onChange={(e) => set("target", e.target.value)} placeholder="${BIND_ADDR}:50401" />
                <button className="btn" onClick={loadMethods} disabled={running || !desktop}>
                  Reflection
                </button>
              </div>
              {methods.length > 0 && (
                <div className="methods">
                  {methods.map((m) => (
                    <div key={m.method} title={`${m.input} → ${m.output}`} onClick={() => setCfg((c) => ({ ...c, grpcMethod: m.method, request: m.example }))}>
                      {m.method}
                    </div>
                  ))}
                </div>
              )}
              <label>Method (paket.Service/Method)</label>
              <input value={cfg.grpcMethod ?? ""} onChange={(e) => set("grpcMethod", e.target.value)} placeholder="katalog.v1.KatalogService/AmbilProduk" />
              <label>Request (JSON)</label>
              <textarea rows={3} value={cfg.request ?? ""} onChange={(e) => set("request", e.target.value)} placeholder='{"id": 1}' />
              <label className="check">
                <input type="checkbox" checked={!!cfg.tls} onChange={(e) => set("tls", e.target.checked)} /> TLS
              </label>
            </>
          )}
          {cfg.kind === "ws" && (
            <>
              <label>Pesan yang dikirim</label>
              <textarea rows={2} value={cfg.message ?? ""} onChange={(e) => set("message", e.target.value)} placeholder="halo" />
              <label className="check">
                <input type="checkbox" checked={!cfg.noReply} onChange={(e) => set("noReply", !e.target.checked)} /> tunggu satu pesan balasan (latency = round trip)
              </label>
            </>
          )}
          {(cfg.kind !== "grpc" || cfg.tls) && (
            <label className="check">
              <input type="checkbox" checked={!!cfg.insecureSkipVerify} onChange={(e) => set("insecureSkipVerify", e.target.checked)} /> terima sertifikat self-signed (lab)
            </label>
          )}

          <div className="card-head" style={{ marginTop: 14 }}>
            Beban
          </div>
          <div className="row">
            <div>
              <label>Concurrency (worker)</label>
              <input type="number" min={1} max={5000} value={cfg.concurrency} onChange={(e) => set("concurrency", Number(e.target.value))} />
            </div>
            <div>
              <label>Target RPS (0 = maks)</label>
              <input type="number" min={0} value={cfg.rps ?? 0} onChange={(e) => set("rps", Number(e.target.value))} />
            </div>
          </div>
          <div className="row3">
            <div>
              <label>Batas</label>
              <select value={mode} onChange={(e) => setMode(e.target.value as "durasi" | "jumlah")}>
                <option value="durasi">durasi</option>
                <option value="jumlah">jumlah request</option>
              </select>
            </div>
            <div>
              {mode === "durasi" ? (
                <>
                  <label>Durasi</label>
                  <input value={cfg.duration ?? ""} onChange={(e) => set("duration", e.target.value)} placeholder="30s" />
                </>
              ) : (
                <>
                  <label>Jumlah</label>
                  <input type="number" min={1} value={cfg.requests ?? 0} onChange={(e) => set("requests", Number(e.target.value))} />
                </>
              )}
            </div>
            <div>
              <label>Timeout</label>
              <input value={cfg.timeout ?? ""} onChange={(e) => set("timeout", e.target.value)} placeholder="5s" />
            </div>
          </div>

          <div className="actions">
            {!running ? (
              <button className="btn primary" onClick={start} disabled={!desktop}>
                ▶ Mulai
              </button>
            ) : (
              <button className="btn danger" onClick={() => app().Stop()}>
                ■ Stop
              </button>
            )}
            <button className="btn" onClick={() => doExport("csv")} disabled={!report}>
              Ekspor CSV
            </button>
            <button className="btn" onClick={() => doExport("json")} disabled={!report}>
              Ekspor JSON
            </button>
          </div>
          {msg && <div className={`msg ${msg.kind}`}>{msg.text}</div>}
        </section>

        <section className="right">
          <div className="tiles">
            <div className="tile">
              <div className="k">throughput {report ? "rata-rata" : "kini"}</div>
              <div className="v">
                {n(report ? report.rps : (last?.rps ?? 0))}
                <small>req/s</small>
              </div>
            </div>
            <div className="tile">
              <div className="k">total request</div>
              <div className="v">{total.toLocaleString("id-ID")}</div>
            </div>
            <div className={errs > 0 ? "tile bad" : "tile"}>
              <div className="k">error</div>
              <div className="v">
                {errs.toLocaleString("id-ID")}
                <small>{total ? n((100 * errs) / total) + "%" : ""}</small>
              </div>
            </div>
            <div className="tile">
              <div className="k">p50</div>
              <div className="v">
                {n(report ? report.latency.p50Ms : (last?.p50Ms ?? 0), 2)}
                <small>ms</small>
              </div>
            </div>
            <div className="tile">
              <div className="k">p95</div>
              <div className="v">
                {n(report ? report.latency.p95Ms : (last?.p95Ms ?? 0), 2)}
                <small>ms</small>
              </div>
            </div>
            <div className="tile">
              <div className="k">p99</div>
              <div className="v">
                {n(report ? report.latency.p99Ms : (last?.p99Ms ?? 0), 2)}
                <small>ms</small>
              </div>
            </div>
          </div>

          <div className="charts">
            <LineChart
              title="Throughput per detik"
              unit="req/s"
              xs={xs}
              series={[
                { name: "rps", color: "var(--c4)", values: points.map((p) => p.rps) },
                { name: "error/s", color: "var(--c3)", values: points.map((p) => p.errors) },
              ]}
            />
            <LineChart
              title="Latency per detik"
              unit="ms"
              xs={xs}
              series={[
                { name: "p50", color: "var(--c1)", values: points.map((p) => p.p50Ms) },
                { name: "p95", color: "var(--c2)", values: points.map((p) => p.p95Ms) },
                { name: "p99", color: "var(--c3)", values: points.map((p) => p.p99Ms) },
              ]}
            />
            <Histogram bins={bins} p50={report?.latency.p50Ms} p95={report?.latency.p95Ms} p99={report?.latency.p99Ms} />
            <div className="card">
              <div className="card-head">
                <span>Error per jenis</span>
                <span className="muted">{report ? "total run" : "kumulatif"}</span>
              </div>
              {Object.keys(errTotals).length === 0 ? (
                <p className="muted">Belum ada error.</p>
              ) : (
                <table>
                  <thead>
                    <tr>
                      <th>jenis</th>
                      <th className="num">jumlah</th>
                      <th className="num">% dari total</th>
                    </tr>
                  </thead>
                  <tbody>
                    {Object.entries(errTotals)
                      .sort((a, b) => b[1] - a[1])
                      .map(([k, v]) => (
                        <tr key={k}>
                          <td>{k}</td>
                          <td className="num">{v.toLocaleString("id-ID")}</td>
                          <td className="num">{total ? n((100 * v) / total) : "-"}</td>
                        </tr>
                      ))}
                  </tbody>
                </table>
              )}
              {report && (
                <table style={{ marginTop: 10 }}>
                  <thead>
                    <tr>
                      <th>latency (ms)</th>
                      <th className="num">min</th>
                      <th className="num">mean</th>
                      <th className="num">p90</th>
                      <th className="num">max</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>request sukses</td>
                      <td className="num">{n(report.latency.minMs, 2)}</td>
                      <td className="num">{n(report.latency.meanMs, 2)}</td>
                      <td className="num">{n(report.latency.p90Ms, 2)}</td>
                      <td className="num">{n(report.latency.maxMs, 2)}</td>
                    </tr>
                  </tbody>
                </table>
              )}
            </div>
          </div>
        </section>
      </main>
    </>
  );
}
