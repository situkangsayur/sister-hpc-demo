// Paket gangguan menyimpan pengaturan fault injection: service bisa dibuat
// lambat atau gagal dari UI agar timeout, retry, dan idempotency terlihat.
// Pengaturan disimpan di Postgres (satu baris) agar berlaku sama di semua
// replica, karena APISIX membagi retry ke replica yang berbeda.
package gangguan

import (
	"context"
	"database/sql"
	"math/rand/v2"
	"time"
)

// Atur adalah pengaturan gangguan untuk operasi Bayar.
type Atur struct {
	// LambatMs: jeda SESUDAH pembayaran diproses dan di-commit, sebelum response
	// dikirim. Ini meniru kasus 3 di buku: server selesai bekerja, tetapi response
	// tidak sampai ke klien sebelum timeout.
	LambatMs int `json:"lambat_ms"`
	// LambatKali: hanya N request Bayar berikutnya yang lambat (gangguan sesaat,
	// lalu pulih). 0 berarti semua request lambat.
	LambatKali int `json:"lambat_kali"`
	// GagalPersen: peluang (0–100) server menjawab 503/UNAVAILABLE SEBELUM memproses.
	GagalPersen int `json:"gagal_persen"`
	// SisaLambat hanya untuk dibaca: sisa request lambat (-1 = semua).
	SisaLambat int `json:"sisa_lambat"`
}

// Penjaga membaca dan menulis pengaturan di tabel gangguan.
type Penjaga struct{ DB *sql.DB }

func batasi(v, lo, hi int) int { return max(lo, min(hi, v)) }

// Set menyimpan pengaturan baru.
func (p *Penjaga) Set(ctx context.Context, a Atur) (Atur, error) {
	a.LambatMs = batasi(a.LambatMs, 0, 30000)
	a.GagalPersen = batasi(a.GagalPersen, 0, 100)
	a.LambatKali = batasi(a.LambatKali, 0, 1000)
	var sisa any // NULL = semua request
	if a.LambatKali > 0 {
		sisa = a.LambatKali
	}
	_, err := p.DB.ExecContext(ctx, `UPDATE gangguan SET lambat_ms = $1, lambat_sisa = $2, gagal_persen = $3 WHERE id = 1`,
		a.LambatMs, sisa, a.GagalPersen)
	if err != nil {
		return Atur{}, err
	}
	return p.Get(ctx)
}

// Get membaca pengaturan sekarang.
func (p *Penjaga) Get(ctx context.Context) (Atur, error) {
	var a Atur
	var sisa sql.NullInt64
	err := p.DB.QueryRowContext(ctx, `SELECT lambat_ms, lambat_sisa, gagal_persen FROM gangguan WHERE id = 1`).
		Scan(&a.LambatMs, &sisa, &a.GagalPersen)
	a.SisaLambat = -1
	if sisa.Valid {
		a.SisaLambat = int(sisa.Int64)
	}
	return a, err
}

// HarusGagal mengundi apakah request ini dibuat gagal sebelum diproses.
func (p *Penjaga) HarusGagal(ctx context.Context) bool {
	var persen int
	if p.DB.QueryRowContext(ctx, `SELECT gagal_persen FROM gangguan WHERE id = 1`).Scan(&persen) != nil {
		return false
	}
	return persen > 0 && rand.IntN(100) < persen
}

// AmbilLambat mengembalikan jeda untuk request ini dan mengurangi sisa jatah
// request lambat secara atomik (dua replica tidak bisa memakai jatah yang sama).
func (p *Penjaga) AmbilLambat(ctx context.Context) time.Duration {
	var ms int
	err := p.DB.QueryRowContext(ctx, `
		WITH u AS (
		  UPDATE gangguan SET lambat_sisa = lambat_sisa - 1
		  WHERE id = 1 AND lambat_ms > 0 AND lambat_sisa > 0
		  RETURNING lambat_ms
		)
		SELECT COALESCE((SELECT lambat_ms FROM u),
		                CASE WHEN g.lambat_sisa IS NULL THEN g.lambat_ms ELSE 0 END)
		FROM gangguan g WHERE g.id = 1`).Scan(&ms)
	if err != nil {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}
