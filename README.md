# sister-hpc-demo

Demo per bab untuk mata kuliah **Sistem Terdistribusi dan Komputasi Berkinerja Tinggi**
(Program Studi Teknik Informatika STMIK Tazkia, pengampu Hendri Karisma).

Repo ini berisi aplikasi demo yang dijalankan di kelas, masing-masing dengan UI/visualisasi.
Potongan kode buku ajar ada di repo terpisah,
[`sample-code-buku`](https://github.com/situkangsayur/sample-code-buku).

Tahap 1 (sampai UTS): Bab 1–7, bagian Sistem Terdistribusi, plus demo CI/CD. Bab 8–14 (HPC) menyusul.

| Folder | Bab / slide | Isi demo | UI |
|---|---|---|---|
| [`bab01-sistem-terdistribusi-101`](bab01-sistem-terdistribusi-101/) | Bab 1, P01 | Cluster Visualizer: 3 node Go di balik nginx + Postgres; suntik latency, drop, hang, mati, crash, geser jam; partial failure, redundansi, "Mengukur Sendiri" | 50100 |
| [`bab02-jaringan-tcp-udp-quic`](bab02-jaringan-tcp-udp-quic/) | Bab 2, P02 | TCP vs UDP vs QUIC dengan `tc netem`, handshake 0/1/2 RTT, head-of-line blocking, timeline socket API + tcpdump, socket chat (`nc` + WebSocket) | 50200 |
| [`bab03-http-tls-https`](bab03-http-tls-https/) | Bab 3, P03 | Waterfall HTTP/1.1 vs HTTP/2 vs HTTP/3, TLS 1.3/1.2 dan rantai certificate, mTLS, HTTP polos vs HTTPS, nginx (cache/ETag/304, gzip, HSTS, least_conn) | 50300 |
| [`bab04-rpc-rest-grpc-graphql`](bab04-rpc-rest-grpc-graphql/) | Bab 4, P04 | Protocol Playground: REST JSON/XML, SOAP + WSDL, GraphQL (N+1 vs DataLoader), gRPC 4 mode dan gRPC-Web di belakang APISIX; real-time; timeout, retry, idempotency key | 50400 |
| [`bab05-microservices-messaging`](bab05-microservices-messaging/) | Bab 5, P05 | Toko daring + bank syariah: APISIX, nginx 2 replica, NATS JetStream SHOP/BANK (outbox, at-least-once, consumer idempotent), saga dengan kompensasi, circuit breaker, OpenSearch 2 node, belanja dibayar dari rekening, Postgres siap CDC (sumber data mata kuliah Data Engineering) | 50500 |
| [`bab06-concurrency-runtime`](bab06-concurrency-runtime/) | Bab 6, P06 | Concurrency Lab: race condition + race detector, deadlock dan wait-for graph, worker pool vs GOMAXPROCS, pipeline CSP, goroutine vs thread OS, actor + supervisor, event loop | 50600 |
| [`bab07-replikasi-konsistensi-kubernetes`](bab07-replikasi-konsistensi-kubernetes/) | Bab 7, P07 | Profil `clock` (Lamport/vector clock), `replication` (Postgres primary + replica), `consensus` (Raft 3 node), `search` (OpenSearch 2 node), `k8s` (k3s: self-healing, scaling, rolling update) | 50700 |
| [`cicd-jenkins-argocd`](cicd-jenkins-argocd/) | Bab 5 dan 7 | Pipeline CI/CD: Gitea, Jenkins (test, build, push registry), GitOps, Argo CD di k3s, dengan Pipeline Viewer | 51000 |
| [`shared/observability`](shared/observability/) | semua bab | Grafana + Loki + Alloy: log semua container, dashboard per bab | 50000 |
| [`tools/perftest-desktop`](tools/perftest-desktop/) | semua bab | Aplikasi desktop perftest (Wails): REST, GraphQL, gRPC, WebSocket; p50/p95/p99, histogram, ekspor CSV/JSON; juga mode CLI | – |

Port UI: `50000 + nomor bab × 100`. Semua port terikat ke `BIND_ADDR` di `.env` (bawaan `127.0.0.1`).
Uji semua demo berurutan: `bash uji-semua.sh`.

Konvensi (stack, port, logging, keamanan, uji): [`docs/KONVENSI.md`](docs/KONVENSI.md).

Semua demo berjalan dengan Docker Compose. Salin `.env.example` menjadi `.env` di tiap folder
sebelum menjalankan.
