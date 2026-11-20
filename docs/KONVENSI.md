# Konvensi repo `sister-hpc-demo`

Demo per bab untuk mata kuliah **Sistem Terdistribusi dan Komputasi Berkinerja Tinggi** (STMIK Tazkia,
Hendri Karisma). Repo ini **berbeda** dari `sample-code-buku`: isinya aplikasi demo yang dijalankan
di kelas dan punya UI/visualisasi, bukan potongan kode buku. Tahap 1: Bab 1–7 (sampai UTS).
Bab 8–14 (HPC) menyusul.

## Stack wajib

| Kebutuhan | Pilihan |
|---|---|
| Backend / service | Go 1.24 (`golang:1.24-alpine` untuk build, runtime `alpine:3.20` atau `gcr.io/distroless/static`) |
| UI / visualisasi | Next.js (App Router, TypeScript), `output: "standalone"`, runtime `node:20-alpine` |
| API gateway | Apache APISIX (mode standalone/YAML, tanpa etcd bila memungkinkan) |
| Load balancer | nginx |
| Logging | Grafana + Loki (+ Grafana Alloy membaca log container dari Docker) di `shared/observability` |
| Database | PostgreSQL 16 (`postgres:16-alpine`) |
| Search / butuh Elastic | OpenSearch 2.x, **2 node** (+ OpenSearch Dashboards bila perlu) |
| Message queue | NATS (`nats:2.10-alpine`, JetStream bila perlu persistensi) |
| Kubernetes | k3s (image `rancher/k3s`, dijalankan lewat Docker Compose) |
| Perftest | aplikasi desktop di `tools/perftest-desktop` |
| Orkestrasi | **Docker Compose** untuk semua demo |

Setiap demo **wajib punya UI atau visualisasi** (Next.js) yang memperlihatkan konsep bab: node yang
hidup/mati, alur pesan, latency, throughput, state replika, dan sebagainya. Endpoint mentah saja tidak
cukup.

## Struktur folder

```
babNN-slug/
  README.md          # bahasa Indonesia: tujuan, kaitan ke bab buku dan slide PNN, arsitektur
                     # (diagram mermaid), cara menjalankan, skenario demo di kelas langkah demi langkah,
                     # port, cara membersihkan
  compose.yaml       # name: sister-babNN ; semua port lewat ${BIND_ADDR}
  .env.example       # BIND_ADDR=127.0.0.1 dan nilai contoh; .env asli TIDAK di-commit
  services/<nama>/   # kode Go (go.mod sendiri per service atau satu go.work)
  ui/                # Next.js
  uji.sh             # uji otomatis: up --build, tunggu sehat, panggil endpoint, cek hasil, down -v
```

Slug mengikuti bab buku: `bab01-sistem-terdistribusi-101`, `bab02-jaringan-tcp-udp-quic`,
`bab03-http-tls-https`, `bab04-rpc-rest-grpc-graphql`, `bab05-microservices-messaging`,
`bab06-concurrency-runtime`, `bab07-replikasi-konsistensi-kubernetes`.

## Port

- Semua port host diikat ke `${BIND_ADDR}` (default `127.0.0.1`; di server lab `10.100.21.22`
  agar dapat diakses lewat wg0).
- Rentang per bab: **`50000 + NN*100 + k`**, `k` = 0–99. Bab 1 → 50100–50199, Bab 7 → 50700–50799.
  UI utama bab selalu di `50000 + NN*100` (Bab 1 → 50100, Bab 4 → 50400).
- `shared/observability`: 50000–50099 (Grafana 50000, Loki 50001, Alloy UI 50002).
- `tools/perftest-desktop` tidak memakai port tetap.

## Logging

- Jalankan sekali: `docker compose -f shared/observability/compose.yaml up -d`.
- Alloy membaca log semua container lewat Docker socket dan memberi label
  `compose_project`, `compose_service`, `container`. Demo tidak perlu konfigurasi khusus.
- Service Go menulis log **JSON satu baris** ke stdout (`log/slog` dengan `slog.NewJSONHandler`),
  minimal field `time`, `level`, `msg`, `service`, dan `request_id` bila ada.
- Grafana punya *dashboard* per bab (folder `shared/observability/grafana/dashboards/`), dipasang
  lewat provisioning. Bab boleh menambah file dashboard JSON-nya sendiri di folder itu.

## Disk dan sumber daya (server lab tinggal ~30 GB disk)

- Pakai image ramping dan **pin versi** (tanpa `latest`). Pakai image dasar yang sama antarbab
  (lihat tabel stack) supaya layer dipakai bersama.
- Next.js: build multi-stage, `output: "standalone"`, `npm ci`, image akhir tanpa `node_modules` dev.
- Go: build multi-stage, binary statik (`CGO_ENABLED=0`).
- Setelah uji: `docker compose down -v` dan `docker builder prune -f`. Jangan menghapus image,
  volume, atau container milik proyek lain (`docker system prune` **dilarang**).
- OpenSearch: `OPENSEARCH_JAVA_OPTS=-Xms512m -Xmx512m`, matikan security plugin hanya untuk demo
  lokal dan tulis catatannya di README, matikan disk watermark (`cluster.routing.allocation.disk.threshold_enabled=false`).

## Keamanan

- Tidak ada password, token, atau kunci asli di repo. Nilai contoh di `.env.example`
  (`changeme`, dsb.). Sertifikat TLS demo dibuat saat runtime atau oleh skrip, bukan di-commit.
- Port hanya terbuka ke `BIND_ADDR`, bukan `0.0.0.0`, kecuali disebut jelas di README.

## Gaya

- README dan komentar bahasa Indonesia; istilah teknis dalam bentuk aslinya (container, service,
  gateway, load balancer, replica, latency, throughput).
- Kode Go mengikuti `gofmt` dan `go vet`; Next.js lolos `npm run build` dan `npm run lint`.
- Setiap README menulis kaitan ke bab buku dan slide (mis. "Bab 4, slide P04").

## Uji

- `bash babNN-*/uji.sh` harus lolos dari keadaan bersih dan selalu `down -v` di akhir (trap).
- Root `uji-semua.sh` menjalankan semua `uji.sh` berurutan.
