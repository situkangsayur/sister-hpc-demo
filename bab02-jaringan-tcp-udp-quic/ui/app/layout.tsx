import type { Metadata } from "next";
import Link from "next/link";
import "./globals.css";

export const metadata: Metadata = {
  title: "Bab 2: TCP, UDP, QUIC",
  description: "Demo Sistem Terdistribusi Bab 2 (slide P02): transport, socket API, netem, tcpdump, socket chat",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="id">
      <body>
        <header className="top">
          <div className="brand">
            <b>Bab 2</b> Jaringan: TCP, UDP, QUIC <span className="muted">· slide P02</span>
          </div>
          <nav>
            <Link href="/">Perbandingan transport</Link>
            <Link href="/hol">Head-of-line blocking</Link>
            <Link href="/socket">Socket API &amp; packet</Link>
            <Link href="/chat">Socket chat</Link>
          </nav>
        </header>
        <main>{children}</main>
        <footer className="muted">
          Sistem Terdistribusi dan Komputasi Berkinerja Tinggi · STMIK Tazkia · demo sister-bab02
        </footer>
      </body>
    </html>
  );
}
