import type { Metadata } from "next";
import Link from "next/link";
import "./globals.css";

export const metadata: Metadata = {
  title: "Bab 3: HTTP, TLS, HTTPS",
  description: "Demo Sistem Terdistribusi Bab 3 (slide P03): waterfall h1/h2/h3, TLS, dan lapisan di depan server",
};

const nav = [
  { href: "/", label: "1. Waterfall h1 / h2 / h3" },
  { href: "/tls", label: "2. TLS dan certificate" },
  { href: "/sniff", label: "3. HTTP polos vs HTTPS" },
  { href: "/depan", label: "4. Di depan server (nginx)" },
];

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="id">
      <body>
        <header className="top">
          <div className="brand">
            <strong>Bab 3 · HTTP, TLS, HTTPS</strong>
            <span className="muted">Sistem Terdistribusi · slide P03</span>
          </div>
          <nav>
            {nav.map((n) => (
              <Link key={n.href} href={n.href}>
                {n.label}
              </Link>
            ))}
          </nav>
        </header>
        <main>{children}</main>
        <footer className="muted">Semua pengukuran dijalankan oleh service probe di sisi server; halaman ini hanya menampilkan hasilnya.</footer>
      </body>
    </html>
  );
}
