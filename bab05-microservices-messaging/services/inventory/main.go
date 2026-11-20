// Service inventory: pemilik stok (database "inventory"). Dalam saga choreography ia
// bereaksi pada shop.order.created (T2: pesan stok), shop.payment.failed (C2: lepas stok),
// dan shop.order.confirmed (stok terjual). Consumer at-least-once + idempotent (dedup).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"sister-bab05/services/platform"
)

const initialStock = 10

// faults adalah kegagalan yang bisa disuntik dari UI.
type faults struct {
	DelayMs        int  `json:"delay_ms"`         // inventory lambat (HTTP dan event)
	CrashBeforeAck bool `json:"crash_before_ack"` // commit lalu "mati" sebelum ack
	Idempotent     bool `json:"idempotent"`       // false = dedup dimatikan (lihat efek ganda)
}

type app struct {
	db  *pgxpool.Pool
	nc  *nats.Conn
	log *slog.Logger
	mu  sync.Mutex
	f   faults
}

func main() {
	log := platform.NewLogger("inventory")
	ctx := context.Background()
	db, err := platform.ConnectDB(ctx, platform.Env("DATABASE_URL", "postgres://sister:changeme@postgres:5432/inventory"))
	if err != nil {
		log.Error("db", "err", err)
		return
	}
	if err := platform.Migrate(ctx, db,
		`CREATE TABLE IF NOT EXISTS stock (product_id TEXT PRIMARY KEY, available INT NOT NULL, reserved INT NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS reservations (id BIGSERIAL PRIMARY KEY, order_id TEXT NOT NULL,
		  product_id TEXT NOT NULL, qty INT NOT NULL, status TEXT NOT NULL, at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE INDEX IF NOT EXISTS reservations_order ON reservations(order_id)`); err != nil {
		log.Error("migrate", "err", err)
		return
	}
	for _, p := range platform.SeedProducts {
		_, _ = db.Exec(ctx, `INSERT INTO stock (product_id, available) VALUES ($1,$2) ON CONFLICT DO NOTHING`, p.ID, initialStock)
	}
	nc, js, err := platform.ConnectNATS(platform.Env("NATS_URL", "nats://nats:4222"), "inventory")
	if err != nil {
		log.Error("nats", "err", err)
		return
	}
	if err := platform.EnsureStream(ctx, js, "SHOP", []string{"shop.>"}); err != nil {
		log.Error("stream", "err", err)
		return
	}
	a := &app{db: db, nc: nc, log: log, f: faults{Idempotent: true}}
	go platform.RunRelay(ctx, db, js, log, nil)
	err = platform.Consume(ctx, js, platform.ConsumerSpec{Stream: "SHOP", Durable: "inventory", Batch: 1,
		Filters: []string{platform.SubjOrderCreated, platform.SubjPaymentFailed, platform.SubjOrderConfirmed}},
		log, nil, a.handle)
	if err != nil {
		log.Error("consumer", "err", err)
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		platform.WriteJSON(w, 200, map[string]any{"service": "inventory", "db": db.Ping(r.Context()) == nil, "nats": nc.IsConnected()})
	})
	mux.HandleFunc("GET /stock", a.handleStock)
	mux.HandleFunc("GET /admin/fault", func(w http.ResponseWriter, r *http.Request) { platform.WriteJSON(w, 200, a.faults()) })
	mux.HandleFunc("POST /admin/fault", a.handleFault)
	mux.HandleFunc("POST /admin/reset-stock", func(w http.ResponseWriter, r *http.Request) {
		_, err := db.Exec(r.Context(), `UPDATE stock SET available=$1, reserved=0`, initialStock)
		platform.WriteJSON(w, 200, map[string]any{"ok": err == nil})
	})
	log.Info("inventory siap", "addr", ":8080")
	platform.Serve(":8080", platform.Middleware(log, mux))
	select {}
}

func (a *app) faults() faults {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.f
}

func (a *app) handleFault(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DelayMs        *int  `json:"delay_ms"`
		CrashBeforeAck *bool `json:"crash_before_ack"`
		Idempotent     *bool `json:"idempotent"`
	}
	if err := platform.ReadJSON(r, &in); err != nil {
		platform.WriteJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	a.mu.Lock()
	if in.DelayMs != nil {
		a.f.DelayMs = max(0, min(*in.DelayMs, 3000))
	}
	if in.CrashBeforeAck != nil {
		a.f.CrashBeforeAck = *in.CrashBeforeAck
	}
	if in.Idempotent != nil {
		a.f.Idempotent = *in.Idempotent
	}
	f := a.f
	a.mu.Unlock()
	a.log.Warn("fault inventory diubah", "request_id", platform.RequestID(r.Context()), "delay_ms", f.DelayMs,
		"crash_before_ack", f.CrashBeforeAck, "idempotent", f.Idempotent)
	platform.WriteJSON(w, 200, f)
}

func (a *app) sleepFault(ctx context.Context) {
	if d := a.faults().DelayMs; d > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(time.Duration(d) * time.Millisecond):
		}
	}
}

type stockRow struct {
	ProductID string `json:"product_id"`
	Available int    `json:"available"`
	Reserved  int    `json:"reserved"`
}

func (a *app) handleStock(w http.ResponseWriter, r *http.Request) {
	// "Inventory lambat" juga memperlambat panggilan synchronous dari order (precheck=1).
	// Tampilan stok di UI tidak ikut diperlambat agar dasbor tetap responsif.
	if r.URL.Query().Get("precheck") == "1" {
		a.sleepFault(r.Context())
		if r.Context().Err() != nil {
			return // pemanggil sudah timeout
		}
	}
	ids := []string{} // slice kosong (bukan nil) agar cardinality() = 0, bukan NULL
	if s := r.URL.Query().Get("ids"); s != "" {
		ids = strings.Split(s, ",")
	}
	rows, err := a.db.Query(r.Context(), `SELECT product_id, available, reserved FROM stock
		WHERE cardinality($1::text[]) = 0 OR product_id = ANY($1) ORDER BY product_id`, ids)
	if err != nil {
		platform.WriteJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	defer rows.Close()
	out := []stockRow{}
	for rows.Next() {
		var s stockRow
		if rows.Scan(&s.ProductID, &s.Available, &s.Reserved) == nil {
			out = append(out, s)
		}
	}
	platform.WriteJSON(w, 200, out)
}

var errNotEnough = errors.New("stok tidak cukup")

// handle memproses satu event. Urutannya: proses + catat (satu transaksi), lalu ack.
func (a *app) handle(ctx context.Context, d *platform.Delivery) error {
	var ev platform.OrderEvent
	if err := json.Unmarshal(d.Data, &ev); err != nil {
		return nil // pesan rusak: ack saja (di produksi: kirim ke dead letter)
	}
	a.sleepFault(ctx)
	f := a.faults()
	log := a.log.With("request_id", d.RequestID, "order_id", ev.OrderID, "subject", d.Subject, "msg_id", d.MsgID,
		"delivered", d.NumDelivered)
	var result string
	dup, err := platform.ProcessOnce(ctx, a.db, "inventory", d.MsgID, f.Idempotent, func(tx pgx.Tx) error {
		var err error
		switch d.Subject {
		case platform.SubjOrderCreated:
			result, err = a.reserve(ctx, tx, ev)
		case platform.SubjPaymentFailed:
			result, err = a.release(ctx, tx, ev)
		case platform.SubjOrderConfirmed:
			result, err = a.commitSold(ctx, tx, ev)
		}
		return err
	})
	audit := map[string]any{"action": "consumed", "subject": d.Subject, "msg_id": d.MsgID, "order_id": ev.OrderID,
		"request_id": d.RequestID, "delivered": d.NumDelivered, "idempotent": f.Idempotent, "note": result}
	switch {
	case err != nil:
		audit["action"], audit["note"] = "error", err.Error()
		platform.Audit(a.nc, "inventory", audit)
		return err
	case dup:
		audit["action"], audit["note"] = "duplicate", "msg_id sudah ada di processed_messages, efek tidak diulang"
		log.Warn("duplikat terdeteksi, di-ack tanpa efek")
		platform.Audit(a.nc, "inventory", audit)
		return nil
	}
	if d.NumDelivered > 1 {
		audit["action"] = "redelivered"
	}
	log.Info("event diproses", "note", result)
	platform.Audit(a.nc, "inventory", audit)
	if f.CrashBeforeAck && d.NumDelivered == 1 && d.Subject == platform.SubjOrderCreated {
		platform.Audit(a.nc, "inventory", map[string]any{"action": "crash", "subject": d.Subject, "msg_id": d.MsgID,
			"order_id": ev.OrderID, "request_id": d.RequestID, "delivered": d.NumDelivered,
			"note": "transaksi sudah commit, ack TIDAK dikirim; JetStream mengirim ulang setelah AckWait 5 s"})
		return platform.ErrSkipAck
	}
	return nil
}

// reserve adalah T2: pesan stok untuk semua item, atau tolak seluruhnya.
func (a *app) reserve(ctx context.Context, tx pgx.Tx, ev platform.OrderEvent) (string, error) {
	for _, it := range ev.Items {
		var avail int
		err := tx.QueryRow(ctx, `SELECT available FROM stock WHERE product_id=$1 FOR UPDATE`, it.ProductID).Scan(&avail)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		if avail < it.Qty {
			ev.Reason = fmt.Sprintf("%s: %s tersisa %d, diminta %d", errNotEnough, it.ProductID, avail, it.Qty)
			_, err := platform.Enqueue(ctx, tx, platform.SubjInventoryRejected, ev)
			return "stok ditolak: " + ev.Reason, err
		}
	}
	for _, it := range ev.Items {
		if _, err := tx.Exec(ctx, `UPDATE stock SET available=available-$2, reserved=reserved+$2 WHERE product_id=$1`, it.ProductID, it.Qty); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO reservations (order_id, product_id, qty, status) VALUES ($1,$2,$3,'RESERVED')`,
			ev.OrderID, it.ProductID, it.Qty); err != nil {
			return "", err
		}
	}
	_, err := platform.Enqueue(ctx, tx, platform.SubjInventoryReserved, ev)
	return fmt.Sprintf("stok dipesan untuk %d item", len(ev.Items)), err
}

// release adalah compensating action C2: kembalikan stok yang dipesan.
func (a *app) release(ctx context.Context, tx pgx.Tx, ev platform.OrderEvent) (string, error) {
	rows, err := tx.Query(ctx, `UPDATE reservations SET status='RELEASED' WHERE order_id=$1 AND status='RESERVED'
		RETURNING product_id, qty`, ev.OrderID)
	if err != nil {
		return "", err
	}
	var items []platform.Item
	for rows.Next() {
		var it platform.Item
		if err := rows.Scan(&it.ProductID, &it.Qty); err != nil {
			rows.Close()
			return "", err
		}
		items = append(items, it)
	}
	rows.Close()
	for _, it := range items {
		if _, err := tx.Exec(ctx, `UPDATE stock SET available=available+$2, reserved=reserved-$2 WHERE product_id=$1`, it.ProductID, it.Qty); err != nil {
			return "", err
		}
	}
	_, err = platform.Enqueue(ctx, tx, platform.SubjInventoryReleased, ev)
	return fmt.Sprintf("kompensasi C2: %d baris reservasi dilepas", len(items)), err
}

// commitSold menandai reservasi menjadi terjual setelah order dikonfirmasi.
func (a *app) commitSold(ctx context.Context, tx pgx.Tx, ev platform.OrderEvent) (string, error) {
	rows, err := tx.Query(ctx, `UPDATE reservations SET status='SOLD' WHERE order_id=$1 AND status='RESERVED' RETURNING product_id, qty`, ev.OrderID)
	if err != nil {
		return "", err
	}
	var items []platform.Item
	for rows.Next() {
		var it platform.Item
		_ = rows.Scan(&it.ProductID, &it.Qty)
		items = append(items, it)
	}
	rows.Close()
	for _, it := range items {
		if _, err := tx.Exec(ctx, `UPDATE stock SET reserved=reserved-$2 WHERE product_id=$1`, it.ProductID, it.Qty); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%d baris reservasi menjadi SOLD", len(items)), nil
}
