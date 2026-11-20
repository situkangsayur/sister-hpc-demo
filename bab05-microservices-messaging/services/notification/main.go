// Service notification: mengirim "surel" ketika pesanan dikonfirmasi atau dibatalkan
// (database "notification"). Langkah yang tidak bisa dikompensasi diletakkan paling akhir.
// Service ini juga menjadi penerima pada lab delivery semantics (lihat lab.go).
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"sister-bab05/services/platform"
)

type app struct {
	db  *pgxpool.Pool
	nc  *nats.Conn
	log *slog.Logger
}

func main() {
	log := platform.NewLogger("notification")
	ctx := context.Background()
	db, err := platform.ConnectDB(ctx, platform.Env("DATABASE_URL", "postgres://sister:changeme@postgres:5432/notification"))
	if err != nil {
		log.Error("db", "err", err)
		return
	}
	if err := platform.Migrate(ctx, db, `CREATE TABLE IF NOT EXISTS notifications (order_id TEXT NOT NULL,
		kind TEXT NOT NULL, message TEXT NOT NULL, at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY (order_id, kind))`); err != nil {
		log.Error("migrate", "err", err)
		return
	}
	natsURL := platform.Env("NATS_URL", "nats://nats:4222")
	nc, js, err := platform.ConnectNATS(natsURL, "notification")
	if err != nil {
		log.Error("nats", "err", err)
		return
	}
	if err := platform.EnsureStream(ctx, js, "SHOP", []string{"shop.>"}); err != nil {
		log.Error("stream", "err", err)
		return
	}
	a := &app{db: db, nc: nc, log: log}
	if err := platform.Consume(ctx, js, platform.ConsumerSpec{Stream: "SHOP", Durable: "notification",
		Filters: []string{platform.SubjOrderConfirmed, platform.SubjOrderCancelled}}, log, nil, a.handle); err != nil {
		log.Error("consumer", "err", err)
		return
	}
	lab, err := newLab(ctx, natsURL, js, log)
	if err != nil {
		log.Error("lab", "err", err)
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		platform.WriteJSON(w, 200, map[string]any{"service": "notification", "db": db.Ping(r.Context()) == nil, "nats": nc.IsConnected()})
	})
	mux.HandleFunc("GET /notifications", a.handleList)
	lab.routes(mux)
	log.Info("notification siap", "addr", ":8080")
	platform.Serve(":8080", platform.Middleware(log, mux))
	select {}
}

func (a *app) handle(ctx context.Context, d *platform.Delivery) error {
	var ev platform.OrderEvent
	if err := json.Unmarshal(d.Data, &ev); err != nil {
		return nil
	}
	kind, msg := "confirmed", "Pesanan "+ev.OrderID+" dikonfirmasi. Terima kasih!"
	if d.Subject == platform.SubjOrderCancelled {
		kind, msg = "cancelled", "Pesanan "+ev.OrderID+" dibatalkan: "+ev.Reason
	}
	note := "surel terkirim: " + msg
	// Kunci alami (order_id, kind) membuat pengiriman idempotent tanpa tabel dedup.
	err := platform.InTx(ctx, a.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO notifications (order_id, kind, message) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
			ev.OrderID, kind, msg)
		if err == nil && tag.RowsAffected() == 0 {
			note = "surel untuk order ini sudah pernah dikirim, dilewati"
		}
		return err
	})
	action := "consumed"
	if err != nil {
		action, note = "error", err.Error()
	}
	a.log.Info("notifikasi", "request_id", d.RequestID, "order_id", ev.OrderID, "kind", kind, "note", note)
	platform.Audit(a.nc, "notification", map[string]any{"action": action, "subject": d.Subject, "msg_id": d.MsgID,
		"order_id": ev.OrderID, "request_id": d.RequestID, "delivered": d.NumDelivered, "note": note})
	return err
}

func (a *app) handleList(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(), `SELECT order_id, kind, message, at FROM notifications ORDER BY at DESC LIMIT 20`)
	if err != nil {
		platform.WriteJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	defer rows.Close()
	type n struct {
		OrderID string    `json:"order_id"`
		Kind    string    `json:"kind"`
		Message string    `json:"message"`
		At      time.Time `json:"at"`
	}
	out := []n{}
	for rows.Next() {
		var x n
		if rows.Scan(&x.OrderID, &x.Kind, &x.Message, &x.At) == nil {
			out = append(out, x)
		}
	}
	platform.WriteJSON(w, 200, out)
}
