// Bentuk JSON dari GET /api/status (service pantau).
export type Tahap = { nama: string; status: string; durasi_ms: number };
export type Build = {
  nomor: number; hasil: string | null; berjalan: boolean; mulai: number; durasi_ms: number; tag: string;
  tahap: Tahap[] | null;
};
export type Commit = { sha: string; pesan: string; penulis: string; waktu: string };
export type Repo = { ok: boolean; error?: string; commits: Commit[] | null; tag?: string; pesan?: string };
export type Pod = { nama: string; image: string; tag: string; health: string; status?: string; dibuat: string };
export type Deploy = { id: number; revisi: string; tag: string; pesan: string; waktu: string };
export type Argo = {
  ok: boolean; error?: string; sync: string; health: string; revisi: string; rekonsiliasi: string;
  operasi?: { fase: string; pesan: string; mulai: string; selesai: string };
  pods: Pod[] | null; replicaset: number; riwayat: Deploy[] | null;
};
export type Berjalan = {
  ok: boolean; error?: string; sampel: number; gagal: number; versi: Record<string, number>;
  respons: { versi: string; pesan: string; pod: string }[] | null;
};
export type Event = { waktu: string; sumber: string; pesan: string };
export type Tautan = Record<
  "UI" | "Gitea" | "GiteaApp" | "GiteaGitops" | "Jenkins" | "JenkinsJob" | "ArgoCD" | "ArgoApp" | "Registry" | "App" | "Grafana",
  string
>;
export type Status = {
  waktu: string; tautan: Tautan; app: Repo; gitops: Repo;
  jenkins: { ok: boolean; error?: string; antre: boolean; builds: Build[] };
  registry: string[]; argo: Argo; berjalan: Berjalan; events: Event[] | null;
};

// Tahap pada Jenkinsfile repo app (urutan tetap, agar tahap yang belum mulai tetap tampil).
export const TAHAP = ["Checkout", "Unit test", "Build & push image", "Update gitops"];

export const detik = (ms: number) => (ms >= 1000 ? `${(ms / 1000).toFixed(ms >= 10000 ? 0 : 1)} s` : `${ms} ms`);
export const jam = (t: string | number) =>
  new Date(t).toLocaleTimeString("id-ID", { hour12: false, hour: "2-digit", minute: "2-digit", second: "2-digit" });
export const pendek = (sha: string) => (sha ?? "").slice(0, 7);

// Warna per versi: turunan nomor build agar konsisten di semua panel.
const PALET = ["#2a78d6", "#eb6834", "#1baf7a", "#6250d6", "#d03b3b", "#c98a00", "#0f8b8d", "#a84ca8"];
export const warnaVersi = (tag: string) => {
  const m = /^b(\d+)-/.exec(tag);
  return m ? PALET[Number(m[1]) % PALET.length] : "#8a8984";
};
