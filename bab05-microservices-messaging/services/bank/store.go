package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"sister-bab05/services/platform"
)

// Skema database "bank". Nama tabel, kolom, dan nilai enum mengikuti kontrak data DE;
// kolom tambahan (saldo_setelah, transfer_id, akad_id, idempotency_key, ...) boleh ada.
var schema = []string{
	`CREATE SEQUENCE IF NOT EXISTS cif_seq START 1001`,
	`CREATE SEQUENCE IF NOT EXISTS rekening_seq START 100001`,
	`CREATE TABLE IF NOT EXISTS cabang (
	  id INT PRIMARY KEY, kode TEXT NOT NULL UNIQUE, nama TEXT NOT NULL, kota TEXT NOT NULL, wilayah TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS nasabah (
	  id UUID PRIMARY KEY, cif TEXT NOT NULL UNIQUE, nama TEXT NOT NULL,
	  nik TEXT, no_hp TEXT, email TEXT, kota TEXT,
	  segmen TEXT NOT NULL DEFAULT 'ritel' CHECK (segmen IN ('ritel','prioritas','umkm')),
	  cabang_id INT REFERENCES cabang(id),
	  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
	`CREATE TABLE IF NOT EXISTS rekening (
	  id UUID PRIMARY KEY, no_rekening TEXT NOT NULL UNIQUE, nasabah_id UUID NOT NULL REFERENCES nasabah(id),
	  jenis TEXT NOT NULL CHECK (jenis IN ('tabungan_wadiah','giro','deposito_mudharabah')),
	  saldo BIGINT NOT NULL DEFAULT 0 CHECK (saldo >= 0),
	  status TEXT NOT NULL DEFAULT 'aktif' CHECK (status IN ('aktif','diblokir','ditutup')),
	  cabang_id INT REFERENCES cabang(id),
	  dibuka_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
	`CREATE TABLE IF NOT EXISTS akad_pembiayaan (
	  id UUID PRIMARY KEY, nasabah_id UUID NOT NULL REFERENCES nasabah(id), rekening_id UUID REFERENCES rekening(id),
	  kode_akad TEXT NOT NULL, pokok BIGINT NOT NULL CHECK (pokok > 0), margin BIGINT NOT NULL DEFAULT 0,
	  tenor_bulan INT NOT NULL CHECK (tenor_bulan > 0),
	  status TEXT NOT NULL DEFAULT 'diajukan' CHECK (status IN ('diajukan','disetujui','cair','lunas','ditolak')),
	  terbayar BIGINT NOT NULL DEFAULT 0,
	  diajukan_at TIMESTAMPTZ NOT NULL DEFAULT now(), cair_at TIMESTAMPTZ, updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
	`CREATE TABLE IF NOT EXISTS transaksi (
	  id UUID PRIMARY KEY, rekening_id UUID NOT NULL REFERENCES rekening(id), cabang_id INT REFERENCES cabang(id),
	  kanal TEXT NOT NULL CHECK (kanal IN ('mobile','atm','teller','web','ecommerce')),
	  jenis TEXT NOT NULL CHECK (jenis IN ('setor','tarik','transfer_masuk','transfer_keluar','bayar_belanja','angsuran')),
	  nominal BIGINT NOT NULL CHECK (nominal > 0), saldo_setelah BIGINT NOT NULL,
	  occurred_at TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	  ref_order_id TEXT, transfer_id UUID, akad_id UUID REFERENCES akad_pembiayaan(id),
	  idempotency_key TEXT UNIQUE, keterangan TEXT NOT NULL DEFAULT '', request_id TEXT NOT NULL DEFAULT '')`,
	// Satu order toko hanya bisa dibayar sekali: kunci idempotency alami untuk payment.
	`CREATE UNIQUE INDEX IF NOT EXISTS transaksi_bayar_order ON transaksi(ref_order_id) WHERE jenis = 'bayar_belanja'`,
	`CREATE INDEX IF NOT EXISTS transaksi_rek_created ON transaksi(rekening_id, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS transaksi_created ON transaksi(created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS rekening_nasabah ON rekening(nasabah_id)`,
	// Publication untuk Debezium (pgoutput). Dibuat di sini karena tabel baru ada sesudah migrasi.
	`DO $$ BEGIN
	  IF NOT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'dbz_bank') THEN
	    CREATE PUBLICATION dbz_bank FOR TABLE cabang, nasabah, rekening, transaksi, akad_pembiayaan;
	  END IF;
	END $$`,
	// REPLICA IDENTITY FULL: WAL menyimpan seluruh baris lama saat UPDATE/DELETE, sehingga envelope
	// Debezium membawa "before" lengkap (bahan SCD-2 dim_nasabah). Hanya tabel yang di-UPDATE;
	// transaksi dan cabang insert-only. ALTER hanya dijalankan bila belum FULL (idempotent, tanpa
	// lock ACCESS EXCLUSIVE setiap kali service start).
	`DO $$ DECLARE t text; BEGIN
	  FOREACH t IN ARRAY ARRAY['nasabah','rekening','akad_pembiayaan'] LOOP
	    IF (SELECT relreplident FROM pg_class WHERE oid = t::regclass) <> 'f' THEN
	      EXECUTE format('ALTER TABLE %I REPLICA IDENTITY FULL', t);
	    END IF;
	  END LOOP;
	END $$`,
}

// ---------- baris entitas (tag JSON = nama kolom, dipakai juga sebagai data event) ----------

type cabang struct {
	ID      int    `json:"id"`
	Kode    string `json:"kode"`
	Nama    string `json:"nama"`
	Kota    string `json:"kota"`
	Wilayah string `json:"wilayah"`
}

type nasabah struct {
	ID        string    `json:"id"`
	CIF       string    `json:"cif"`
	Nama      string    `json:"nama"`
	NIK       *string   `json:"nik"`
	NoHP      *string   `json:"no_hp"`
	Email     *string   `json:"email"`
	Kota      *string   `json:"kota"`
	Segmen    string    `json:"segmen"`
	CabangID  *int      `json:"cabang_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type rekening struct {
	ID         string    `json:"id"`
	NoRekening string    `json:"no_rekening"`
	NasabahID  string    `json:"nasabah_id"`
	Jenis      string    `json:"jenis"`
	Saldo      int64     `json:"saldo"`
	Status     string    `json:"status"`
	CabangID   *int      `json:"cabang_id"`
	DibukaAt   time.Time `json:"dibuka_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type transaksi struct {
	ID             string    `json:"id"`
	RekeningID     string    `json:"rekening_id"`
	CabangID       *int      `json:"cabang_id"`
	Kanal          string    `json:"kanal"`
	Jenis          string    `json:"jenis"`
	Nominal        int64     `json:"nominal"`
	SaldoSetelah   int64     `json:"saldo_setelah"`
	OccurredAt     time.Time `json:"occurred_at"`
	CreatedAt      time.Time `json:"created_at"`
	RefOrderID     *string   `json:"ref_order_id"`
	TransferID     *string   `json:"transfer_id"`
	AkadID         *string   `json:"akad_id"`
	IdempotencyKey *string   `json:"idempotency_key"`
	Keterangan     string    `json:"keterangan"`
	RequestID      string    `json:"request_id"`
}

type akad struct {
	ID         string     `json:"id"`
	NasabahID  string     `json:"nasabah_id"`
	RekeningID *string    `json:"rekening_id"`
	KodeAkad   string     `json:"kode_akad"`
	Pokok      int64      `json:"pokok"`
	Margin     int64      `json:"margin"`
	TenorBulan int        `json:"tenor_bulan"`
	Status     string     `json:"status"`
	Terbayar   int64      `json:"terbayar"`
	DiajukanAt time.Time  `json:"diajukan_at"`
	CairAt     *time.Time `json:"cair_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

const (
	cabangCols    = `id, kode, nama, kota, wilayah`
	nasabahCols   = `id::text, cif, nama, nik, no_hp, email, kota, segmen, cabang_id, created_at, updated_at`
	rekeningCols  = `id::text, no_rekening, nasabah_id::text, jenis, saldo, status, cabang_id, dibuka_at, updated_at`
	transaksiCols = `id::text, rekening_id::text, cabang_id, kanal, jenis, nominal, saldo_setelah, occurred_at, created_at,
	  ref_order_id, transfer_id::text, akad_id::text, idempotency_key, keterangan, request_id`
	akadCols = `id::text, nasabah_id::text, rekening_id::text, kode_akad, pokok, margin, tenor_bulan, status, terbayar,
	  diajukan_at, cair_at, updated_at`
)

func scanCabang(r pgx.Row) (c cabang, err error) {
	err = r.Scan(&c.ID, &c.Kode, &c.Nama, &c.Kota, &c.Wilayah)
	return
}

func scanNasabah(r pgx.Row) (n nasabah, err error) {
	err = r.Scan(&n.ID, &n.CIF, &n.Nama, &n.NIK, &n.NoHP, &n.Email, &n.Kota, &n.Segmen, &n.CabangID, &n.CreatedAt, &n.UpdatedAt)
	return
}

func scanRekening(r pgx.Row) (k rekening, err error) {
	err = r.Scan(&k.ID, &k.NoRekening, &k.NasabahID, &k.Jenis, &k.Saldo, &k.Status, &k.CabangID, &k.DibukaAt, &k.UpdatedAt)
	return
}

func scanTransaksi(r pgx.Row) (t transaksi, err error) {
	err = r.Scan(&t.ID, &t.RekeningID, &t.CabangID, &t.Kanal, &t.Jenis, &t.Nominal, &t.SaldoSetelah, &t.OccurredAt,
		&t.CreatedAt, &t.RefOrderID, &t.TransferID, &t.AkadID, &t.IdempotencyKey, &t.Keterangan, &t.RequestID)
	return
}

func scanAkad(r pgx.Row) (a akad, err error) {
	err = r.Scan(&a.ID, &a.NasabahID, &a.RekeningID, &a.KodeAkad, &a.Pokok, &a.Margin, &a.TenorBulan, &a.Status,
		&a.Terbayar, &a.DiajukanAt, &a.CairAt, &a.UpdatedAt)
	return
}

func collect[T any](rows pgx.Rows, err error, scan func(pgx.Row) (T, error)) ([]T, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func isFK(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23503"
}

func nullable(s string) *string {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	return &s
}

// ---------- seed ----------

var seedCabang = []cabang{
	{1, "JKT-SDR", "KC Jakarta Sudirman", "Jakarta", "Jabodetabek"},
	{2, "JKT-KGD", "KCP Jakarta Kelapa Gading", "Jakarta", "Jabodetabek"},
	{3, "BGR-01", "KC Bogor", "Bogor", "Jabodetabek"},
	{4, "BDG-01", "KC Bandung Asia Afrika", "Bandung", "Jawa Barat"},
	{5, "YOG-01", "KC Yogyakarta", "Yogyakarta", "Jawa Tengah & DIY"},
	{6, "SBY-01", "KC Surabaya Darmo", "Surabaya", "Jawa Timur"},
	{7, "MDN-01", "KC Medan", "Medan", "Sumatra"},
	{8, "MKS-01", "KC Makassar", "Makassar", "Sulawesi"},
}

// seed mengisi cabang (event bank.cabang.dibuat hanya untuk baris baru) dan tiga nasabah
// contoh bila tabel nasabah masih kosong, agar demo checkout dari rekening langsung bisa dicoba.
func (s *svc) seed(ctx context.Context) error {
	return platform.InTx(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(5006)`); err != nil {
			return err
		}
		for _, c := range seedCabang {
			tag, err := tx.Exec(ctx, `INSERT INTO cabang (`+cabangCols+`) VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
				c.ID, c.Kode, c.Nama, c.Kota, c.Wilayah)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 1 {
				if _, err := platform.EnqueueEvent(ctx, tx, "bank.cabang.dibuat", time.Now(), c); err != nil {
					return err
				}
			}
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM nasabah`).Scan(&n); err != nil || n > 0 {
			return err
		}
		demo := []struct {
			in    nasabahIn
			saldo int64
		}{
			{nasabahIn{Nama: "Aisyah Rahmawati", NIK: "3273015001900001", NoHP: "081200000001", Email: "aisyah@contoh.id", Kota: "Bandung", Segmen: "ritel", CabangID: 4}, 2_000_000},
			{nasabahIn{Nama: "Budi Santoso", NIK: "3578011002850002", NoHP: "081200000002", Email: "budi@contoh.id", Kota: "Surabaya", Segmen: "umkm", CabangID: 6}, 5_000_000},
			{nasabahIn{Nama: "Citra Lestari", NIK: "3171016003950003", NoHP: "081200000003", Email: "citra@contoh.id", Kota: "Jakarta", Segmen: "prioritas", CabangID: 1}, 150_000},
		}
		for _, d := range demo {
			nb, err := s.insertNasabah(ctx, tx, d.in)
			if err != nil {
				return err
			}
			if _, err := s.insertRekening(ctx, tx, nb, rekeningIn{Jenis: "tabungan_wadiah", SetoranAwal: d.saldo, Kanal: "teller"}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---------- nasabah ----------

type nasabahIn struct {
	Nama     string `json:"nama"`
	NIK      string `json:"nik"`
	NoHP     string `json:"no_hp"`
	Email    string `json:"email"`
	Kota     string `json:"kota"`
	Segmen   string `json:"segmen"`
	CabangID int    `json:"cabang_id"`
	// OccurredAt (opsional, RFC3339) = waktu bisnis pendaftaran: created_at, updated_at, dan
	// occurred_at event. Kosong = sekarang; lampau boleh (backfill); > 5 menit ke depan ditolak.
	OccurredAt string `json:"occurred_at"`
}

func (s *svc) insertNasabah(ctx context.Context, tx pgx.Tx, in nasabahIn) (nasabah, error) {
	if strings.TrimSpace(in.Nama) == "" {
		return nasabah{}, bad(400, "nama wajib diisi")
	}
	occ, err := parseOccurred(in.OccurredAt, time.Now())
	if err != nil {
		return nasabah{}, err
	}
	if in.Segmen == "" {
		in.Segmen = "ritel"
	}
	if !contains(Segmen, in.Segmen) {
		return nasabah{}, bad(400, "segmen harus salah satu dari %v", Segmen)
	}
	var cab *int
	if in.CabangID != 0 {
		cab = &in.CabangID
	}
	n, err := scanNasabah(tx.QueryRow(ctx, `INSERT INTO nasabah (id, cif, nama, nik, no_hp, email, kota, segmen, cabang_id, created_at, updated_at)
		VALUES ($1, 'CIF' || lpad(nextval('cif_seq')::text, 8, '0'), $2,$3,$4,$5,$6,$7,$8,$9,$9) RETURNING `+nasabahCols,
		platform.NewID(), strings.TrimSpace(in.Nama), nullable(in.NIK), nullable(in.NoHP), nullable(in.Email), nullable(in.Kota), in.Segmen, cab, occ))
	if isFK(err) {
		return n, bad(400, "cabang_id %d tidak ada", in.CabangID)
	}
	if err != nil {
		return n, err
	}
	_, err = platform.EnqueueEvent(ctx, tx, "bank.nasabah.dibuat", n.CreatedAt, n)
	return n, err
}

type nasabahPatch struct {
	Kota     *string `json:"kota"`
	Segmen   *string `json:"segmen"`
	NoHP     *string `json:"no_hp"`
	Email    *string `json:"email"`
	CabangID *int    `json:"cabang_id"`
	// OccurredAt (opsional) = waktu bisnis perubahan profil: updated_at dan occurred_at event.
	OccurredAt string `json:"occurred_at"`
}

func (s *svc) updateNasabah(ctx context.Context, id string, p nasabahPatch) (n nasabah, err error) {
	if p.Segmen != nil && !contains(Segmen, *p.Segmen) {
		return n, bad(400, "segmen harus salah satu dari %v", Segmen)
	}
	occ, err := parseOccurred(p.OccurredAt, time.Now())
	if err != nil {
		return n, err
	}
	err = platform.InTx(ctx, s.db, func(tx pgx.Tx) error {
		cur, err := scanNasabah(tx.QueryRow(ctx, `SELECT `+nasabahCols+` FROM nasabah WHERE id::text=$1 OR cif=$1 FOR UPDATE`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return bad(404, "nasabah tidak ditemukan")
		}
		if err != nil {
			return err
		}
		if p.Kota != nil {
			cur.Kota = nullable(*p.Kota)
		}
		if p.Segmen != nil {
			cur.Segmen = *p.Segmen
		}
		if p.NoHP != nil {
			cur.NoHP = nullable(*p.NoHP)
		}
		if p.Email != nil {
			cur.Email = nullable(*p.Email)
		}
		if p.CabangID != nil {
			cur.CabangID = p.CabangID
		}
		n, err = scanNasabah(tx.QueryRow(ctx, `UPDATE nasabah SET kota=$2, segmen=$3, no_hp=$4, email=$5, cabang_id=$6, updated_at=$7
			WHERE id=$1 RETURNING `+nasabahCols, cur.ID, cur.Kota, cur.Segmen, cur.NoHP, cur.Email, cur.CabangID, occ))
		if isFK(err) {
			return bad(400, "cabang_id tidak ada")
		}
		if err != nil {
			return err
		}
		_, err = platform.EnqueueEvent(ctx, tx, "bank.nasabah.diperbarui", n.UpdatedAt, n)
		return err
	})
	return n, err
}

// ---------- rekening ----------

type rekeningIn struct {
	NasabahID   string `json:"nasabah_id"`
	Jenis       string `json:"jenis"`
	SetoranAwal int64  `json:"setoran_awal"`
	CabangID    int    `json:"cabang_id"`
	Kanal       string `json:"kanal"`
	// OccurredAt (opsional) = waktu bisnis pembukaan: dibuka_at, updated_at, occurred_at event
	// bank.rekening.dibuka, dan occurred_at setoran awal.
	OccurredAt string `json:"occurred_at"`
}

func (s *svc) insertRekening(ctx context.Context, tx pgx.Tx, nb nasabah, in rekeningIn) (rekening, error) {
	if in.Jenis == "" {
		in.Jenis = "tabungan_wadiah"
	}
	if !contains(JenisRekening, in.Jenis) {
		return rekening{}, bad(400, "jenis rekening harus salah satu dari %v", JenisRekening)
	}
	occ, err := parseOccurred(in.OccurredAt, time.Now())
	if err != nil {
		return rekening{}, err
	}
	cab := nb.CabangID
	if in.CabangID != 0 {
		cab = &in.CabangID
	}
	k, err := scanRekening(tx.QueryRow(ctx, `INSERT INTO rekening (id, no_rekening, nasabah_id, jenis, cabang_id, dibuka_at, updated_at)
		VALUES ($1, $2 || lpad(nextval('rekening_seq')::text, 9, '0'), $3, $4, $5, $6, $6) RETURNING `+rekeningCols,
		platform.NewID(), prefixRekening(in.Jenis), nb.ID, in.Jenis, cab, occ))
	if isFK(err) {
		return k, bad(400, "cabang_id tidak ada")
	}
	if err != nil {
		return k, err
	}
	if _, err := platform.EnqueueEvent(ctx, tx, "bank.rekening.dibuka", k.DibukaAt, k); err != nil {
		return k, err
	}
	if in.SetoranAwal > 0 {
		if in.Kanal == "" {
			in.Kanal = "teller"
		}
		t, err := s.post(ctx, tx, &k, postIn{Jenis: "setor", Nominal: in.SetoranAwal, Kanal: in.Kanal, OccurredAt: k.DibukaAt,
			Keterangan: "setoran awal"})
		if err != nil {
			return k, err
		}
		k.Saldo = t.SaldoSetelah
	}
	return k, nil
}

func (s *svc) openRekening(ctx context.Context, in rekeningIn) (k rekening, err error) {
	err = platform.InTx(ctx, s.db, func(tx pgx.Tx) error {
		nb, err := scanNasabah(tx.QueryRow(ctx, `SELECT `+nasabahCols+` FROM nasabah WHERE id::text=$1 OR cif=$1`, in.NasabahID))
		if errors.Is(err, pgx.ErrNoRows) {
			return bad(404, "nasabah %q tidak ditemukan", in.NasabahID)
		}
		if err != nil {
			return err
		}
		k, err = s.insertRekening(ctx, tx, nb, in)
		return err
	})
	return k, err
}

// lockRekening mengunci baris rekening (id atau no_rekening) untuk transaksi.
func lockRekening(ctx context.Context, tx pgx.Tx, ref string) (rekening, error) {
	k, err := scanRekening(tx.QueryRow(ctx, `SELECT `+rekeningCols+` FROM rekening WHERE id::text=$1 OR no_rekening=$1 FOR UPDATE`, ref))
	if errors.Is(err, pgx.ErrNoRows) {
		return k, bad(404, "rekening %q tidak ditemukan", ref)
	}
	return k, err
}

// ---------- transaksi ----------

type postIn struct {
	Jenis          string
	Nominal        int64
	Kanal          string
	CabangID       *int
	OccurredAt     time.Time
	RefOrderID     *string
	TransferID     *string
	AkadID         *string
	IdempotencyKey *string
	Keterangan     string
}

// post mencatat satu transaksi pada rekening yang SUDAH dikunci: hitung saldo, update
// rekening, insert transaksi, dan event bank.transaksi.dibuat di outbox (satu transaksi DB).
func (s *svc) post(ctx context.Context, tx pgx.Tx, k *rekening, in postIn) (transaksi, error) {
	if !contains(Kanal, in.Kanal) {
		return transaksi{}, bad(400, "kanal harus salah satu dari %v", Kanal)
	}
	if k.Status != "aktif" {
		return transaksi{}, bad(422, "rekening %s berstatus %s", k.NoRekening, k.Status)
	}
	saldo, err := applySaldo(k.Saldo, in.Jenis, in.Nominal)
	if errors.Is(err, ErrSaldoKurang) {
		return transaksi{}, bad(409, "saldo tidak cukup: saldo %d, diminta %d", k.Saldo, in.Nominal)
	}
	if err != nil {
		return transaksi{}, err
	}
	if in.CabangID == nil {
		in.CabangID = k.CabangID // tanpa cabang (mis. kanal mobile): pakai cabang pembuka rekening
	}
	if _, err := tx.Exec(ctx, `UPDATE rekening SET saldo=$2, updated_at=now() WHERE id=$1`, k.ID, saldo); err != nil {
		return transaksi{}, err
	}
	k.Saldo = saldo
	t, err := scanTransaksi(tx.QueryRow(ctx, `INSERT INTO transaksi (id, rekening_id, cabang_id, kanal, jenis, nominal, saldo_setelah,
		occurred_at, ref_order_id, transfer_id, akad_id, idempotency_key, keterangan, request_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING `+transaksiCols,
		platform.NewID(), k.ID, in.CabangID, in.Kanal, in.Jenis, in.Nominal, saldo, in.OccurredAt, in.RefOrderID,
		in.TransferID, in.AkadID, in.IdempotencyKey, in.Keterangan, platform.RequestID(ctx)))
	if isFK(err) {
		return t, bad(400, "cabang_id tidak ada")
	}
	if err != nil {
		return t, err
	}
	// occurred_at event = waktu kejadian di perangkat, sehingga data terlambat masuk partisi aslinya.
	_, err = platform.EnqueueEvent(ctx, tx, "bank.transaksi.dibuat", t.OccurredAt, t)
	return t, err
}

func (s *svc) byIdemKey(ctx context.Context, key string) ([]transaksi, error) {
	rows, err := s.db.Query(ctx, `SELECT `+transaksiCols+` FROM transaksi WHERE idempotency_key = $1 OR idempotency_key = $1 || ':masuk'
		ORDER BY created_at`, key)
	return collect(rows, err, scanTransaksi)
}

// withIdem menjalankan fn sekali per Idempotency-Key: request ulang mengembalikan transaksi lama.
func (s *svc) withIdem(ctx context.Context, key string, fn func(tx pgx.Tx) ([]transaksi, error)) (out []transaksi, replayed bool, err error) {
	if key != "" {
		if prev, err := s.byIdemKey(ctx, key); err == nil && len(prev) > 0 {
			return prev, true, nil
		}
	}
	err = platform.InTx(ctx, s.db, func(tx pgx.Tx) error {
		out, err = fn(tx)
		return err
	})
	if err != nil && key != "" && isUnique(err) {
		if prev, e := s.byIdemKey(ctx, key); e == nil && len(prev) > 0 {
			return prev, true, nil
		}
	}
	return out, false, err
}

// ---------- akad pembiayaan ----------

type akadIn struct {
	NasabahID  string `json:"nasabah_id"`
	RekeningID string `json:"rekening_id"`
	KodeAkad   string `json:"kode_akad"`
	Pokok      int64  `json:"pokok"`
	Margin     int64  `json:"margin"`
	TenorBulan int    `json:"tenor_bulan"`
	// OccurredAt (opsional) = waktu bisnis pengajuan: diajukan_at, updated_at, occurred_at event.
	OccurredAt string `json:"occurred_at"`
}

func (s *svc) ajukanAkad(ctx context.Context, in akadIn) (a akad, err error) {
	if in.Pokok <= 0 || in.TenorBulan <= 0 || in.Margin < 0 {
		return a, bad(400, "pokok > 0, margin >= 0, tenor_bulan > 0")
	}
	occ, err := parseOccurred(in.OccurredAt, time.Now())
	if err != nil {
		return a, err
	}
	err = platform.InTx(ctx, s.db, func(tx pgx.Tx) error {
		var nid string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM nasabah WHERE id::text=$1 OR cif=$1`, in.NasabahID).Scan(&nid); err != nil {
			return bad(404, "nasabah %q tidak ditemukan", in.NasabahID)
		}
		var rek *string
		if in.RekeningID != "" {
			var rid string
			if err := tx.QueryRow(ctx, `SELECT id::text FROM rekening WHERE (id::text=$1 OR no_rekening=$1) AND nasabah_id=$2`,
				in.RekeningID, nid).Scan(&rid); err != nil {
				return bad(400, "rekening %q bukan milik nasabah ini", in.RekeningID)
			}
			rek = &rid
		} else {
			var rid string
			if tx.QueryRow(ctx, `SELECT id::text FROM rekening WHERE nasabah_id=$1 AND jenis<>'deposito_mudharabah'
				ORDER BY dibuka_at LIMIT 1`, nid).Scan(&rid) == nil {
				rek = &rid
			}
		}
		// kode_akad disimpan APA ADANYA (tanpa validasi), sesuai kontrak data.
		a, err = scanAkad(tx.QueryRow(ctx, `INSERT INTO akad_pembiayaan (id, nasabah_id, rekening_id, kode_akad, pokok, margin, tenor_bulan, diajukan_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8) RETURNING `+akadCols, platform.NewID(), nid, rek, in.KodeAkad, in.Pokok, in.Margin, in.TenorBulan, occ))
		if err != nil {
			return err
		}
		_, err = platform.EnqueueEvent(ctx, tx, "bank.akad.diajukan", a.DiajukanAt, a)
		return err
	})
	return a, err
}

type akadAksiIn struct {
	Nominal    int64  `json:"nominal"`
	Kanal      string `json:"kanal"`
	CabangID   int    `json:"cabang_id"`
	OccurredAt string `json:"occurred_at"`
}

var subjAkad = map[string]string{"setujui": "bank.akad.disetujui", "tolak": "bank.akad.ditolak",
	"cairkan": "bank.akad.dicairkan", "angsuran": "bank.akad.diangsur"}

// aksiAkad menjalankan setujui / tolak / cairkan / angsuran. Pencairan mengkredit rekening
// akad (transaksi jenis transfer_masuk), angsuran mendebit rekening (jenis angsuran).
func (s *svc) aksiAkad(ctx context.Context, id, aksi string, in akadAksiIn) (a akad, t *transaksi, err error) {
	if _, ok := subjAkad[aksi]; !ok {
		return a, nil, bad(404, "aksi tidak dikenal; pakai setujui, tolak, cairkan, angsuran")
	}
	occ, err := parseOccurred(in.OccurredAt, time.Now())
	if err != nil {
		return a, nil, err
	}
	if in.Kanal == "" {
		in.Kanal = "teller"
	}
	var cab *int
	if in.CabangID != 0 {
		cab = &in.CabangID
	}
	err = platform.InTx(ctx, s.db, func(tx pgx.Tx) error {
		cur, err := scanAkad(tx.QueryRow(ctx, `SELECT `+akadCols+` FROM akad_pembiayaan WHERE id::text=$1 FOR UPDATE`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return bad(404, "akad tidak ditemukan")
		}
		if err != nil {
			return err
		}
		next, err := nextAkad(cur.Status, aksi)
		if err != nil {
			return err
		}
		subj := subjAkad[aksi]
		terbayar := cur.Terbayar
		cairAt := cur.CairAt
		if aksi == "cairkan" || aksi == "angsuran" {
			if cur.RekeningID == nil {
				return bad(422, "akad tidak punya rekening untuk pencairan/angsuran")
			}
			k, err := lockRekening(ctx, tx, *cur.RekeningID)
			if err != nil {
				return err
			}
			p := postIn{Kanal: in.Kanal, CabangID: cab, OccurredAt: occ, AkadID: &cur.ID}
			if aksi == "cairkan" {
				p.Jenis, p.Nominal = "transfer_masuk", cur.Pokok
				p.Keterangan = fmt.Sprintf("pencairan akad %s", cur.KodeAkad)
				cairAt = &occ
			} else {
				n, lunas := angsuranAkad(cur.Pokok, cur.Margin, cur.TenorBulan, cur.Terbayar, in.Nominal)
				p.Jenis, p.Nominal = "angsuran", n
				p.Keterangan = fmt.Sprintf("angsuran akad %s", cur.KodeAkad)
				terbayar += n
				if lunas {
					next, subj = "lunas", "bank.akad.dilunasi"
				}
			}
			tr, err := s.post(ctx, tx, &k, p)
			if err != nil {
				return err
			}
			t = &tr
		}
		a, err = scanAkad(tx.QueryRow(ctx, `UPDATE akad_pembiayaan SET status=$2, terbayar=$3, cair_at=$4, updated_at=$5
			WHERE id=$1 RETURNING `+akadCols, cur.ID, next, terbayar, cairAt, occ))
		if err != nil {
			return err
		}
		// occurred_at event = waktu bisnis aksi (occurred_at body; kosong = sekarang), juga untuk
		// setujui/tolak, sehingga backfill siklus akad konsisten dengan transaksinya.
		_, err = platform.EnqueueEvent(ctx, tx, subj, occ, a)
		return err
	})
	return a, t, err
}
