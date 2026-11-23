"use client";

import { useState } from "react";
import { Akad, Cabang, Nasabah, Rekening, Ringkasan, rupiah, sendJSON, shortTime, Transaksi, usePoll } from "../lib";

const KANAL = ["mobile", "atm", "teller", "web", "ecommerce"];
const JENIS_REK = ["tabungan_wadiah", "giro", "deposito_mudharabah"];
const SEGMEN = ["ritel", "prioritas", "umkm"];
const KODE_AKAD = [
  ["MRB", "murabahah (jual beli)"],
  ["MSY", "musyarakah (kemitraan)"],
  ["MDH", "mudharabah (bagi hasil)"],
  ["IJR", "ijarah (sewa)"],
];
const DEBIT = ["tarik", "transfer_keluar", "bayar_belanja", "angsuran"];

type Result = { ok: boolean; text: string };

function msg(status: number, body: unknown, okText: string): Result {
  const b = body as { error?: string; replayed?: boolean };
  if (status < 300) return { ok: true, text: `HTTP ${status} · ${okText}${b.replayed ? " · REPLAYED (Idempotency-Key sama)" : ""}` };
  return { ok: false, text: `HTTP ${status} · ${b.error ?? JSON.stringify(body)}` };
}

// toISO mengubah nilai <input type="datetime-local"> (waktu lokal browser) ke RFC3339.
const toISO = (v: string) => (v ? new Date(v).toISOString() : "");

function ResultLine({ r }: { r: Result | null }) {
  if (!r) return null;
  return <div className={`result ${r.ok ? "okb" : "errb"}`}>{r.text}</div>;
}

function BankSummary() {
  const { data } = usePoll<Ringkasan>("bank/ringkasan", 2000);
  const cards: [string, string][] = data
    ? [
        ["nasabah", String(data.nasabah)],
        ["rekening", String(data.rekening)],
        ["total saldo", rupiah(data.total_saldo)],
        ["transaksi", String(data.transaksi)],
        ["akad", Object.entries(data.akad).map(([k, v]) => `${k} ${v}`).join(" · ") || "-"],
        ["stream BANK", data.stream ? `${data.stream.messages} event` : "-"],
        ["outbox belum terkirim", String(data.outbox_pending)],
      ]
    : [];
  return (
    <section className="panel wide">
      <h2>Bank syariah · ringkasan</h2>
      <p className="hint">
        Service <b>bank</b> (Go) di belakang APISIX <code>/api/bank/*</code> (key-auth), database <code>bank</code>. Setiap perubahan ditulis bersama event ke tabel
        outbox lalu diterbitkan ke JetStream stream <code>BANK</code> (<code>bank.&lt;entitas&gt;.&lt;aksi&gt;</code>). Data ini juga sumber pipeline mata kuliah Data
        Engineering (event BANK + CDC Debezium).
      </p>
      <div className="kpis">
        {cards.map(([k, v]) => (
          <div key={k} className="kpi">
            <div className="hgroup">{k}</div>
            <div className="kval">{v}</div>
          </div>
        ))}
      </div>
    </section>
  );
}

function NasabahPanel({ cabang, selected, onSelect }: { cabang: Cabang[]; selected: string; onSelect: (id: string) => void }) {
  const list = usePoll<Nasabah[]>("bank/nasabah?limit=30", 3000);
  const [f, setF] = useState({ nama: "", nik: "", no_hp: "", email: "", kota: "Bandung", segmen: "ritel", cabang_id: 4 });
  const [edit, setEdit] = useState<{ kota: string; segmen: string }>({ kota: "", segmen: "ritel" });
  const [res, setRes] = useState<Result | null>(null);
  const sel = list.data?.find((n) => n.id === selected);

  const daftar = async () => {
    const r = await sendJSON<Nasabah>("POST", "bank/nasabah", f);
    setRes(msg(r.status, r.body, `nasabah ${r.body.cif ?? ""} dibuat → event bank.nasabah.dibuat`));
    if (r.status < 300) {
      onSelect(r.body.id);
      list.refresh();
    }
  };
  const ubah = async () => {
    if (!sel) return;
    const r = await sendJSON<Nasabah>("PATCH", `bank/nasabah/${sel.id}`, edit);
    setRes(msg(r.status, r.body, `profil ${sel.cif} diperbarui → event bank.nasabah.diperbarui (bahan SCD-2)`));
    list.refresh();
  };

  return (
    <section className="panel">
      <h2>Nasabah</h2>
      <div className="formgrid">
        <input placeholder="nama" value={f.nama} onChange={(e) => setF({ ...f, nama: e.target.value })} />
        <input placeholder="NIK (PII)" value={f.nik} onChange={(e) => setF({ ...f, nik: e.target.value })} />
        <input placeholder="no HP (PII)" value={f.no_hp} onChange={(e) => setF({ ...f, no_hp: e.target.value })} />
        <input placeholder="email (PII)" value={f.email} onChange={(e) => setF({ ...f, email: e.target.value })} />
        <input placeholder="kota" value={f.kota} onChange={(e) => setF({ ...f, kota: e.target.value })} />
        <select value={f.segmen} onChange={(e) => setF({ ...f, segmen: e.target.value })}>
          {SEGMEN.map((s) => (
            <option key={s}>{s}</option>
          ))}
        </select>
        <select value={f.cabang_id} onChange={(e) => setF({ ...f, cabang_id: Number(e.target.value) })}>
          {cabang.map((c) => (
            <option key={c.id} value={c.id}>
              {c.kode} · {c.kota}
            </option>
          ))}
        </select>
        <button disabled={!f.nama} onClick={daftar}>
          Daftar nasabah
        </button>
      </div>
      <ResultLine r={res} />
      <div className="scroll">
        <table className="mini-t">
          <thead>
            <tr>
              <th>CIF</th>
              <th>nama</th>
              <th>kota</th>
              <th>segmen</th>
              <th>cabang</th>
            </tr>
          </thead>
          <tbody>
            {(list.data ?? []).map((n) => (
              <tr
                key={n.id}
                className={`clickable ${n.id === selected ? "sel" : ""}`}
                onClick={() => {
                  onSelect(n.id);
                  setEdit({ kota: n.kota ?? "", segmen: n.segmen });
                }}
              >
                <td className="mono">{n.cif}</td>
                <td>{n.nama}</td>
                <td>{n.kota ?? <span className="faint">(kosong)</span>}</td>
                <td>{n.segmen}</td>
                <td>{cabang.find((c) => c.id === n.cabang_id)?.kode ?? "-"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {sel && (
        <div className="row wrap">
          <span className="small">
            ubah profil <b>{sel.cif}</b>:
          </span>
          <input value={edit.kota} onChange={(e) => setEdit({ ...edit, kota: e.target.value })} placeholder="kota baru" />
          <select value={edit.segmen} onChange={(e) => setEdit({ ...edit, segmen: e.target.value })}>
            {SEGMEN.map((s) => (
              <option key={s}>{s}</option>
            ))}
          </select>
          <button className="ghost" onClick={ubah}>
            Simpan
          </button>
        </div>
      )}
    </section>
  );
}

function RekeningPanel({ rekening, nasabah, refresh }: { rekening: Rekening[]; nasabah: string; refresh: () => void }) {
  const [jenis, setJenis] = useState("tabungan_wadiah");
  const [awal, setAwal] = useState(1000000);
  const [res, setRes] = useState<Result | null>(null);
  const buka = async () => {
    const r = await sendJSON<Rekening>("POST", "bank/rekening", { nasabah_id: nasabah, jenis, setoran_awal: awal });
    setRes(msg(r.status, r.body, `rekening ${r.body.no_rekening ?? ""} dibuka, saldo ${rupiah(r.body.saldo ?? 0)}`));
    refresh();
  };
  return (
    <section className="panel">
      <h2>Rekening</h2>
      <div className="row wrap">
        <select value={jenis} onChange={(e) => setJenis(e.target.value)}>
          {JENIS_REK.map((j) => (
            <option key={j}>{j}</option>
          ))}
        </select>
        <input type="number" value={awal} onChange={(e) => setAwal(Number(e.target.value))} title="setoran awal" />
        <button disabled={!nasabah} onClick={buka} title={nasabah ? "" : "pilih nasabah dulu"}>
          Buka rekening
        </button>
      </div>
      <ResultLine r={res} />
      <div className="scroll">
        <table className="mini-t">
          <thead>
            <tr>
              <th>no rekening</th>
              <th>pemilik</th>
              <th>jenis</th>
              <th>saldo</th>
            </tr>
          </thead>
          <tbody>
            {rekening.map((k) => (
              <tr key={k.id} className={k.nasabah_id === nasabah ? "sel" : ""}>
                <td className="mono">{k.no_rekening}</td>
                <td>{k.nama}</td>
                <td className="small">{k.jenis}</td>
                <td>
                  <b>{rupiah(k.saldo)}</b>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function TransaksiPanel({ rekening, cabang, refresh }: { rekening: Rekening[]; cabang: Cabang[]; refresh: () => void }) {
  const [mode, setMode] = useState("transfer");
  const [dari, setDari] = useState("");
  const [ke, setKe] = useState("");
  const [nominal, setNominal] = useState(50000);
  const [kanal, setKanal] = useState("mobile");
  const [cab, setCab] = useState(0);
  const [offline, setOffline] = useState("");
  const [lastKey, setLastKey] = useState("");
  const [res, setRes] = useState<Result | null>(null);
  const src = dari || rekening[0]?.no_rekening || "";
  const dst = ke || rekening.find((k) => k.no_rekening !== src)?.no_rekening || "";

  const kirim = async (key: string) => {
    setLastKey(key);
    const common = { nominal, kanal, cabang_id: cab, occurred_at: toISO(offline) };
    const r =
      mode === "transfer"
        ? await sendJSON("POST", "bank/transfer", { ...common, dari: src, ke: dst }, { "idempotency-key": key })
        : await sendJSON("POST", "bank/transaksi", { ...common, jenis: mode, no_rekening: src }, { "idempotency-key": key });
    setRes(msg(r.status, r.body, `${mode} ${rupiah(nominal)} via ${kanal}${offline ? " (occurred_at mundur: data terlambat)" : ""}`));
    refresh();
  };
  const opts = rekening.map((k) => (
    <option key={k.id} value={k.no_rekening}>
      {k.no_rekening} · {k.nama} · {rupiah(k.saldo)}
    </option>
  ));
  return (
    <section className="panel">
      <h2>Transaksi</h2>
      <p className="hint">Saldo dikunci dengan <code>SELECT … FOR UPDATE</code>; transfer = dua baris (keluar + masuk) dalam satu transaksi database plus event di outbox.</p>
      <div className="row wrap">
        {["setor", "tarik", "transfer"].map((m) => (
          <button key={m} className={mode === m ? "" : "ghost"} onClick={() => setMode(m)}>
            {m}
          </button>
        ))}
      </div>
      <div className="formgrid">
        <select value={src} onChange={(e) => setDari(e.target.value)}>
          {opts}
        </select>
        {mode === "transfer" && (
          <select value={dst} onChange={(e) => setKe(e.target.value)}>
            {opts}
          </select>
        )}
        <input type="number" value={nominal} onChange={(e) => setNominal(Number(e.target.value))} />
        <select value={kanal} onChange={(e) => setKanal(e.target.value)}>
          {KANAL.map((k) => (
            <option key={k}>{k}</option>
          ))}
        </select>
        <select value={cab} onChange={(e) => setCab(Number(e.target.value))}>
          <option value={0}>cabang rekening</option>
          {cabang.map((c) => (
            <option key={c.id} value={c.id}>
              {c.kode} · {c.kota}
            </option>
          ))}
        </select>
        <label className="small" title="transaksi offline perangkat: waktu kejadian lebih awal dari waktu sampai di server">
          occurred_at (offline)
          <input type="datetime-local" value={offline} onChange={(e) => setOffline(e.target.value)} />
        </label>
      </div>
      <div className="row wrap">
        <button disabled={!src} onClick={() => kirim("trx-" + Math.random().toString(36).slice(2, 10))}>
          Kirim
        </button>
        <button className="ghost" disabled={!lastKey} onClick={() => kirim(lastKey)}>
          Kirim ulang (Idempotency-Key sama)
        </button>
      </div>
      <ResultLine r={res} />
    </section>
  );
}

function AkadPanel({ nasabah }: { nasabah: string }) {
  const list = usePoll<Akad[]>("bank/akad?limit=20", 3000);
  const [kode, setKode] = useState("MRB");
  const [bebas, setBebas] = useState("");
  const [pokok, setPokok] = useState(1200000);
  const [margin, setMargin] = useState(120000);
  const [tenor, setTenor] = useState(6);
  const [res, setRes] = useState<Result | null>(null);
  const ajukan = async () => {
    const r = await sendJSON<{ akad: Akad; kode_valid: boolean }>("POST", "bank/akad", {
      nasabah_id: nasabah,
      kode_akad: kode === "lain" ? bebas : kode,
      pokok,
      margin,
      tenor_bulan: tenor,
    });
    setRes(msg(r.status, r.body, `akad diajukan${r.body.kode_valid === false ? " · kode tidak valid tetap disimpan apa adanya (dikarantina di silver DE)" : ""}`));
    list.refresh();
  };
  const aksi = async (a: Akad, x: string) => {
    const r = await sendJSON("POST", `bank/akad/${a.id}/${x}`, { kanal: "teller" });
    setRes(msg(r.status, r.body, `akad ${a.kode_akad}: ${x}`));
    list.refresh();
  };
  const tombol: Record<string, string[]> = { diajukan: ["setujui", "tolak"], disetujui: ["cairkan", "tolak"], cair: ["angsuran"] };
  return (
    <section className="panel">
      <h2>Pembiayaan syariah (akad)</h2>
      <p className="hint">diajukan → disetujui → cair (rekening dikredit) → angsuran … → lunas; atau ditolak.</p>
      <div className="formgrid">
        <select value={kode} onChange={(e) => setKode(e.target.value)}>
          {KODE_AKAD.map(([k, l]) => (
            <option key={k} value={k}>
              {k} · {l}
            </option>
          ))}
          <option value="lain">kode lain (bebas)</option>
        </select>
        {kode === "lain" && <input placeholder="mis. MRBX" value={bebas} onChange={(e) => setBebas(e.target.value)} />}
        <input type="number" value={pokok} onChange={(e) => setPokok(Number(e.target.value))} title="pokok" />
        <input type="number" value={margin} onChange={(e) => setMargin(Number(e.target.value))} title="margin" />
        <input type="number" value={tenor} onChange={(e) => setTenor(Number(e.target.value))} title="tenor bulan" />
        <button disabled={!nasabah} onClick={ajukan} title={nasabah ? "" : "pilih nasabah dulu"}>
          Ajukan
        </button>
      </div>
      <ResultLine r={res} />
      <div className="scroll">
        <table className="mini-t">
          <thead>
            <tr>
              <th>kode</th>
              <th>pokok + margin</th>
              <th>terbayar</th>
              <th>status</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {(list.data ?? []).map((a) => (
              <tr key={a.id} className={a.nasabah_id === nasabah ? "sel" : ""}>
                <td className="mono">{a.kode_akad}</td>
                <td className="small">
                  {rupiah(a.pokok)} + {rupiah(a.margin)} / {a.tenor_bulan} bln
                </td>
                <td className="small">{rupiah(a.terbayar)}</td>
                <td>
                  <b className={a.status === "ditolak" ? "bad" : a.status === "lunas" ? "ok" : ""}>{a.status}</b>
                </td>
                <td>
                  {(tombol[a.status] ?? []).map((x) => (
                    <button key={x} className="mini" onClick={() => aksi(a, x)}>
                      {x}
                    </button>
                  ))}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function MutasiPanel({ cabang }: { cabang: Cabang[] }) {
  const { data } = usePoll<Transaksi[]>("bank/transaksi?limit=25", 1500);
  return (
    <section className="panel wide">
      <h2>Mutasi live (semua rekening)</h2>
      <p className="hint">
        <code>occurred_at</code> = waktu kejadian di perangkat, <code>created_at</code> = waktu tercatat di server. Baris bertanda <span className="tag s-warn">terlambat</span>{" "}
        adalah transaksi offline yang baru tersinkron (bahan late data di pipeline DE).
      </p>
      <div className="scroll tall">
        <table className="mini-t">
          <thead>
            <tr>
              <th>created_at</th>
              <th>occurred_at</th>
              <th>rekening</th>
              <th>jenis</th>
              <th>kanal</th>
              <th>cabang</th>
              <th>nominal</th>
              <th>saldo</th>
              <th>ref order / keterangan</th>
            </tr>
          </thead>
          <tbody>
            {(data ?? []).map((t) => {
              const late = new Date(t.created_at).getTime() - new Date(t.occurred_at).getTime() > 60000;
              const debit = DEBIT.includes(t.jenis);
              return (
                <tr key={t.id}>
                  <td className="mono">{shortTime(t.created_at)}</td>
                  <td className="mono">
                    {late ? new Date(t.occurred_at).toLocaleString("id-ID") : shortTime(t.occurred_at)} {late && <span className="tag s-warn">terlambat</span>}
                  </td>
                  <td className="small">
                    <span className="mono">{t.no_rekening}</span> {t.nama}
                  </td>
                  <td>
                    <span className={`tag ${t.jenis === "bayar_belanja" ? "s-bank" : debit ? "s-bad" : "s-ok"}`}>{t.jenis}</span>
                  </td>
                  <td className="small">{t.kanal}</td>
                  <td className="small">{cabang.find((c) => c.id === t.cabang_id)?.kode ?? "-"}</td>
                  <td className={debit ? "bad" : "ok"}>
                    {debit ? "−" : "+"}
                    {rupiah(t.nominal)}
                  </td>
                  <td>{rupiah(t.saldo_setelah)}</td>
                  <td className="small">
                    {t.ref_order_id && <code>{t.ref_order_id}</code>} {t.keterangan}
                  </td>
                </tr>
              );
            })}
            {(data ?? []).length === 0 && (
              <tr>
                <td colSpan={9} className="faint small">
                  belum ada transaksi
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  );
}

// BankTab: semua panel bank dalam satu tab.
export default function BankTab() {
  const cabang = usePoll<Cabang[]>("bank/cabang", 30000);
  const rekening = usePoll<Rekening[]>("bank/rekening?limit=60", 2000);
  const [nasabah, setNasabah] = useState("");
  const cab = cabang.data ?? [];
  const reks = rekening.data ?? [];
  return (
    <>
      <BankSummary />
      <NasabahPanel cabang={cab} selected={nasabah} onSelect={setNasabah} />
      <RekeningPanel rekening={reks} nasabah={nasabah} refresh={rekening.refresh} />
      <TransaksiPanel rekening={reks} cabang={cab} refresh={rekening.refresh} />
      <AkadPanel nasabah={nasabah} />
      <MutasiPanel cabang={cab} />
    </>
  );
}
