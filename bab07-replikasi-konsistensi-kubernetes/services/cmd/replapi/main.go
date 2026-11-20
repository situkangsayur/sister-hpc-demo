// replapi: klien Go untuk PostgreSQL primary + replica (streaming replication).
// Memperlihatkan replication lag, anomali read-your-writes bila membaca dari replica,
// dan perbandingan consistency model (strong, read-your-writes, eventual) dari sisi klien.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/situkangsayur/sister-hpc-demo/bab07/services/internal/httpx"
	"github.com/situkangsayur/sister-hpc-demo/bab07/services/internal/logx"
)

type app struct {
	primary *pgxpool.Pool
	replica *pgxpool.Pool
	log     *slog.Logger

	mu      sync.Mutex
	history []LagPoint
}

// LagPoint adalah satu sampel replication lag untuk grafik di UI.
type LagPoint struct {
	At         int64   `json:"at_ms"`
	LagBytes   int64   `json:"lag_bytes"`
	ReplayLagS float64 `json:"replay_lag_s"`
	Up         bool    `json:"replica_up"`
}

// Status adalah ringkasan kedua server.
type Status struct {
	PrimaryUp    bool    `json:"primary_up"`
	ReplicaUp    bool    `json:"replica_up"`
	PrimaryLSN   string  `json:"primary_lsn"`
	ReceiveLSN   string  `json:"replica_receive_lsn"`
	ReplayLSN    string  `json:"replica_replay_lsn"`
	LagBytes     int64   `json:"lag_bytes"`
	ReplicaState string  `json:"replica_state"`
	SyncState    string  `json:"sync_state"`
	WriteLagS    float64 `json:"write_lag_s"`
	FlushLagS    float64 `json:"flush_lag_s"`
	ReplayLagS   float64 `json:"replay_lag_s"`
	SyncNames    string  `json:"synchronous_standby_names"`
	SyncCommit   string  `json:"synchronous_commit"`
	ApplyDelay   string  `json:"recovery_min_apply_delay"`
	InRecovery   bool    `json:"replica_in_recovery"`
	PricePrimary int     `json:"harga_primary"`
	PriceReplica int     `json:"harga_replica"`
	Mode         string  `json:"mode"`
	Error        string  `json:"error,omitempty"`
}

func (a *app) status(ctx context.Context) Status {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var s Status
	if err := a.primary.QueryRow(ctx, `select pg_current_wal_lsn()::text, current_setting('synchronous_standby_names'),
		current_setting('synchronous_commit'), (select harga from barang where id=1)`).
		Scan(&s.PrimaryLSN, &s.SyncNames, &s.SyncCommit, &s.PricePrimary); err != nil {
		s.Error = "primary: " + err.Error()
	} else {
		s.PrimaryUp = true
	}
	s.Mode = "async"
	if s.SyncNames != "" {
		s.Mode = "sync"
	}
	err := a.primary.QueryRow(ctx, `select state, sync_state,
		coalesce(extract(epoch from write_lag), 0)::float8, coalesce(extract(epoch from flush_lag), 0)::float8,
		coalesce(extract(epoch from replay_lag), 0)::float8
		from pg_stat_replication where application_name='replica1'`).
		Scan(&s.ReplicaState, &s.SyncState, &s.WriteLagS, &s.FlushLagS, &s.ReplayLagS)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && s.Error == "" {
		s.Error = "pg_stat_replication: " + err.Error()
	}
	var recv, replay *string
	if err := a.replica.QueryRow(ctx, `select pg_last_wal_receive_lsn()::text, pg_last_wal_replay_lsn()::text,
		pg_is_in_recovery(), current_setting('recovery_min_apply_delay'), (select harga from barang where id=1)`).
		Scan(&recv, &replay, &s.InRecovery, &s.ApplyDelay, &s.PriceReplica); err != nil {
		if s.Error == "" {
			s.Error = "replica: " + err.Error()
		}
	} else {
		s.ReplicaUp = true
		if recv != nil {
			s.ReceiveLSN = *recv
		}
		if replay != nil {
			s.ReplayLSN = *replay
		}
	}
	if s.PrimaryUp && s.ReplayLSN != "" {
		_ = a.primary.QueryRow(ctx, `select pg_wal_lsn_diff(pg_current_wal_lsn(), $1::pg_lsn)::bigint`, s.ReplayLSN).Scan(&s.LagBytes)
	}
	return s
}

func (a *app) sampler(ctx context.Context) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s := a.status(ctx)
		a.mu.Lock()
		a.history = append(a.history, LagPoint{At: time.Now().UnixMilli(), LagBytes: s.LagBytes, ReplayLagS: s.ReplayLagS, Up: s.ReplicaUp})
		if len(a.history) > 240 {
			a.history = a.history[len(a.history)-240:]
		}
		a.mu.Unlock()
	}
}

// write mengubah harga di primary dan mengembalikan LSN commit (posisi log tulisan terakhir).
func (a *app) write(ctx context.Context, harga int) (lsn string, ms float64, err error) {
	start := time.Now()
	if _, err = a.primary.Exec(ctx, `update barang set harga=$1, updated_at=now() where id=1`, harga); err != nil {
		return "", 0, err
	}
	ms = float64(time.Since(start).Microseconds()) / 1000
	err = a.primary.QueryRow(ctx, `select pg_current_wal_lsn()::text`).Scan(&lsn)
	return lsn, ms, err
}

// Read adalah hasil satu pembacaan dari sisi klien.
type Read struct {
	Who    string  `json:"who"`
	Model  string  `json:"model"`
	Source string  `json:"source"`
	Harga  int     `json:"harga"`
	Fresh  bool    `json:"fresh"`
	Ms     float64 `json:"ms"`
	Note   string  `json:"note,omitempty"`
}

func (a *app) readFrom(ctx context.Context, p *pgxpool.Pool) (int, float64, error) {
	start := time.Now()
	var h int
	err := p.QueryRow(ctx, `select harga from barang where id=1`).Scan(&h)
	return h, float64(time.Since(start).Microseconds()) / 1000, err
}

// replicaCaughtUp memeriksa apakah replica sudah menerapkan log sampai posisi lsn.
func (a *app) replicaCaughtUp(ctx context.Context, lsn string) bool {
	var ok bool
	err := a.replica.QueryRow(ctx, `select coalesce(pg_last_wal_replay_lsn() >= $1::pg_lsn, false)`, lsn).Scan(&ok)
	return err == nil && ok
}

// readModel membaca harga menurut consistency model tertentu.
func (a *app) readModel(ctx context.Context, who, model, lsn string, want int) Read {
	r := Read{Who: who, Model: model}
	var err error
	switch model {
	case "strong":
		r.Source = "primary"
		r.Harga, r.Ms, err = a.readFrom(ctx, a.primary)
	case "read-your-writes":
		// Klien mengingat LSN tulisannya. Replica hanya dipakai bila sudah melewati LSN itu.
		if who == "Ani" && !a.replicaCaughtUp(ctx, lsn) {
			r.Source = "primary"
			r.Note = "replica belum sampai LSN " + lsn + ", dialihkan ke primary"
			r.Harga, r.Ms, err = a.readFrom(ctx, a.primary)
		} else {
			r.Source = "replica"
			r.Harga, r.Ms, err = a.readFrom(ctx, a.replica)
		}
	default:
		r.Source = "replica"
		r.Harga, r.Ms, err = a.readFrom(ctx, a.replica)
	}
	if err != nil {
		r.Note = err.Error()
	}
	r.Fresh = err == nil && r.Harga == want
	return r
}

func (a *app) handleScenario(w http.ResponseWriter, req *http.Request) {
	var in struct {
		Harga int `json:"harga"`
	}
	_ = httpx.Decode(req, &in)
	ctx, cancel := context.WithTimeout(req.Context(), 20*time.Second)
	defer cancel()
	var old int
	_ = a.primary.QueryRow(ctx, `select harga from barang where id=1`).Scan(&old)
	if in.Harga == 0 || in.Harga == old {
		in.Harga = old + 1000
	}
	lsn, wms, err := a.write(ctx, in.Harga)
	if err != nil {
		httpx.Err(w, 503, "tulis ke primary gagal: "+err.Error())
		return
	}
	// Ani (penjual) dan Budi (pembeli) membaca sesaat setelah tulisan, di tiga model.
	var reads []Read
	for _, m := range []string{"strong", "read-your-writes", "eventual"} {
		for _, who := range []string{"Ani", "Budi"} {
			reads = append(reads, a.readModel(ctx, who, m, lsn, in.Harga))
		}
	}
	// Konvergensi: baca replica berulang sampai nilainya sama dengan primary.
	type pt struct {
		T     int64 `json:"t_ms"`
		Harga int   `json:"harga"`
	}
	var conv []pt
	start := time.Now()
	converged := int64(-1)
	for time.Since(start) < 12*time.Second {
		h, _, err := a.readFrom(ctx, a.replica)
		if err == nil {
			conv = append(conv, pt{T: time.Since(start).Milliseconds(), Harga: h})
			if h == in.Harga {
				converged = time.Since(start).Milliseconds()
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	a.log.Info("skenario ani-budi", "harga_lama", old, "harga_baru", in.Harga, "lsn", lsn, "write_ms", wms, "converged_ms", converged)
	httpx.JSON(w, 200, map[string]any{"harga_lama": old, "harga_baru": in.Harga, "write_lsn": lsn, "write_ms": wms,
		"reads": reads, "convergence": conv, "converged_ms": converged, "status": a.status(ctx)})
}

func (a *app) handleMode(w http.ResponseWriter, req *http.Request) {
	var in struct {
		Mode string `json:"mode"`
	}
	_ = httpx.Decode(req, &in)
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()
	names, commit := "", "on"
	if in.Mode == "sync" {
		// remote_apply: commit menunggu replica selesai MENERAPKAN tulisan, bukan sekadar menerima.
		names, commit = "replica1", "remote_apply"
	}
	for _, q := range []string{
		fmt.Sprintf(`alter system set synchronous_standby_names = '%s'`, names),
		fmt.Sprintf(`alter system set synchronous_commit = '%s'`, commit),
		`select pg_reload_conf()`,
	} {
		if _, err := a.primary.Exec(ctx, q); err != nil {
			httpx.Err(w, 500, err.Error())
			return
		}
	}
	time.Sleep(300 * time.Millisecond)
	a.log.Info("mode replikasi", "mode", in.Mode)
	httpx.JSON(w, 200, a.status(ctx))
}

func (a *app) handleDelay(w http.ResponseWriter, req *http.Request) {
	var in struct {
		Ms int `json:"ms"`
	}
	_ = httpx.Decode(req, &in)
	if in.Ms < 0 || in.Ms > 10000 {
		httpx.Err(w, 400, "ms harus 0..10000")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()
	// recovery_min_apply_delay membuat replica menunda penerapan WAL: lag buatan yang terukur.
	for _, q := range []string{fmt.Sprintf(`alter system set recovery_min_apply_delay = '%dms'`, in.Ms), `select pg_reload_conf()`} {
		if _, err := a.replica.Exec(ctx, q); err != nil {
			httpx.Err(w, 500, err.Error())
			return
		}
	}
	time.Sleep(300 * time.Millisecond)
	a.log.Info("apply delay", "ms", in.Ms)
	httpx.JSON(w, 200, a.status(ctx))
}

func (a *app) handleWrite(w http.ResponseWriter, req *http.Request) {
	var in struct {
		Harga int `json:"harga"`
	}
	if err := httpx.Decode(req, &in); err != nil || in.Harga <= 0 {
		httpx.Err(w, 400, "body harus {harga: >0}")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 15*time.Second)
	defer cancel()
	lsn, ms, err := a.write(ctx, in.Harga)
	if err != nil {
		httpx.Err(w, 503, err.Error())
		return
	}
	a.log.Info("tulis", "harga", in.Harga, "lsn", lsn, "ms", ms)
	httpx.JSON(w, 200, map[string]any{"lsn": lsn, "write_ms": ms})
}

func connect(ctx context.Context, url string) *pgxpool.Pool {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		panic(err)
	}
	cfg.MaxConns = 4
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		panic(err)
	}
	return p
}

func main() {
	log := logx.New("repl-api")
	ctx := context.Background()
	a := &app{
		primary: connect(ctx, httpx.Env("PRIMARY_URL", "postgres://postgres:changeme@pg-primary:5432/katalog")),
		replica: connect(ctx, httpx.Env("REPLICA_URL", "postgres://postgres:changeme@pg-replica:5432/katalog")),
		log:     log,
	}
	// Siapkan tabel di primary; replica menerimanya lewat replication.
	for i := 0; ; i++ {
		_, err := a.primary.Exec(ctx, `create table if not exists barang(id int primary key, nama text not null,
			harga int not null, updated_at timestamptz not null default now());
			insert into barang(id, nama, harga) values (1, 'Kopi Gayo 250 g', 10000) on conflict (id) do nothing`)
		if err == nil {
			break
		}
		if i%10 == 0 {
			log.Warn("menunggu primary", "err", err.Error())
		}
		time.Sleep(time.Second)
	}
	go a.sampler(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		s := a.status(r.Context())
		a.mu.Lock()
		h := append([]LagPoint(nil), a.history...)
		a.mu.Unlock()
		httpx.JSON(w, 200, map[string]any{"status": s, "history": h})
	})
	mux.HandleFunc("POST /write", a.handleWrite)
	mux.HandleFunc("POST /scenario", a.handleScenario)
	mux.HandleFunc("POST /mode", a.handleMode)
	mux.HandleFunc("POST /delay", a.handleDelay)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	addr := ":" + httpx.Env("PORT", "8080")
	log.Info("mulai", "addr", addr)
	if err := http.ListenAndServe(addr, httpx.Log(log, mux)); err != nil {
		log.Error("berhenti", "err", err.Error())
	}
}
