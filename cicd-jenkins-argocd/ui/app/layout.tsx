import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "CI/CD Pipeline Viewer",
  description: "Demo CI/CD Jenkins + Argo CD (GitOps) di k3s: Sistem Terdistribusi, Bab 5 dan Bab 7",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="id">
      <body>{children}</body>
    </html>
  );
}
