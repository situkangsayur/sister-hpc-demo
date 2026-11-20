import type { Metadata } from "next";
import Nav from "@/components/Nav";
import "./globals.css";

export const metadata: Metadata = {
  title: "Protocol Playground · Bab 4",
  description: "Demo Bab 4 Sistem Terdistribusi: RPC, REST, XML/SOAP, gRPC, GraphQL, real-time, API gateway",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="id">
      <body>
        <header className="top">
          <div className="top-inner">
            <div className="brand">
              Bab 4 · Komunikasi<small>slide P04</small>
            </div>
            <Nav />
          </div>
        </header>
        <main>{children}</main>
      </body>
    </html>
  );
}
