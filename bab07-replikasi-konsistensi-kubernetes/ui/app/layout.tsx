import type { Metadata } from "next";
import Link from "next/link";
import "./globals.css";

export const metadata: Metadata = {
  title: "Bab 7 Replikasi",
  description: "Demo Bab 7: waktu logis, replication, consistency, Raft, shard OpenSearch, Kubernetes",
};

const nav = [
  ["/", "Ringkasan"],
  ["/clock", "Lamport & vector clock"],
  ["/replication", "Replication & consistency"],
  ["/consensus", "Raft consensus"],
  ["/search", "Shard & replica OpenSearch"],
  ["/k8s", "Kubernetes (k3s)"],
];

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="id">
      <body>
        <header className="top">
          <div className="brand">
            <b>Sister Bab 7</b>
            <span className="muted"> Replikasi, Konsistensi, Kubernetes (slide P07)</span>
          </div>
          <nav>
            {nav.map(([href, label]) => (
              <Link key={href} href={href}>
                {label}
              </Link>
            ))}
          </nav>
        </header>
        <main>{children}</main>
      </body>
    </html>
  );
}
