# sister-hpc-demo

Demo per bab untuk mata kuliah **Sistem Terdistribusi dan Komputasi Berkinerja Tinggi**
(Program Studi Teknik Informatika STMIK Tazkia, pengampu Hendri Karisma).

Repo ini berisi aplikasi demo yang dijalankan di kelas, masing-masing dengan UI/visualisasi.
Potongan kode buku ajar ada di repo terpisah,
[`sample-code-buku`](https://github.com/situkangsayur/sample-code-buku).

Tahap 1 (sampai UTS): Bab 1–7, bagian Sistem Terdistribusi. Bab 8–14 (HPC) menyusul.

| Folder | Bab / slide | Isi demo |
|---|---|---|
| `shared/observability` | semua bab | Grafana + Loki + Alloy untuk log semua container |
| `tools/perftest-desktop` | semua bab | aplikasi desktop untuk perftest REST, gRPC, GraphQL, WebSocket |
| `bab01-...` sampai `bab07-...` | Bab 1–7, P01–P07 | lihat README tiap folder |

Konvensi (stack, port, logging, keamanan, uji): [`docs/KONVENSI.md`](docs/KONVENSI.md).

Semua demo berjalan dengan Docker Compose. Salin `.env.example` menjadi `.env` di tiap folder
sebelum menjalankan.
