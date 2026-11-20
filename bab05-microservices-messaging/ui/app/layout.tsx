import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Bab 5 · Toko Daring Microservices",
  description: "Demo microservices dan messaging: APISIX, nginx, NATS JetStream, saga, circuit breaker, OpenSearch",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="id">
      <body>{children}</body>
    </html>
  );
}
