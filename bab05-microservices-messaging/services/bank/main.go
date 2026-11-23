// Service bank: bank syariah kecil yang berbagi platform dengan toko daring (database "bank").
// Fitur: cabang, nasabah, rekening, transaksi (setor, tarik, transfer), akad pembiayaan, dan
// pembayaran belanja toko dari rekening (dipanggil service payment lewat port internal 8081).
// Setiap perubahan ditulis bersama event ke outbox lalu diterbitkan ke JetStream stream BANK
// (subject bank.<entitas>.<aksi>). Aplikasi ini juga menjadi sumber data mata kuliah Data
// Engineering; bentuk tabel dan event mengikuti de-demo/platform/KONTRAK-DATA.md.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"sister-bab05/services/platform"
)

type svc struct {
	db  *pgxpool.Pool
	nc  *nats.Conn
	js  jetstream.JetStream
	log *slog.Logger
}

func main() {
	log := platform.NewLogger("bank")
	ctx := context.Background()
	db, err := platform.ConnectDB(ctx, platform.Env("DATABASE_URL", "postgres://sister:changeme@postgres:5432/bank"))
	if err != nil {
		log.Error("db", "err", err)
		return
	}
	if err := platform.Migrate(ctx, db, schema...); err != nil {
		log.Error("migrate", "err", err)
		return
	}
	nc, js, err := platform.ConnectNATS(platform.Env("NATS_URL", "nats://nats:4222"), "bank")
	if err != nil {
		log.Error("nats", "err", err)
		return
	}
	// Retensi 7 hari / 512 MB: pipeline DE boleh tertinggal beberapa hari tanpa kehilangan event.
	if err := platform.EnsureStreamRetention(ctx, js, "BANK", []string{"bank.>"}, 7*24*time.Hour, 512<<20); err != nil {
		log.Error("stream", "err", err)
		return
	}
	s := &svc{db: db, nc: nc, js: js, log: log}
	if err := s.seed(ctx); err != nil {
		log.Error("seed", "err", err)
		return
	}
	go platform.RunRelay(ctx, db, js, log, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		platform.WriteJSON(w, 200, map[string]any{"service": "bank", "db": db.Ping(r.Context()) == nil, "nats": nc.IsConnected()})
	})
	mux.HandleFunc("GET /cabang", s.listCabang)
	mux.HandleFunc("GET /nasabah", s.listNasabah)
	mux.HandleFunc("POST /nasabah", s.createNasabah)
	mux.HandleFunc("GET /nasabah/{id}", s.getNasabah)
	mux.HandleFunc("PATCH /nasabah/{id}", s.patchNasabah)
	mux.HandleFunc("GET /rekening", s.listRekening)
	mux.HandleFunc("POST /rekening", s.createRekening)
	mux.HandleFunc("GET /rekening/{ref}", s.getRekening)
	mux.HandleFunc("GET /rekening/{ref}/mutasi", s.mutasi)
	mux.HandleFunc("GET /transaksi", s.mutasi)
	mux.HandleFunc("POST /transaksi", s.createTransaksi)
	mux.HandleFunc("POST /transfer", s.transfer)
	mux.HandleFunc("GET /akad", s.listAkad)
	mux.HandleFunc("POST /akad", s.createAkad)
	mux.HandleFunc("POST /akad/{id}/{aksi}", s.akadAksi)
	mux.HandleFunc("GET /ringkasan", s.ringkasan)
	platform.Serve(":8080", platform.Middleware(log, mux))

	// Port internal 8081 tidak dirutekan APISIX: hanya service lain di network compose yang
	// boleh mendebit rekening untuk belanja (payment).
	internal := http.NewServeMux()
	internal.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		platform.WriteJSON(w, 200, map[string]any{"service": "bank-internal"})
	})
	internal.HandleFunc("POST /internal/bayar-belanja", s.bayarBelanja)
	log.Info("bank siap", "addr", ":8080", "internal", ":8081")
	platform.Serve(":8081", platform.Middleware(log, internal))
	select {}
}

// fail menerjemahkan error ke respons HTTP.
func (s *svc) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		platform.WriteJSON(w, ae.Code, map[string]any{"error": ae.Msg})
		return
	}
	s.log.Error("bank error", "request_id", platform.RequestID(r.Context()), "path", r.URL.Path, "err", err)
	platform.WriteJSON(w, 500, map[string]any{"error": err.Error()})
}

func limitOf(r *http.Request, def int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return def
	}
	return min(n, 500)
}

func (s *svc) audit(r *http.Request, action, note string, extra map[string]any) {
	f := map[string]any{"action": action, "note": note, "request_id": platform.RequestID(r.Context())}
	for k, v := range extra {
		f[k] = v
	}
	platform.Audit(s.nc, "bank", f)
}

// ---------- cabang & nasabah ----------

func (s *svc) listCabang(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT `+cabangCols+` FROM cabang ORDER BY id`)
	out, err := collect(rows, err, scanCabang)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	platform.WriteJSON(w, 200, out)
}

func (s *svc) listNasabah(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT `+nasabahCols+` FROM nasabah ORDER BY created_at DESC LIMIT $1`, limitOf(r, 50))
	out, err := collect(rows, err, scanNasabah)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	platform.WriteJSON(w, 200, out)
}

func (s *svc) createNasabah(w http.ResponseWriter, r *http.Request) {
	var in nasabahIn
	if err := platform.ReadJSON(r, &in); err != nil {
		s.fail(w, r, bad(400, "body JSON tidak valid"))
		return
	}
	var n nasabah
	err := platform.InTx(r.Context(), s.db, func(tx pgx.Tx) (err error) {
		n, err = s.insertNasabah(r.Context(), tx, in)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("nasabah dibuat", "request_id", platform.RequestID(r.Context()), "cif", n.CIF)
	platform.WriteJSON(w, 201, n)
}

func (s *svc) getNasabah(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	n, err := scanNasabah(s.db.QueryRow(ctx, `SELECT `+nasabahCols+` FROM nasabah WHERE id::text=$1 OR cif=$1`, r.PathValue("id")))
	if errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, r, bad(404, "nasabah tidak ditemukan"))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err := s.db.Query(ctx, `SELECT `+rekeningCols+` FROM rekening WHERE nasabah_id=$1 ORDER BY dibuka_at`, n.ID)
	reks, _ := collect(rows, err, scanRekening)
	rows, err = s.db.Query(ctx, `SELECT `+akadCols+` FROM akad_pembiayaan WHERE nasabah_id=$1 ORDER BY diajukan_at DESC`, n.ID)
	aks, _ := collect(rows, err, scanAkad)
	platform.WriteJSON(w, 200, map[string]any{"nasabah": n, "rekening": reks, "akad": aks})
}

func (s *svc) patchNasabah(w http.ResponseWriter, r *http.Request) {
	var p nasabahPatch
	if err := platform.ReadJSON(r, &p); err != nil {
		s.fail(w, r, bad(400, "body JSON tidak valid"))
		return
	}
	n, err := s.updateNasabah(r.Context(), r.PathValue("id"), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	platform.WriteJSON(w, 200, n)
}

// ---------- rekening ----------

// rekeningView menambah nama dan CIF pemilik untuk tampilan (bukan bagian event).
type rekeningView struct {
	rekening
	Nama string `json:"nama"`
	CIF  string `json:"cif"`
}

func (s *svc) listRekening(w http.ResponseWriter, r *http.Request) {
	q := `SELECT r.id::text, r.no_rekening, r.nasabah_id::text, r.jenis, r.saldo, r.status, r.cabang_id, r.dibuka_at, r.updated_at, n.nama, n.cif
		FROM rekening r JOIN nasabah n ON n.id = r.nasabah_id WHERE ($1 = '' OR n.id::text = $1 OR n.cif = $1)
		ORDER BY r.dibuka_at DESC LIMIT $2`
	rows, err := s.db.Query(r.Context(), q, r.URL.Query().Get("nasabah_id"), limitOf(r, 100))
	out, err := collect(rows, err, func(row pgx.Row) (v rekeningView, err error) {
		k := &v.rekening
		err = row.Scan(&k.ID, &k.NoRekening, &k.NasabahID, &k.Jenis, &k.Saldo, &k.Status, &k.CabangID, &k.DibukaAt, &k.UpdatedAt, &v.Nama, &v.CIF)
		return
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	platform.WriteJSON(w, 200, out)
}

func (s *svc) createRekening(w http.ResponseWriter, r *http.Request) {
	var in rekeningIn
	if err := platform.ReadJSON(r, &in); err != nil {
		s.fail(w, r, bad(400, "body JSON tidak valid"))
		return
	}
	k, err := s.openRekening(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("rekening dibuka", "request_id", platform.RequestID(r.Context()), "no_rekening", k.NoRekening, "jenis", k.Jenis)
	platform.WriteJSON(w, 201, k)
}

func (s *svc) getRekening(w http.ResponseWriter, r *http.Request) {
	k, err := scanRekening(s.db.QueryRow(r.Context(), `SELECT `+rekeningCols+` FROM rekening WHERE id::text=$1 OR no_rekening=$1`, r.PathValue("ref")))
	if errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, r, bad(404, "rekening tidak ditemukan"))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	platform.WriteJSON(w, 200, k)
}

// transaksiView menambah nomor rekening dan nama nasabah untuk tampilan mutasi.
type transaksiView struct {
	transaksi
	NoRekening string `json:"no_rekening"`
	Nama       string `json:"nama"`
}

// mutasi: GET /transaksi (semua rekening, untuk mutasi live) atau GET /rekening/{ref}/mutasi.
// ?since=<RFC3339 created_at> hanya mengembalikan transaksi yang lebih baru.
func (s *svc) mutasi(w http.ResponseWriter, r *http.Request) {
	since := r.URL.Query().Get("since")
	if since == "" {
		since = "1970-01-01T00:00:00Z"
	}
	q := `SELECT t.id::text, t.rekening_id::text, t.cabang_id, t.kanal, t.jenis, t.nominal, t.saldo_setelah, t.occurred_at, t.created_at,
		  t.ref_order_id, t.transfer_id::text, t.akad_id::text, t.idempotency_key, t.keterangan, t.request_id, r.no_rekening, n.nama
		FROM transaksi t JOIN rekening r ON r.id = t.rekening_id JOIN nasabah n ON n.id = r.nasabah_id
		WHERE ($1 = '' OR r.id::text = $1 OR r.no_rekening = $1) AND t.created_at > $2::timestamptz
		ORDER BY t.created_at DESC LIMIT $3`
	rows, err := s.db.Query(r.Context(), q, r.PathValue("ref"), since, limitOf(r, 30))
	out, err := collect(rows, err, func(row pgx.Row) (v transaksiView, err error) {
		t := &v.transaksi
		err = row.Scan(&t.ID, &t.RekeningID, &t.CabangID, &t.Kanal, &t.Jenis, &t.Nominal, &t.SaldoSetelah, &t.OccurredAt,
			&t.CreatedAt, &t.RefOrderID, &t.TransferID, &t.AkadID, &t.IdempotencyKey, &t.Keterangan, &t.RequestID, &v.NoRekening, &v.Nama)
		return
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	platform.WriteJSON(w, 200, out)
}

// ---------- transaksi ----------

type transaksiIn struct {
	Jenis      string `json:"jenis"` // setor | tarik
	RekeningID string `json:"rekening_id"`
	NoRekening string `json:"no_rekening"`
	Nominal    int64  `json:"nominal"`
	Kanal      string `json:"kanal"`
	CabangID   int    `json:"cabang_id"`
	OccurredAt string `json:"occurred_at"`
	Keterangan string `json:"keterangan"`
}

func (in transaksiIn) ref() string {
	if in.RekeningID != "" {
		return in.RekeningID
	}
	return in.NoRekening
}

func cabangPtr(id int) *int {
	if id == 0 {
		return nil
	}
	return &id
}

func (s *svc) createTransaksi(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in transaksiIn
	if err := platform.ReadJSON(r, &in); err != nil {
		s.fail(w, r, bad(400, "body JSON tidak valid"))
		return
	}
	if in.Jenis != "setor" && in.Jenis != "tarik" {
		s.fail(w, r, bad(400, "jenis harus setor atau tarik (transfer lewat POST /transfer)"))
		return
	}
	occ, err := parseOccurred(in.OccurredAt, time.Now())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	out, replayed, err := s.withIdem(ctx, key, func(tx pgx.Tx) ([]transaksi, error) {
		k, err := lockRekening(ctx, tx, in.ref())
		if err != nil {
			return nil, err
		}
		t, err := s.post(ctx, tx, &k, postIn{Jenis: in.Jenis, Nominal: in.Nominal, Kanal: in.Kanal, CabangID: cabangPtr(in.CabangID),
			OccurredAt: occ, IdempotencyKey: nullable(key), Keterangan: in.Keterangan})
		return []transaksi{t}, err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	code := 201
	if replayed {
		code = 200
	}
	t := out[0]
	s.log.Info("transaksi", "request_id", platform.RequestID(ctx), "jenis", t.Jenis, "nominal", t.Nominal, "kanal", t.Kanal, "replayed", replayed)
	platform.WriteJSON(w, code, map[string]any{"transaksi": t, "replayed": replayed})
}

type transferIn struct {
	Dari       string `json:"dari"` // id atau no_rekening
	Ke         string `json:"ke"`
	Nominal    int64  `json:"nominal"`
	Kanal      string `json:"kanal"`
	CabangID   int    `json:"cabang_id"`
	OccurredAt string `json:"occurred_at"`
	Keterangan string `json:"keterangan"`
}

// transfer: dua baris transaksi (transfer_keluar, transfer_masuk) dengan transfer_id sama,
// dalam SATU transaksi database. Rekening dikunci berurutan id agar tidak deadlock.
func (s *svc) transfer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in transferIn
	if err := platform.ReadJSON(r, &in); err != nil || in.Dari == "" || in.Ke == "" {
		s.fail(w, r, bad(400, "butuh dari, ke, nominal"))
		return
	}
	occ, err := parseOccurred(in.OccurredAt, time.Now())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	out, replayed, err := s.withIdem(ctx, key, func(tx pgx.Tx) ([]transaksi, error) {
		var ids []string
		rows, err := tx.Query(ctx, `SELECT id::text FROM rekening WHERE id::text = ANY($1) OR no_rekening = ANY($1) ORDER BY id`,
			[]string{in.Dari, in.Ke})
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		if len(ids) != 2 {
			return nil, bad(400, "rekening asal dan tujuan harus ada dan berbeda")
		}
		locked := map[string]rekening{}
		for _, id := range ids {
			k, err := lockRekening(ctx, tx, id)
			if err != nil {
				return nil, err
			}
			locked[k.ID], locked[k.NoRekening] = k, k
		}
		src, dst := locked[in.Dari], locked[in.Ke]
		tid := platform.NewID()
		ket := in.Keterangan
		keluar, err := s.post(ctx, tx, &src, postIn{Jenis: "transfer_keluar", Nominal: in.Nominal, Kanal: in.Kanal,
			CabangID: cabangPtr(in.CabangID), OccurredAt: occ, TransferID: &tid, IdempotencyKey: nullable(key),
			Keterangan: "ke " + dst.NoRekening + " " + ket})
		if err != nil {
			return nil, err
		}
		var k2 *string
		if key != "" {
			k := key + ":masuk"
			k2 = &k
		}
		masuk, err := s.post(ctx, tx, &dst, postIn{Jenis: "transfer_masuk", Nominal: in.Nominal, Kanal: in.Kanal,
			CabangID: cabangPtr(in.CabangID), OccurredAt: occ, TransferID: &tid, IdempotencyKey: k2,
			Keterangan: "dari " + src.NoRekening + " " + ket})
		return []transaksi{keluar, masuk}, err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	code := 201
	if replayed {
		code = 200
	}
	s.log.Info("transfer", "request_id", platform.RequestID(ctx), "nominal", in.Nominal, "replayed", replayed)
	platform.WriteJSON(w, code, map[string]any{"transaksi": out, "replayed": replayed})
}

// bayarBelanja (internal, dipanggil payment): debit rekening untuk satu order toko.
// Idempotent per order_id: panggilan ulang (retry/redelivery) mengembalikan transaksi lama.
func (s *svc) bayarBelanja(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		NoRekening string `json:"no_rekening"`
		OrderID    string `json:"order_id"`
		Nominal    int64  `json:"nominal"`
	}
	if err := platform.ReadJSON(r, &in); err != nil || in.OrderID == "" || in.NoRekening == "" {
		s.fail(w, r, bad(400, "butuh no_rekening, order_id, nominal"))
		return
	}
	find := func() (transaksi, error) {
		return scanTransaksi(s.db.QueryRow(ctx, `SELECT `+transaksiCols+` FROM transaksi WHERE ref_order_id=$1 AND jenis='bayar_belanja'`, in.OrderID))
	}
	if t, err := find(); err == nil {
		platform.WriteJSON(w, 200, map[string]any{"transaksi": t, "replayed": true})
		return
	}
	var t transaksi
	err := platform.InTx(ctx, s.db, func(tx pgx.Tx) error {
		k, err := lockRekening(ctx, tx, in.NoRekening)
		if err != nil {
			return err
		}
		t, err = s.post(ctx, tx, &k, postIn{Jenis: "bayar_belanja", Nominal: in.Nominal, Kanal: "ecommerce", OccurredAt: time.Now(),
			RefOrderID: &in.OrderID, Keterangan: "belanja toko " + in.OrderID})
		return err
	})
	if err != nil && isUnique(err) {
		if prev, e := find(); e == nil {
			platform.WriteJSON(w, 200, map[string]any{"transaksi": prev, "replayed": true})
			return
		}
	}
	if err != nil {
		s.log.Warn("bayar belanja ditolak", "request_id", platform.RequestID(ctx), "order_id", in.OrderID, "err", err)
		s.audit(r, "bayar_belanja", "ditolak: "+err.Error(), map[string]any{"order_id": in.OrderID})
		s.fail(w, r, err)
		return
	}
	s.log.Info("bayar belanja", "request_id", platform.RequestID(ctx), "order_id", in.OrderID, "nominal", in.Nominal)
	s.audit(r, "bayar_belanja", "rekening didebit "+strconv.FormatInt(in.Nominal, 10), map[string]any{"order_id": in.OrderID})
	platform.WriteJSON(w, 201, map[string]any{"transaksi": t, "replayed": false})
}

// ---------- akad ----------

func (s *svc) listAkad(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT `+akadCols+` FROM akad_pembiayaan
		WHERE ($1 = '' OR nasabah_id::text = $1) ORDER BY diajukan_at DESC LIMIT $2`, r.URL.Query().Get("nasabah_id"), limitOf(r, 50))
	out, err := collect(rows, err, scanAkad)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	platform.WriteJSON(w, 200, out)
}

func (s *svc) createAkad(w http.ResponseWriter, r *http.Request) {
	var in akadIn
	if err := platform.ReadJSON(r, &in); err != nil {
		s.fail(w, r, bad(400, "body JSON tidak valid"))
		return
	}
	a, err := s.ajukanAkad(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	_, valid := KodeAkadValid[a.KodeAkad]
	s.log.Info("akad diajukan", "request_id", platform.RequestID(r.Context()), "kode_akad", a.KodeAkad, "kode_valid", valid)
	platform.WriteJSON(w, 201, map[string]any{"akad": a, "kode_valid": valid})
}

func (s *svc) akadAksi(w http.ResponseWriter, r *http.Request) {
	var in akadAksiIn
	_ = platform.ReadJSON(r, &in) // body boleh kosong
	a, t, err := s.aksiAkad(r.Context(), r.PathValue("id"), r.PathValue("aksi"), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("akad", "request_id", platform.RequestID(r.Context()), "aksi", r.PathValue("aksi"), "status", a.Status)
	platform.WriteJSON(w, 200, map[string]any{"akad": a, "transaksi": t})
}

// ringkasan: angka untuk kartu di UI dan isi stream BANK.
func (s *svc) ringkasan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := map[string]any{}
	var nas, rek, trx, pending int64
	var saldo int64
	_ = s.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM nasabah), (SELECT count(*) FROM rekening),
		(SELECT coalesce(sum(saldo),0) FROM rekening), (SELECT count(*) FROM transaksi),
		(SELECT count(*) FROM outbox WHERE sent_at IS NULL)`).Scan(&nas, &rek, &saldo, &trx, &pending)
	out["nasabah"], out["rekening"], out["total_saldo"], out["transaksi"], out["outbox_pending"] = nas, rek, saldo, trx, pending
	akadBy := map[string]int64{}
	if rows, err := s.db.Query(ctx, `SELECT status, count(*) FROM akad_pembiayaan GROUP BY status`); err == nil {
		for rows.Next() {
			var st string
			var n int64
			if rows.Scan(&st, &n) == nil {
				akadBy[st] = n
			}
		}
		rows.Close()
	}
	out["akad"] = akadBy
	if st, err := s.js.Stream(ctx, "BANK"); err == nil {
		if info, err := st.Info(ctx); err == nil {
			out["stream"] = map[string]any{"name": "BANK", "messages": info.State.Msgs, "bytes": info.State.Bytes,
				"first_seq": info.State.FirstSeq, "last_seq": info.State.LastSeq}
		}
	}
	out["kode_akad_valid"] = KodeAkadValid
	platform.WriteJSON(w, 200, out)
}
