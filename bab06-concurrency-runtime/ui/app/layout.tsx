import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Concurrency Lab · Bab 6",
  description: "Demo Bab 6 Concurrency dan Runtime: race condition, deadlock, worker pool, CSP, goroutine, actor, event loop.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="id">
      <body>{children}</body>
    </html>
  );
}
