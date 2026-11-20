"use client";
import Link from "next/link";
import { usePoll } from "@/lib/poll";

const items = [
  { href: "/clock", profile: "clock", probe: "/api/clock-p1/state", title: "Lamport & vector clock",
    teori: "Bab 7 subbab Waktu dan Urutan; slide P07 bagian Waktu dan urutan",
    isi: "Tiga proses Go saling mengirim pesan. Diagram ruang-waktu menampilkan nilai Lamport dan vector clock, relasi happens-before, kejadian concurrent, dan jam dinding yang digeser." },
  { href: "/replication", profile: "replication", probe: "/api/repl/status", title: "Replication & consistency",
    teori: "Bab 7 subbab Replication dan Consistency Model; slide P07 bagian Replication dan Model consistency",
    isi: "PostgreSQL primary + replica (streaming replication). Replication lag, anomali read-your-writes, skenario Ani dan Budi, async vs sync." },
  { href: "/consensus", profile: "consensus", probe: "/api/raft-n1/status", title: "Raft consensus",
    teori: "Bab 7 subbab Consensus dengan Raft; slide P07 bagian Consensus dengan Raft",
    isi: "Kluster Raft 3 node (hashicorp/raft). Leader, term, replicated log, commit index; matikan leader dan amati election." },
  { href: "/search", profile: "search", probe: "/api/search/cluster", title: "Shard & replica OpenSearch",
    teori: "Bab 7 subbab Replication dan Data Partitioning; slide P07 bagian Partition data",
    isi: "OpenSearch 2 node, index 3 shard x 1 replica. Alokasi shard per node, status yellow saat node mati, replica dipromosikan menjadi primary." },
  { href: "/k8s", profile: "k8s", probe: "/api/k8s/status", title: "Kubernetes (k3s)",
    teori: "Bab 7 subbab Container dan Kubernetes; slide P07 bagian Container dan Kubernetes dan Praktikum",
    isi: "Deployment Go dengan beberapa replica dan Service. Self-healing saat pod dihapus, scaling, rolling update, readinessProbe, beban 5 request/detik." },
];

function Status({ probe }: { probe: string }) {
  const p = usePoll<unknown>(probe, 3000);
  if (p.down) return <span className="badge muted">mati</span>;
  if (!p.data) return <span className="badge muted">memeriksa...</span>;
  return <span className="badge ok">jalan</span>;
}

export default function Home() {
  return (
    <>
      <h1>Replikasi, konsistensi, consensus, dan Kubernetes</h1>
      <p className="lead">
        Lima demo untuk Bab 7 buku Sistem Terdistribusi dan slide P07. Setiap demo adalah satu compose profile
        sehingga bagian berat (OpenSearch, k3s) dapat dijalankan terpisah. Halaman yang profilnya belum jalan menampilkan
        cara menyalakannya.
      </p>
      <div className="grid g2">
        {items.map((it) => (
          <div className="card" key={it.href}>
            <div className="row" style={{ justifyContent: "space-between" }}>
              <h2>
                <Link href={it.href}>{it.title}</Link>
              </h2>
              <Status probe={it.probe} />
            </div>
            <p>{it.isi}</p>
            <p className="muted">{it.teori}</p>
            <pre>docker compose --profile {it.profile} up -d --build</pre>
          </div>
        ))}
      </div>
    </>
  );
}
