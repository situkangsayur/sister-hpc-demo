"use client";

import { useCallback, useEffect, useState } from "react";
import { type Build, type Status, TAHAP, detik, jam, pendek, warnaVersi } from "@/lib/types";

const st = (s: string | null | undefined) => `st-${s || "menunggu"}`;

function Lencana({ s, teks }: { s: string | null | undefined; teks?: string }) {
  return <span className={`lencana ${st(s)}`}>{teks ?? (s || "menunggu")}</span>;
}

export default function Viewer() {
  const [s, setS] = useState<Status | null>(null);
  const [galat, setGalat] = useState("");

  const muat = useCallback(async () => {
    try {
      const r = await fetch("/api/status", { cache: "no-store" });
      if (!r.ok) throw new Error((await r.json()).error ?? r.statusText);
      setS(await r.json());
      setGalat("");
    } catch (e) {
      setGalat(String(e instanceof Error ? e.message : e));
    }
  }, []);

  useEffect(() => {
    muat();
    const t = setInterval(muat, 2000);
    return () => clearInterval(t);
  }, [muat]);

  return (
    <main>
      <header className="kepala">
        <div>
          <h1>CI/CD Pipeline Viewer</h1>
          <p className="muted">
            Jenkins (CI) + Argo CD (CD, GitOps) di k3s · Bab 7 &quot;Container dan Kubernetes&quot; (slide P07) dan Bab 5
            microservices · data diperbarui setiap 2 detik
          </p>
        </div>
        <span className={s && !galat ? "sambung ok" : "sambung"} role="status">
          {s && !galat ? `● ${jam(s.waktu)}` : galat ? `○ ${galat}` : "○ menyambung…"}
        </span>
      </header>
      {!s ? (
        <p>Menunggu data pertama dari service pantau… (init pertama kali butuh 3–6 menit)</p>
      ) : (
        <>
          {s.tautan && <Tautan s={s} />}
          <section className="kartu" style={{ marginBottom: 12 }}>
            <h2>Alur ujung ke ujung</h2>
            <Alur s={s} />
          </section>
          <div className="grid-2">
            <section className="kartu">
              <h2>Aksi demo</h2>
              <Aksi s={s} onSelesai={muat} />
            </section>
            <section className="kartu">
              <h2>Versi yang sedang melayani request</h2>
              <Berjalan s={s} />
            </section>
          </div>
          <div className="grid-2">
            <section className="kartu">
              <h2>Pipeline Jenkins (job app)</h2>
              <Jenkins s={s} />
            </section>
            <section className="kartu">
              <h2>Argo CD: Application app</h2>
              <Argo s={s} />
            </section>
          </div>
          <div className="grid-2">
            <section className="kartu">
              <h2>Riwayat deploy</h2>
              <Riwayat s={s} />
            </section>
            <section className="kartu">
              <h2>Log event</h2>
              <Events s={s} />
            </section>
          </div>
        </>
      )}
    </main>
  );
}

function Tautan({ s }: { s: Status }) {
  const t = s.tautan;
  const daftar: [string, string][] = [
    ["Gitea: repo app", t.GiteaApp],
    ["Gitea: repo gitops", t.GiteaGitops],
    ["Jenkins: job app", t.JenkinsJob],
    ["Argo CD: app", t.ArgoApp],
    ["Registry: tag", t.Registry],
    ["Aplikasi (NodePort)", t.App],
    ["Grafana (log)", t.Grafana],
  ];
  return (
    <nav className="tautan-baris" style={{ marginBottom: 12 }}>
      {daftar.map(([n, u]) => (
        <a key={n} href={u} target="_blank" rel="noreferrer">
          {n} ↗
        </a>
      ))}
    </nav>
  );
}

// Enam langkah: commit kode → Jenkins → registry → commit gitops → Argo CD → pod di k3s.
function Alur({ s }: { s: Status }) {
  const b = s.jenkins.builds[0];
  const hasilBuild = b ? (b.berjalan ? "IN_PROGRESS" : b.hasil) : null;
  const tahapJalan = b?.tahap?.find((t) => t.status === "IN_PROGRESS")?.nama;
  const c = s.app.commits?.[0];
  const g = s.gitops.commits?.[0];
  const tagAda = s.registry.includes(s.gitops.tag ?? "");
  const versi = Object.keys(s.berjalan.versi ?? {});
  return (
    <div className="alur">
      <div className="langkah st-SUCCESS">
        <div className="zona">1 · Git (repo app)</div>
        <h3>Commit kode</h3>
        <div className="nilai">{c ? pendek(c.sha) : "–"}</div>
        <div className="isi">{c?.pesan}</div>
      </div>
      <div className={`langkah ${st(hasilBuild)}`}>
        <div className="zona">2 · CI · Jenkins</div>
        <h3>Build #{b?.nomor ?? "–"} <Lencana s={hasilBuild} /></h3>
        <div className="isi">{b?.berjalan ? `tahap: ${tahapJalan ?? "menyiapkan"}` : b ? `durasi ${detik(b.durasi_ms)}` : s.jenkins.error}</div>
      </div>
      <div className={`langkah ${tagAda ? "st-SUCCESS" : "st-menunggu"}`}>
        <div className="zona">3 · registry:5000</div>
        <h3>Image</h3>
        <div className="nilai">demo/app:{s.registry[0] ?? "–"}</div>
        <div className="isi">{s.registry.length} tag tersimpan</div>
      </div>
      <div className="langkah st-SUCCESS">
        <div className="zona">4 · Git (repo gitops)</div>
        <h3>Desired state</h3>
        <div className="nilai">newTag: {s.gitops.tag ?? "–"}</div>
        <div className="isi">{g ? `${pendek(g.sha)} ${g.pesan}` : s.gitops.error}</div>
      </div>
      <div className={`langkah ${st(s.argo.ok ? (s.argo.sync === "Synced" ? s.argo.health : s.argo.sync) : null)}`}>
        <div className="zona">5 · CD · Argo CD</div>
        <h3>
          <Lencana s={s.argo.sync} /> <Lencana s={s.argo.health} />
        </h3>
        <div className="isi">{s.argo.ok ? `revisi ${pendek(s.argo.revisi)}` : s.argo.error ?? "belum terpasang"}</div>
      </div>
      <div className={`langkah ${versi.length === 1 && versi[0] === s.gitops.tag ? "st-SUCCESS" : versi.length ? "st-Progressing" : "st-menunggu"}`}>
        <div className="zona">6 · k3s · Deployment</div>
        <h3>Versi berjalan</h3>
        <div className="nilai">{versi.join(" + ") || "–"}</div>
        <div className="isi">{s.argo.pods?.length ?? 0} pod</div>
      </div>
    </div>
  );
}

function Aksi({ s, onSelesai }: { s: Status; onSelesai: () => void }) {
  const [pesan, setPesan] = useState("");
  const [tag, setTag] = useState("");
  const [sibuk, setSibuk] = useState(false);
  const [hasil, setHasil] = useState("");

  const kirim = async (jalur: string, body: object, ket: string) => {
    setSibuk(true);
    try {
      const r = await fetch(`/api/${jalur}`, { method: "POST", body: JSON.stringify(body) });
      const j = await r.json();
      setHasil(r.ok ? `${ket}: ${j.commit ? `commit ${pendek(j.commit)}` : "ok"}` : `gagal: ${j.error}`);
      onSelesai();
    } catch (e) {
      setHasil(`gagal: ${e}`);
    } finally {
      setSibuk(false);
    }
  };
  const tagLain = s.registry.filter((t) => t !== s.gitops.tag);

  return (
    <div>
      <h3>1. Ubah kode dan push (repo app)</h3>
      <p className="kecil muted">
        Pesan sekarang: <b>{s.app.pesan || "(kosong)"}</b>. Tombol ini membuat satu commit yang mengganti konstanta{" "}
        <code>Pesan</code> di <code>pesan.go</code>. Webhook Gitea memicu Jenkins.
      </p>
      <div className="tombol-baris">
        <input type="text" value={pesan} maxLength={120} placeholder="pesan baru, mis. Halo kelas Sister"
          onChange={(e) => setPesan(e.target.value)} />
        <button className="utama" disabled={sibuk || !pesan.trim()}
          onClick={() => kirim("push", { pesan }, "push kode").then(() => setPesan(""))}>
          Ubah kode dan push
        </button>
      </div>
      <div className="tombol-baris">
        <button className="bahaya" disabled={sibuk} onClick={() => kirim("push", { gagal: true }, "push kode rusak")}>
          Push kode rusak (unit test gagal)
        </button>
        <span className="kecil muted">CI menjadi gerbang: image tidak dibangun, cluster tidak berubah.</span>
      </div>

      <h3>2. Rollback (commit ke repo gitops)</h3>
      <div className="tombol-baris">
        <select value={tag} onChange={(e) => setTag(e.target.value)} aria-label="tag tujuan rollback">
          <option value="">otomatis: versi sebelumnya</option>
          {tagLain.map((t) => (
            <option key={t} value={t}>{t}</option>
          ))}
        </select>
        <button disabled={sibuk || tagLain.length === 0} onClick={() => kirim("rollback", { tag }, "rollback")}>
          Rollback
        </button>
      </div>
      <p className="kecil muted">
        GitOps: rollback adalah commit yang mengembalikan <code>newTag</code>. Argo CD menolak rollback lewat API-nya selama
        auto-sync aktif, karena Git tetap sumber kebenaran.
      </p>

      <h3>3. Drift: ubah cluster tanpa Git</h3>
      <div className="tombol-baris">
        <button disabled={sibuk || !s.argo.ok} onClick={() => kirim("drift", {}, "scale manual ke 4 replica")}>
          Scale manual ke 4 replica
        </button>
        <span className="kecil muted">selfHeal Argo CD mengembalikan ke 2 replica (isi Git).</span>
      </div>
      {hasil && <p className="kecil mono">{hasil}</p>}
    </div>
  );
}

function Berjalan({ s }: { s: Status }) {
  const b = s.berjalan;
  const entri = Object.entries(b.versi ?? {}).sort((x, y) => y[1] - x[1]);
  const pesan = [...new Set((b.respons ?? []).map((r) => `${r.versi}: ${r.pesan}`))];
  return (
    <div>
      <p className="kecil muted">
        {b.sampel} request baru setiap 2 detik ke Service NodePort (koneksi baru tiap request, jadi kube-proxy menyebarnya ke
        semua pod). Saat rolling update, dua versi tampak bersamaan.
      </p>
      {entri.length ? (
        <div className="batang" role="img" aria-label="sebaran versi">
          {entri.map(([v, n]) => (
            <div key={v} style={{ width: `${(100 * n) / b.sampel}%`, background: warnaVersi(v) }} title={`${v}: ${n}`}>
              {v} · {n}
            </div>
          ))}
        </div>
      ) : (
        <p className="galat">belum ada jawaban dari aplikasi {b.error ? `(${b.error})` : ""}</p>
      )}
      {b.gagal > 0 && <p className="galat">{b.gagal} request gagal</p>}
      <ul className="kecil" style={{ paddingLeft: 18, margin: "6px 0" }}>
        {pesan.map((p) => (
          <li key={p}>{p}</li>
        ))}
      </ul>
      <h3>Pod (dari resource tree Argo CD)</h3>
      <div className="pod-grid">
        {(s.argo.pods ?? []).map((p) => (
          <div key={p.nama} className={`pod ${st(p.health)}`}>
            <div className="mono">{p.nama}</div>
            <div style={{ color: warnaVersi(p.tag) }}><b>{p.tag || "?"}</b></div>
            <div className="muted">{p.health}{p.status ? ` · ${p.status}` : ""}</div>
          </div>
        ))}
      </div>
    </div>
  );
}

function KartuBuild({ b }: { b: Build }) {
  const hasil = b.berjalan ? "IN_PROGRESS" : b.hasil;
  const peta = new Map((b.tahap ?? []).map((t) => [t.nama, t]));
  return (
    <div className="build">
      <div className="build-kepala">
        <b>#{b.nomor}</b> <Lencana s={hasil} />
        <span className="mono">{b.tag}</span>
        <span className="muted">{jam(b.mulai)} · {detik(b.durasi_ms)}</span>
      </div>
      {b.tahap !== null && (
        <div className="tahap-baris">
          {TAHAP.map((n) => {
            const t = peta.get(n);
            return (
              <div key={n} className={`tahap ${st(t?.status ?? (b.berjalan ? null : "SKIPPED"))}`}>
                <b title={n}>{n}</b>
                <span>{t ? `${t.status === "IN_PROGRESS" ? "berjalan" : t.status} · ${detik(t.durasi_ms)}` : "–"}</span>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function Jenkins({ s }: { s: Status }) {
  const j = s.jenkins;
  if (!j.ok) return <p className="galat">Jenkins belum siap: {j.error}</p>;
  return (
    <div>
      {j.antre && <p className="kecil"><Lencana s="IN_PROGRESS" teks="build dalam antrean" /></p>}
      {j.builds.length === 0 && <p className="muted">Belum ada build.</p>}
      {j.builds.slice(0, 5).map((b) => (
        <KartuBuild key={b.nomor} b={b} />
      ))}
      <p className="kecil muted">
        Tahap Unit test menjalankan <code>go vet</code> dan <code>go test</code> di BuildKit (target <code>test</code>).
        Jenkins tidak menyentuh cluster: tahap terakhir hanya commit tag baru ke repo gitops.
      </p>
    </div>
  );
}

function Argo({ s }: { s: Status }) {
  const a = s.argo;
  if (!a.ok) return <p className="galat">Argo CD belum siap: {a.error}</p>;
  return (
    <div>
      <table className="tabel">
        <tbody>
          <tr><th>Sync status</th><td><Lencana s={a.sync} /> <span className="muted kecil">Git vs cluster</span></td></tr>
          <tr><th>Health</th><td><Lencana s={a.health} /> <span className="muted kecil">pod siap (readinessProbe)</span></td></tr>
          <tr><th>Revisi Git</th><td className="mono">{pendek(a.revisi)}</td></tr>
          <tr><th>Rekonsiliasi terakhir</th><td>{a.rekonsiliasi ? jam(a.rekonsiliasi) : "–"} <span className="muted kecil">(repo dicek tiap 15 s)</span></td></tr>
          {a.operasi && (
            <tr><th>Operasi terakhir</th><td><Lencana s={a.operasi.fase === "Succeeded" ? "SUCCESS" : a.operasi.fase === "Running" ? "IN_PROGRESS" : "FAILED"} teks={a.operasi.fase} /> <span className="kecil">{a.operasi.pesan}</span></td></tr>
          )}
          <tr><th>ReplicaSet</th><td>{a.replicaset} <span className="muted kecil">(satu per versi template pod)</span></td></tr>
        </tbody>
      </table>
      <p className="kecil muted">
        Argo CD menjalankan reconciliation loop satu tingkat di atas Deployment controller: desired state ada di Git,
        actual state di cluster, dan selisihnya (OutOfSync) disamakan otomatis.
      </p>
    </div>
  );
}

function Riwayat({ s }: { s: Status }) {
  const r = s.argo.riwayat ?? [];
  if (!r.length) return <p className="muted">Belum ada deploy.</p>;
  return (
    <div className="tabel-gulir">
      <table className="tabel">
        <thead>
          <tr><th>#</th><th>waktu</th><th>tag</th><th>commit gitops</th></tr>
        </thead>
        <tbody>
          {r.map((d, i) => (
            <tr key={`${d.id}-${i}`} className={i === 0 ? "sorot-baris" : ""}>
              <td>{d.id}</td>
              <td>{jam(d.waktu)}</td>
              <td className="mono" style={{ color: warnaVersi(d.tag) }}><b>{d.tag || "?"}</b></td>
              <td className="kecil"><span className="mono">{pendek(d.revisi)}</span> {d.pesan}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Events({ s }: { s: Status }) {
  const ev = s.events ?? [];
  if (!ev.length) return <p className="muted">Belum ada event. Tekan &quot;Ubah kode dan push&quot;.</p>;
  return (
    <ul className="event">
      {ev.map((e, i) => (
        <li key={`${e.waktu}-${i}`}>
          <time>{jam(e.waktu)}</time>
          <span className="sumber">{e.sumber}</span>
          <span>{e.pesan}</span>
        </li>
      ))}
    </ul>
  );
}
