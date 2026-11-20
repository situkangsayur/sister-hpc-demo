// Paket toko berisi data dan logika bisnis yang dipakai bersama oleh semua
// adapter protokol (REST, XML/SOAP, gRPC, GraphQL, SSE/WebSocket). Semua protokol
// membaca tabel Postgres yang sama, sehingga perbandingan antarprotokol adil:
// yang berbeda hanya cara data dikodekan dan dikirim.
package toko

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/lib/pq"
)

var (
	ErrTidakAda    = errors.New("produk tidak ditemukan")
	ErrValidasi    = errors.New("data tidak valid")
	ErrSaldo       = errors.New("saldo tidak cukup")
	ErrKunciBeda   = errors.New("idempotency key sudah dipakai untuk isi yang berbeda")
	ErrMasihProses = errors.New("request dengan idempotency key ini masih diproses")
)

// Produk sama bentuknya di semua protokol: id, nama, harga (seperti slide P04).
type Produk struct {
	ID    int64  `json:"id" xml:"id"`
	Nama  string `json:"nama" xml:"nama"`
	Harga int64  `json:"harga" xml:"harga"`
}

// Penjual hanya dipakai GraphQL untuk memperlihatkan masalah N+1.
type Penjual struct {
	ID   int64  `json:"id"`
	Nama string `json:"nama"`
	Kota string `json:"kota"`
}

// HasilBayar adalah hasil satu operasi bayar yang disimpan bersama idempotency key.
type HasilBayar struct {
	Sisa      int64  `json:"sisa"`
	IDBayar   string `json:"id_bayar"`
	DariCache bool   `json:"dari_cache"`
}

// Toko membungkus koneksi Postgres.
type Toko struct {
	db *sql.DB
}

// penghitungKey adalah kunci context untuk menghitung jumlah query per request.
type penghitungKey struct{}

// DenganPenghitung memasang penghitung query SQL di context. GraphQL memakainya
// untuk menunjukkan berapa kali database dipanggil (N+1 vs DataLoader).
func DenganPenghitung(ctx context.Context) (context.Context, *atomic.Int64) {
	n := &atomic.Int64{}
	return context.WithValue(ctx, penghitungKey{}, n), n
}

func hitung(ctx context.Context) {
	if n, ok := ctx.Value(penghitungKey{}).(*atomic.Int64); ok {
		n.Add(1)
	}
}

// Buka menyambung ke Postgres, mencoba ulang sampai database siap.
func Buka(ctx context.Context, url string) (*Toko, error) {
	db, err := sql.Open("postgres", url)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(20)
	for i := 0; ; i++ {
		c, batal := context.WithTimeout(ctx, 2*time.Second)
		err = db.PingContext(c)
		batal()
		if err == nil {
			break
		}
		if i > 30 {
			return nil, fmt.Errorf("database tidak siap: %w", err)
		}
		time.Sleep(time.Second)
	}
	t := &Toko{db: db}
	return t, t.siapkan(ctx)
}

// DB mengembalikan koneksi mentah (dipakai realtime untuk LISTEN/NOTIFY).
func (t *Toko) DB() *sql.DB { return t.db }

func (t *Toko) Tutup() error { return t.db.Close() }

// Ping dipakai health check.
func (t *Toko) Ping(ctx context.Context) error { return t.db.PingContext(ctx) }

const kunciSkema = 20260404

// siapkan membuat tabel dan data contoh di bawah advisory lock, karena dua replica
// katalog bisa mulai bersamaan.
func (t *Toko) siapkan(ctx context.Context) error {
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kunciSkema); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, skema); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM produk`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		for i, p := range daftarPenjual {
			if _, err := tx.ExecContext(ctx, `INSERT INTO penjual (id, nama, kota) VALUES ($1,$2,$3)`, i+1, p[0], p[1]); err != nil {
				return err
			}
		}
		for _, p := range ProdukContoh() {
			pj := p.ID%int64(len(daftarPenjual)) + 1
			if _, err := tx.ExecContext(ctx, `INSERT INTO produk (id, nama, harga, penjual_id) VALUES ($1,$2,$3,$4)`, p.ID, p.Nama, p.Harga, pj); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO saldo (akun, nilai) VALUES ('budi', 1000000), ('siti', 1000000)`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const skema = `
CREATE TABLE IF NOT EXISTS penjual (
  id   BIGINT PRIMARY KEY,
  nama TEXT NOT NULL,
  kota TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS produk (
  id         BIGINT PRIMARY KEY,
  nama       TEXT   NOT NULL,
  harga      BIGINT NOT NULL CHECK (harga >= 0),
  penjual_id BIGINT NOT NULL REFERENCES penjual(id)
);
CREATE TABLE IF NOT EXISTS saldo (
  akun  TEXT PRIMARY KEY,
  nilai BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS pembayaran (
  id     BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  akun   TEXT   NOT NULL,
  jumlah BIGINT NOT NULL,
  kunci  TEXT,
  waktu  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Pengaturan fault injection, satu baris untuk semua replica.
CREATE TABLE IF NOT EXISTS gangguan (
  id           INT PRIMARY KEY,
  lambat_ms    INT NOT NULL DEFAULT 0,
  lambat_sisa  INT,
  gagal_persen INT NOT NULL DEFAULT 0
);
INSERT INTO gangguan (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
-- Tabel idempotency: unique pada kunci menjamin dua retry tidak sama-sama lolos.
CREATE TABLE IF NOT EXISTS idem (
  kunci  TEXT PRIMARY KEY,
  sidik  TEXT NOT NULL,
  status TEXT NOT NULL,
  hasil  JSONB,
  dibuat TIMESTAMPTZ NOT NULL DEFAULT now()
);`

var daftarPenjual = [][2]string{
	{"Toko Berkah", "Bogor"}, {"Kedai Nusantara", "Bandung"}, {"Warung Sejahtera", "Jakarta"},
	{"Pasar Tani", "Malang"}, {"Rumah Rempah", "Yogyakarta"}, {"Gerai Amanah", "Depok"},
}

var namaProduk = []string{
	"Beras", "Gula Pasir", "Minyak Goreng", "Telur", "Tepung Terigu", "Garam", "Kecap", "Saus Sambal",
	"Mi Instan", "Susu UHT", "Roti Tawar", "Mentega", "Keju", "Sarden", "Kornet", "Teh Celup",
	"Sirup", "Air Mineral", "Biskuit", "Wafer", "Cokelat", "Kacang Tanah", "Kedelai", "Jagung",
	"Bawang Merah", "Bawang Putih", "Cabai", "Tomat", "Kentang", "Wortel", "Kol", "Bayam",
	"Kangkung", "Tahu", "Tempe", "Ayam", "Daging Sapi", "Ikan Kembung", "Udang", "Cumi",
	"Kerupuk", "Kopi", "Gula Aren", "Madu", "Kurma", "Jahe", "Kunyit", "Lengkuas",
	"Sabun", "Sampo",
}

// ProdukContoh mengembalikan 50 produk awal. Produk 42 adalah Kopi 25000,
// sama dengan contoh di slide P04 (JSON 37 byte, Protobuf 12 byte).
func ProdukContoh() []Produk {
	hasil := make([]Produk, len(namaProduk))
	for i, n := range namaProduk {
		id := int64(i + 1)
		hasil[i] = Produk{ID: id, Nama: n, Harga: 5000 + (id*7919)%20*1000}
	}
	hasil[41].Harga = 25000 // Kopi
	return hasil
}

// Ambil membaca satu produk.
func (t *Toko) Ambil(ctx context.Context, id int64) (Produk, error) {
	hitung(ctx)
	var p Produk
	err := t.db.QueryRowContext(ctx, `SELECT id, nama, harga FROM produk WHERE id = $1`, id).Scan(&p.ID, &p.Nama, &p.Harga)
	if errors.Is(err, sql.ErrNoRows) {
		return Produk{}, ErrTidakAda
	}
	return p, err
}

// AmbilLengkap membaca satu produk beserta id penjualnya (untuk GraphQL).
func (t *Toko) AmbilLengkap(ctx context.Context, id int64) (Produk, int64, error) {
	hitung(ctx)
	var p Produk
	var pj int64
	err := t.db.QueryRowContext(ctx, `SELECT id, nama, harga, penjual_id FROM produk WHERE id = $1`, id).Scan(&p.ID, &p.Nama, &p.Harga, &pj)
	if errors.Is(err, sql.ErrNoRows) {
		return Produk{}, 0, ErrTidakAda
	}
	return p, pj, err
}

// Daftar membaca semua produk terurut id.
func (t *Toko) Daftar(ctx context.Context) ([]Produk, error) {
	hitung(ctx)
	baris, err := t.db.QueryContext(ctx, `SELECT id, nama, harga FROM produk ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer baris.Close()
	hasil := []Produk{}
	for baris.Next() {
		var p Produk
		if err := baris.Scan(&p.ID, &p.Nama, &p.Harga); err != nil {
			return nil, err
		}
		hasil = append(hasil, p)
	}
	return hasil, baris.Err()
}

// Penjual mengambil satu penjual (versi naif: satu query per produk).
func (t *Toko) Penjual(ctx context.Context, id int64) (Penjual, error) {
	hitung(ctx)
	var p Penjual
	err := t.db.QueryRowContext(ctx, `SELECT id, nama, kota FROM penjual WHERE id = $1`, id).Scan(&p.ID, &p.Nama, &p.Kota)
	return p, err
}

// PenjualBanyak mengambil banyak penjual dalam satu query (dipakai DataLoader).
func (t *Toko) PenjualBanyak(ctx context.Context, ids []int64) (map[int64]Penjual, error) {
	hitung(ctx)
	baris, err := t.db.QueryContext(ctx, `SELECT id, nama, kota FROM penjual WHERE id = ANY($1)`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer baris.Close()
	hasil := map[int64]Penjual{}
	for baris.Next() {
		var p Penjual
		if err := baris.Scan(&p.ID, &p.Nama, &p.Kota); err != nil {
			return nil, err
		}
		hasil[p.ID] = p
	}
	return hasil, baris.Err()
}

// ProdukDenganPenjual membaca daftar produk beserta penjual_id (untuk GraphQL).
func (t *Toko) ProdukDenganPenjual(ctx context.Context) ([]Produk, map[int64]int64, error) {
	hitung(ctx)
	baris, err := t.db.QueryContext(ctx, `SELECT id, nama, harga, penjual_id FROM produk ORDER BY id`)
	if err != nil {
		return nil, nil, err
	}
	defer baris.Close()
	hasil, pj := []Produk{}, map[int64]int64{}
	for baris.Next() {
		var p Produk
		var j int64
		if err := baris.Scan(&p.ID, &p.Nama, &p.Harga, &j); err != nil {
			return nil, nil, err
		}
		hasil = append(hasil, p)
		pj[p.ID] = j
	}
	return hasil, pj, baris.Err()
}

// SaluranHarga adalah nama channel Postgres NOTIFY untuk perubahan harga. Semua
// replica katalog berlangganan channel ini, sehingga klien SSE/WebSocket di
// replica mana pun menerima perubahan (pola pub-sub di buku, Gambar b04-ws).
const SaluranHarga = "harga_berubah"

// SaluranObrolan dipakai chat WebSocket antarreplica.
const SaluranObrolan = "obrolan"

// PerubahanHarga adalah isi NOTIFY untuk setiap perubahan harga.
type PerubahanHarga struct {
	Produk
	WaktuMs int64  `json:"waktu_ms"`
	Oleh    string `json:"oleh,omitempty"` // protokol yang mengubah: rest, soap, grpc, graphql, ticker
}

type olehKey struct{}

// DenganOleh mencatat protokol pengubah harga di context.
func DenganOleh(ctx context.Context, oleh string) context.Context {
	return context.WithValue(ctx, olehKey{}, oleh)
}

// Oleh membaca protokol pengubah dari context.
func Oleh(ctx context.Context) string {
	s, _ := ctx.Value(olehKey{}).(string)
	return s
}

// UbahHarga memperbarui harga lalu mengumumkannya lewat NOTIFY dalam transaksi
// yang sama (notifikasi hanya terkirim bila commit berhasil).
func (t *Toko) UbahHarga(ctx context.Context, id, harga int64) (Produk, error) {
	if harga < 0 || harga > 2_000_000_000 {
		return Produk{}, ErrValidasi
	}
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return Produk{}, err
	}
	defer tx.Rollback()
	var p Produk
	err = tx.QueryRowContext(ctx, `UPDATE produk SET harga = $2 WHERE id = $1 RETURNING id, nama, harga`, id, harga).Scan(&p.ID, &p.Nama, &p.Harga)
	if errors.Is(err, sql.ErrNoRows) {
		return Produk{}, ErrTidakAda
	}
	if err != nil {
		return Produk{}, err
	}
	isi, _ := json.Marshal(PerubahanHarga{Produk: p, WaktuMs: time.Now().UnixMilli(), Oleh: Oleh(ctx)})
	if _, err := tx.ExecContext(ctx, `SELECT pg_notify($1, $2)`, SaluranHarga, string(isi)); err != nil {
		return Produk{}, err
	}
	return p, tx.Commit()
}

// Umumkan mengirim pesan bebas ke channel NOTIFY (dipakai chat).
func (t *Toko) Umumkan(ctx context.Context, saluran, isi string) error {
	_, err := t.db.ExecContext(ctx, `SELECT pg_notify($1, $2)`, saluran, isi)
	return err
}

// Saldo membaca saldo akun.
func (t *Toko) Saldo(ctx context.Context, akun string) (int64, int64, error) {
	var nilai, kali int64
	err := t.db.QueryRowContext(ctx, `SELECT s.nilai, (SELECT count(*) FROM pembayaran p WHERE p.akun = s.akun)
		FROM saldo s WHERE s.akun = $1`, akun).Scan(&nilai, &kali)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, ErrTidakAda
	}
	return nilai, kali, err
}

// ResetSaldo mengembalikan saldo akun ke nilai awal dan menghapus riwayatnya.
func (t *Toko) ResetSaldo(ctx context.Context, akun string, nilai int64) error {
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO saldo (akun, nilai) VALUES ($1,$2)
		ON CONFLICT (akun) DO UPDATE SET nilai = EXCLUDED.nilai`, akun, nilai); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pembayaran WHERE akun = $1`, akun); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM idem`); err != nil {
		return err
	}
	return tx.Commit()
}

// Sidik adalah hash isi request, untuk mendeteksi kunci yang dipakai ulang
// dengan isi berbeda (422 menurut Algoritma b04-idem).
func Sidik(akun string, jumlah int64) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d", akun, jumlah)))
	return hex.EncodeToString(h[:])
}

// Bayar memotong saldo. Bila kunci tidak kosong, pemrosesan mengikuti Algoritma
// b04-idem di buku: kunci dan efek bisnis disimpan dalam SATU transaksi dengan
// unique constraint, sehingga retry dengan kunci sama hanya memotong sekali.
// Tanpa kunci, setiap panggilan memotong saldo (at-least-once tanpa idempotency).
func (t *Toko) Bayar(ctx context.Context, kunci, akun string, jumlah int64) (HasilBayar, error) {
	if jumlah <= 0 || akun == "" {
		return HasilBayar{}, ErrValidasi
	}
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return HasilBayar{}, err
	}
	defer tx.Rollback()

	if kunci != "" {
		sidik := Sidik(akun, jumlah)
		res, err := tx.ExecContext(ctx, `INSERT INTO idem (kunci, sidik, status) VALUES ($1,$2,'proses')
			ON CONFLICT (kunci) DO NOTHING`, kunci, sidik)
		if err != nil {
			return HasilBayar{}, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			// kunci sudah ada: jangan proses ulang
			tx.Rollback()
			var sLama, status string
			var hasil []byte
			if err := t.db.QueryRowContext(ctx, `SELECT sidik, status, hasil FROM idem WHERE kunci = $1`, kunci).
				Scan(&sLama, &status, &hasil); err != nil {
				return HasilBayar{}, err
			}
			if sLama != sidik {
				return HasilBayar{}, ErrKunciBeda
			}
			if status != "selesai" {
				return HasilBayar{}, ErrMasihProses
			}
			var h HasilBayar
			if err := json.Unmarshal(hasil, &h); err != nil {
				return HasilBayar{}, err
			}
			h.DariCache = true
			return h, nil
		}
	}

	var sisa int64
	err = tx.QueryRowContext(ctx, `UPDATE saldo SET nilai = nilai - $2 WHERE akun = $1 AND nilai >= $2 RETURNING nilai`, akun, jumlah).Scan(&sisa)
	if errors.Is(err, sql.ErrNoRows) {
		return HasilBayar{}, ErrSaldo
	}
	if err != nil {
		return HasilBayar{}, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO pembayaran (akun, jumlah, kunci) VALUES ($1,$2,NULLIF($3,'')) RETURNING id`,
		akun, jumlah, kunci).Scan(&id); err != nil {
		return HasilBayar{}, err
	}
	h := HasilBayar{Sisa: sisa, IDBayar: fmt.Sprintf("bayar-%d", id)}
	if kunci != "" {
		isi, _ := json.Marshal(h)
		if _, err := tx.ExecContext(ctx, `UPDATE idem SET status = 'selesai', hasil = $2 WHERE kunci = $1`, kunci, isi); err != nil {
			return HasilBayar{}, err
		}
	}
	return h, tx.Commit()
}
