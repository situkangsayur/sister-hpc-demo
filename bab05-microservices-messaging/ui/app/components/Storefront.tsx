"use client";

import { useEffect, useState } from "react";
import { gw, Item, Product, Rekening, rupiah, SearchResult, Stock, usePoll } from "../lib";

type CheckoutResult = {
  status: number;
  requestId: string;
  servedBy: string;
  upstream: string;
  ms: number;
  body: Record<string, unknown>;
};

function newKey() {
  return "idem-" + Math.random().toString(36).slice(2, 10);
}

export default function Storefront({ onOrder }: { onOrder: (id: string) => void }) {
  const [q, setQ] = useState("kopi");
  const [res, setRes] = useState<SearchResult | null>(null);
  const [searchMeta, setSearchMeta] = useState("");
  const [cart, setCart] = useState<Record<string, number>>({});
  const [card, setCard] = useState("ok");
  const [results, setResults] = useState<CheckoutResult[]>([]);
  const [lastKey, setLastKey] = useState("");
  const [busy, setBusy] = useState(false);
  const stock = usePoll<Stock[]>("monitor/stock", 2000);
  const rekening = usePoll<Rekening[]>("bank/rekening?limit=60", 3000);

  const search = async (query: string) => {
    const r = await fetch(gw(`catalog/search?q=${encodeURIComponent(query)}`), { cache: "no-store" });
    const remaining = r.headers.get("x-ratelimit-remaining");
    setSearchMeta(`HTTP ${r.status} · request_id ${r.headers.get("x-request-id") ?? "-"}${remaining ? ` · sisa kuota rate limit ${remaining}` : ""}`);
    if (r.ok) setRes((await r.json()) as SearchResult);
    else setRes({ source: "error", took_ms: 0, hits: [], error: await r.text() });
  };

  useEffect(() => {
    search("kopi");
  }, []);

  const stockOf = (id: string) => stock.data?.find((s) => s.product_id === id);
  const items: Item[] = Object.entries(cart)
    .filter(([, n]) => n > 0)
    .map(([product_id, qty]) => ({ product_id, qty }));

  const checkout = async (key: string, noauth = false) => {
    const started = performance.now();
    const r = await fetch(gw(`orders${noauth ? "?noauth=1" : ""}`), {
      method: "POST",
      headers: { "content-type": "application/json", "idempotency-key": key },
      // card "rek:<no>" = bayar dari rekening bank (payment memanggil service bank).
      body: JSON.stringify(
        card.startsWith("rek:") ? { customer: "mahasiswa", items, no_rekening: card.slice(4) } : { customer: "mahasiswa", items, card },
      ),
    });
    let body: Record<string, unknown> = {};
    try {
      body = await r.json();
    } catch {
      body = { error: "respons bukan JSON" };
    }
    const out: CheckoutResult = {
      status: r.status,
      requestId: r.headers.get("x-request-id") ?? "-",
      servedBy: r.headers.get("x-served-by") ?? String(body.served_by ?? "-"),
      upstream: r.headers.get("x-upstream") ?? "-",
      ms: Math.round(performance.now() - started),
      body,
    };
    setResults((p) => [out, ...p].slice(0, 6));
    const o = body.order as { id?: string } | undefined;
    if (o?.id) onOrder(o.id);
    return out;
  };

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await fn();
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="panel">
      <h2>Storefront</h2>
      <p className="hint">Pencarian: UI → APISIX (rate limit) → catalog → OpenSearch 2 node. Checkout: APISIX (key-auth) → nginx → order-1/order-2.</p>
      <form
        className="row"
        onSubmit={(e) => {
          e.preventDefault();
          search(q);
        }}
      >
        <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="cari: kopi, batik, raspbery (salah ketik pun boleh)" />
        <button type="submit">Cari</button>
      </form>
      {res && (
        <div className="meta">
          sumber <b className={res.source === "opensearch" ? "ok" : "warn"}>{res.source}</b>
          {res.node && <> · node {res.node.replace("http://", "")}</>}
          {res.shards && <> · shard {res.shards.successful}/{res.shards.total}</>} · {res.took_ms} ms · {searchMeta}
        </div>
      )}
      <div className="products">
        {res?.hits?.map((p: Product) => {
          const s = stockOf(p.id);
          return (
            <div key={p.id} className="product">
              <div className="pname">{p.name}</div>
              <div className="pdesc">{p.description}</div>
              <div className="prow">
                <span>{rupiah(p.price)}</span>
                <span className="badge">{p.category}</span>
              </div>
              <div className="prow">
                <span className="small">
                  stok {s ? s.available : "?"} {s && s.reserved > 0 ? `(dipesan ${s.reserved})` : ""}
                  {p.score ? ` · skor ${p.score.toFixed(2)}` : ""}
                </span>
                <span>
                  <button className="mini" onClick={() => setCart((c) => ({ ...c, [p.id]: Math.max(0, (c[p.id] ?? 0) - 1) }))}>−</button>
                  <b className="qty">{cart[p.id] ?? 0}</b>
                  <button className="mini" onClick={() => setCart((c) => ({ ...c, [p.id]: Math.min(5, (c[p.id] ?? 0) + 1) }))}>+</button>
                </span>
              </div>
            </div>
          );
        })}
        {res?.error && <div className="err">{res.error}</div>}
      </div>

      <h3>Keranjang & checkout</h3>
      <div className="meta">
        {items.length === 0 ? "keranjang kosong (tekan + pada produk)" : items.map((i) => `${i.product_id}×${i.qty}`).join(", ")}
      </div>
      <div className="row wrap">
        <label>
          bayar&nbsp;
          <select value={card} onChange={(e) => setCard(e.target.value)}>
            <option value="ok">kartu valid</option>
            <option value="declined">kartu ditolak bank (saga gagal di T3)</option>
            {(rekening.data ?? [])
              .filter((k) => k.jenis !== "deposito_mudharabah")
              .map((k) => (
                <option key={k.id} value={`rek:${k.no_rekening}`}>
                  rekening {k.no_rekening} · {k.nama} · saldo {rupiah(k.saldo)}
                </option>
              ))}
          </select>
        </label>
        <button
          disabled={busy || items.length === 0}
          onClick={() =>
            run(async () => {
              const k = newKey();
              setLastKey(k);
              await checkout(k);
            })
          }
        >
          Checkout
        </button>
        <button disabled={busy || !lastKey} title="Kirim ulang request yang sama (mis. setelah timeout)" onClick={() => run(() => checkout(lastKey))}>
          Kirim ulang (Idempotency-Key sama)
        </button>
        <button
          disabled={busy || items.length === 0}
          title="5 checkout berurutan untuk melihat circuit breaker bergerak"
          onClick={() =>
            run(async () => {
              for (let i = 0; i < 5; i++) await checkout(newKey());
            })
          }
        >
          Checkout 5×
        </button>
        <button className="ghost" disabled={busy || items.length === 0} onClick={() => run(() => checkout(newKey(), true))}>
          Tanpa API key
        </button>
      </div>
      <div className="results">
        {results.map((r, i) => (
          <div key={i} className={`result ${r.status < 300 ? "okb" : "errb"}`}>
            <div>
              <b>HTTP {r.status}</b> · {r.ms} ms · dilayani <b>{r.servedBy}</b> · request_id <code>{r.requestId}</code>
            </div>
            {"order" in r.body && r.body.order ? (
              <div className="small">
                order <code>{(r.body.order as { id: string }).id}</code> · total {rupiah((r.body.order as { total: number }).total)}
                {r.body.replayed ? " · REPLAYED (tidak dibuat ganda)" : ""}
                {r.body.precheck ? ` · precheck stok: ${String(r.body.precheck)}` : ""}
              </div>
            ) : (
              <div className="small">{String(r.body.error ?? r.body.message ?? JSON.stringify(r.body)).slice(0, 220)}</div>
            )}
          </div>
        ))}
      </div>
    </section>
  );
}
