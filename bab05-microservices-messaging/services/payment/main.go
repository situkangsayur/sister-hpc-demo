// Service payment: pemilik data pembayaran (database "payment"). Bereaksi pada
// shop.inventory.reserved (T3: tagih). Gagal bayar -> shop.payment.failed, yang memicu
// kompensasi di inventory (C2) lalu order (C1). order_id unik membuat penagihan idempotent.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"sister-bab05/services/platform"
)

type app struct {
	db       *pgxpool.Pool
	nc       *nats.Conn
	log      *slog.Logger
	mu       sync.Mutex
	failRate float64
}

func main() {
	log := platform.NewLogger("payment")
	ctx := context.Background()
	db, err := platform.ConnectDB(ctx, platform.Env("DATABASE_URL", "postgres://sister:changeme@postgres:5432/payment"))
	if err != nil {
		log.Error("db", "err", err)
		return
	}
	if err := platform.Migrate(ctx, db, `CREATE TABLE IF NOT EXISTS payments (order_id TEXT PRIMARY KEY,
		amount INT NOT NULL, status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		log.Error("migrate", "err", err)
		return
	}
	nc, js, err := platform.ConnectNATS(platform.Env("NATS_URL", "nats://nats:4222"), "payment")
	if err != nil {
		log.Error("nats", "err", err)
		return
	}
	if err := platform.EnsureStream(ctx, js, "SHOP", []string{"shop.>"}); err != nil {
		log.Error("stream", "err", err)
		return
	}
	a := &app{db: db, nc: nc, log: log}
	go platform.RunRelay(ctx, db, js, log, nil)
	if err := platform.Consume(ctx, js, platform.ConsumerSpec{Stream: "SHOP", Durable: "payment",
		Filters: []string{platform.SubjInventoryReserved}}, log, nil, a.handle); err != nil {
		log.Error("consumer", "err", err)
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		platform.WriteJSON(w, 200, map[string]any{"service": "payment", "db": db.Ping(r.Context()) == nil, "nats": nc.IsConnected()})
	})
	mux.HandleFunc("GET /admin/fault", func(w http.ResponseWriter, r *http.Request) {
		platform.WriteJSON(w, 200, map[string]any{"fail_rate": a.rate()})
	})
	mux.HandleFunc("POST /admin/fault", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			FailRate *float64 `json:"fail_rate"`
		}
		if err := platform.ReadJSON(r, &in); err != nil || in.FailRate == nil {
			platform.WriteJSON(w, 400, map[string]any{"error": "butuh fail_rate 0..1"})
			return
		}
		a.mu.Lock()
		a.failRate = max(0, min(*in.FailRate, 1))
		a.mu.Unlock()
		log.Warn("fault payment diubah", "request_id", platform.RequestID(r.Context()), "fail_rate", a.rate())
		platform.WriteJSON(w, 200, map[string]any{"fail_rate": a.rate()})
	})
	log.Info("payment siap", "addr", ":8080")
	platform.Serve(":8080", platform.Middleware(log, mux))
	select {}
}

func (a *app) rate() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.failRate
}

// decide menentukan hasil penagihan (dipisah agar mudah diuji).
func decide(card string, failRate, roll float64) (ok bool, reason string) {
	switch {
	case card == "declined":
		return false, "kartu ditolak bank"
	case roll < failRate:
		return false, "payment gateway bank sedang gangguan (fault disuntik)"
	}
	return true, ""
}

func (a *app) handle(ctx context.Context, d *platform.Delivery) error {
	var ev platform.OrderEvent
	if err := json.Unmarshal(d.Data, &ev); err != nil {
		return nil
	}
	time.Sleep(300 * time.Millisecond) // waktu "bank" memproses
	ok, reason := decide(ev.Card, a.rate(), rand.Float64())
	note := ""
	dup, err := platform.ProcessOnce(ctx, a.db, "payment", d.MsgID, true, func(tx pgx.Tx) error {
		status := "SUCCEEDED"
		if !ok {
			status = "FAILED"
		}
		// order_id PRIMARY KEY: kunci idempotency alami, satu order hanya ditagih sekali.
		tag, err := tx.Exec(ctx, `INSERT INTO payments (order_id, amount, status, reason) VALUES ($1,$2,$3,$4)
			ON CONFLICT (order_id) DO NOTHING`, ev.OrderID, ev.Total, status, reason)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			note = "order sudah pernah ditagih, tidak ditagih ulang"
			return nil
		}
		ev.Reason = reason
		subj := platform.SubjPaymentSucceeded
		if !ok {
			subj = platform.SubjPaymentFailed
		}
		note = status + " " + reason
		_, err = platform.Enqueue(ctx, tx, subj, ev)
		return err
	})
	action := "consumed"
	if dup {
		action, note = "duplicate", "msg_id sudah diproses"
	}
	if err != nil {
		action, note = "error", err.Error()
	}
	a.log.Info("penagihan", "request_id", d.RequestID, "order_id", ev.OrderID, "note", note, "delivered", d.NumDelivered)
	platform.Audit(a.nc, "payment", map[string]any{"action": action, "subject": d.Subject, "msg_id": d.MsgID,
		"order_id": ev.OrderID, "request_id": d.RequestID, "delivered": d.NumDelivered, "note": note})
	return err
}
