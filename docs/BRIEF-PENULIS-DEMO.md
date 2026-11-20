# Brief penulis demo (dipakai bersama semua penulis paralel)

Repo: `/home/hendri/stmik-codes/sister-hpc-demo` (GitHub public `situkangsayur/sister-hpc-demo`).
Baca dulu `docs/KONVENSI.md` (stack, struktur folder, port, logging, disk, keamanan, uji). Patuhi
seluruhnya.

Bahan materi: buku `/home/hendri/Documents/obsidian/MyNotes/STMIK-Tazkia/semester-3/Sistem-Terdistribusi/buku/bab/babNN-*.tex`
dan slide `.../Sistem-Terdistribusi/slides/PNN-*.tex` untuk bab Anda. Demo harus memperlihatkan
konsep yang benar-benar dibahas di bab dan slide itu; baca daftar section-nya dulu.

Permintaan pengampu (Hendri, 21 Nov 2026): demo untuk setiap materi; Go untuk backend, Next.js
untuk UI; APISIX bila butuh gateway; nginx untuk load balancer; Grafana + Loki untuk logging;
Postgres untuk database; OpenSearch 2 node bila butuh Elastic; NATS bila butuh message queue;
semua jalan dengan Docker Compose, kecuali demo Kubernetes memakai k3s (ringan); **setiap demo
punya UI atau visualisasi**; contoh setiap protokol (XML, REST, gRPC, GraphQL) dan contoh socket
chat; ada aplikasi desktop untuk perftest.

## Aturan kerja

1. Kerjakan hanya folder milik Anda. Jangan mengubah folder bab lain, `shared/`, atau `tools/`
   kecuali Anda penulis bagian itu. Bila butuh sesuatu dari `shared/observability`, tulis di laporan.
2. **Disk server tinggal sekitar 30 GB.** Sebelum setiap `docker compose build`, jalankan
   `df -h /`; bila sisa < 8 GB, tunggu (cek lagi beberapa menit kemudian) dan jangan membangun.
   Pakai image yang dipin di KONVENSI agar layer dipakai bersama. Sesudah uji: `docker compose down -v`
   lalu `docker builder prune -f`. **Dilarang** `docker system prune`, `docker volume prune`,
   `docker image prune -a`, atau menghapus resource yang bukan milik project `sister-babNN` Anda.
3. Ada ±68 container lain yang sedang berjalan di mesin ini. Jangan menyentuhnya. Pakai hanya
   port di rentang bab Anda (KONVENSI) dan ikat ke `${BIND_ADDR}`.
4. Jangan membuka berkas kredensial atau secret di mana pun (mis. `.env` proyek lain,
   `~/.ssh`, `*.conf` berisi password, catatan akun). `.env` demo dibuat dari `.env.example`
   dan tidak di-commit.
5. Jangan `git commit` atau `git push`; koordinator yang melakukannya. Jangan mengubah
   `README.md` root (tulis baris tabel yang diusulkan untuk bab Anda di laporan).
6. Uji sungguhan: `cp .env.example .env` (BIND_ADDR=127.0.0.1), `bash uji.sh` harus lolos dari
   keadaan bersih; uji.sh memanggil endpoint utama dan memeriksa hasil, termasuk bahwa UI melayani
   halaman (HTTP 200) dan API yang dipakai UI mengembalikan data. Render UI ke PNG bila ada alat
   (mis. `npx playwright screenshot` atau `chromium --headless --screenshot`) dan periksa sendiri;
   bila alat tidak ada, tulis di laporan.
7. README bahasa Indonesia, istilah teknis bentuk asli. Isi wajib: tujuan dan kaitan ke bab buku
   dan slide; diagram arsitektur (mermaid); cara menjalankan; **skenario demo di kelas** langkah
   demi langkah (apa yang diklik, apa yang diamati, apa hubungannya dengan teori); tabel port;
   cara melihat log di Grafana; cara membersihkan.
8. Kode Go: `go vet` dan `gofmt` bersih, ada unit test untuk logika inti bila masuk akal. Next.js:
   `npm run build` lolos. `package-lock.json` di-commit; `node_modules` tidak.

## Laporan akhir

Struktur folder, layanan dan image (dengan versi), port, apa yang divisualisasikan UI, skenario demo
ringkas, hasil `uji.sh` (keluaran ringkas), ukuran image yang dibangun, sisa disk sesudah selesai,
usulan baris tabel README root, dan hal yang perlu diputuskan pengampu.
