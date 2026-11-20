import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Cluster Visualizer · Bab 1",
  description: "Demo Bab 1 Sistem Terdistribusi 101: partial failure, fallacies, dan pengukuran",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="id">
      <body>{children}</body>
    </html>
  );
}
