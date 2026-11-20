"use client";
// Socket chat: browser (WebSocket lewat gateway) dan nc (socket TCP mentah) di ruang yang sama.
import { useEffect, useRef, useState } from "react";

type Cfg = { chatWsPort: string; chatTcpPort: string; echoTcpPort: string; echoUdpPort: string };
type ClientInfo = { id: number; nick: string; kind: string; remote: string; origin?: string; joined_at: string; msgs: number };
type Line = { text: string; t: number };

const RE_CHAT = /^\[(\d\d:\d\d:\d\d)\] ([^:]+): (.*)$/;

export default function ChatPage() {
  const [cfg, setCfg] = useState<Cfg | null>(null);
  const [host, setHost] = useState("localhost");
  const [nick, setNick] = useState("");
  const [connected, setConnected] = useState(false);
  const [lines, setLines] = useState<Line[]>([]);
  const [input, setInput] = useState("");
  const [clients, setClients] = useState<ClientInfo[]>([]);
  const ws = useRef<WebSocket | null>(null);
  const box = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    setHost(window.location.hostname);
    setNick(`web-${Math.floor(Math.random() * 900 + 100)}`);
    fetch("/api/config")
      .then((r) => r.json())
      .then(setCfg)
      .catch(() => undefined);
    const load = () =>
      fetch("/api/chat/clients", { cache: "no-store" })
        .then((r) => r.json())
        .then((d) => Array.isArray(d) && setClients(d))
        .catch(() => undefined);
    load();
    const t = setInterval(load, 1500);
    return () => {
      clearInterval(t);
      ws.current?.close();
    };
  }, []);

  useEffect(() => {
    box.current?.scrollTo({ top: box.current.scrollHeight });
  }, [lines]);

  function connect() {
    if (!cfg) return;
    const proto = window.location.protocol === "https:" ? "wss" : "ws";
    const url = `${proto}://${window.location.hostname}:${cfg.chatWsPort}/ws?nick=${encodeURIComponent(nick)}`;
    const sock = new WebSocket(url);
    ws.current = sock;
    sock.onopen = () => {
      setConnected(true);
      setLines((l) => [...l, { text: `*** WebSocket terbuka ke ${url}`, t: Date.now() }]);
    };
    sock.onmessage = (ev) => setLines((l) => [...l.slice(-300), { text: String(ev.data), t: Date.now() }]);
    sock.onclose = () => {
      setConnected(false);
      setLines((l) => [...l, { text: "*** WebSocket tertutup", t: Date.now() }]);
    };
    sock.onerror = () => setLines((l) => [...l, { text: "*** gagal terhubung ke gateway WebSocket", t: Date.now() }]);
  }

  function send(e: React.FormEvent) {
    e.preventDefault();
    if (!input.trim() || !ws.current || ws.current.readyState !== WebSocket.OPEN) return;
    ws.current.send(input);
    setInput("");
  }

  const tcpPort = cfg?.chatTcpPort ?? "50210";

  return (
    <>
      <section className="card">
        <h1>Socket chat</h1>
        <p className="muted">
          Server chat adalah program Go di atas socket TCP mentah: <code>net.Listen</code> (= socket + bind + listen),
          satu <code>Accept()</code> dan satu goroutine per client, framing satu baris = satu pesan. Browser tidak bisa
          membuka socket TCP, jadi gateway membuka <b>satu socket TCP per koneksi WebSocket</b>. Di mata server, browser
          hanyalah client TCP lain (Bab 2, Socket API dan Framing).
        </p>
        <pre>{`# ikut dari terminal mana pun yang bisa menjangkau host ini
nc ${host} ${tcpPort}
/nick budi
halo dari terminal
/who
/quit`}</pre>
      </section>
      <div className="grid2">
        <section className="card">
          <h2>Ruang chat</h2>
          <div className="row">
            <label>
              nick <input type="text" value={nick} maxLength={20} disabled={connected} onChange={(e) => setNick(e.target.value)} />
            </label>
            {connected ? (
              <button className="ghost" onClick={() => ws.current?.close()}>
                Putuskan
              </button>
            ) : (
              <button onClick={connect} disabled={!cfg}>
                Hubungkan
              </button>
            )}
          </div>
          <div className="chatbox" ref={box} style={{ marginTop: 10 }}>
            {lines.map((l, i) => {
              const m = l.text.match(RE_CHAT);
              if (m) {
                return (
                  <div key={i} className={m[2] === nick ? "me" : undefined}>
                    <span className="sys">{m[1]}</span> <span className="nick">{m[2]}</span>: {m[3]}
                  </div>
                );
              }
              return (
                <div key={i} className="sys">
                  {l.text}
                </div>
              );
            })}
            {lines.length === 0 && <div className="sys">Klik Hubungkan, lalu buka nc di terminal lain.</div>}
          </div>
          <form className="row" onSubmit={send}>
            <input type="text" style={{ flex: 1 }} value={input} disabled={!connected} placeholder="ketik pesan atau /who" onChange={(e) => setInput(e.target.value)} />
            <button type="submit" disabled={!connected}>
              Kirim
            </button>
          </form>
        </section>
        <section className="card">
          <h2>Siapa yang terhubung ({clients.length})</h2>
          <p className="muted">Dibaca dari server chat setiap 1,5 detik. Alamat adalah ujung socket yang dilihat server (5-tuple sisi client).</p>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>nick</th>
                  <th>jenis</th>
                  <th>alamat socket</th>
                  <th className="num">pesan</th>
                </tr>
              </thead>
              <tbody>
                {clients.map((c) => (
                  <tr key={c.id}>
                    <td>{c.nick}</td>
                    <td>
                      <span className={`badge ${c.kind}`}>{c.kind === "websocket" ? "browser via gateway" : "TCP mentah (nc)"}</span>
                    </td>
                    <td>
                      <code>{c.remote}</code>
                      {c.origin && <div className="muted">browser: {c.origin}</div>}
                    </td>
                    <td className="num">{c.msgs}</td>
                  </tr>
                ))}
                {clients.length === 0 && (
                  <tr>
                    <td colSpan={4} className="muted">
                      belum ada client
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
          <div className="theory" style={{ marginTop: 10 }}>
            Perhatikan: client “browser via gateway” punya alamat socket milik container gateway, bukan alamat browser.
            Server hanya tahu ujung socket yang terhubung langsung kepadanya.
          </div>
        </section>
      </div>
    </>
  );
}
