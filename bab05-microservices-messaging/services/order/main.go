// Service order: pemilik pesanan (database "orders"), berjalan 2 replica di belakang nginx.
// POST /orders memanggil catalog (harga) dan inventory (cek stok) secara synchronous dengan
// timeout, retry, dan circuit breaker, lalu menyimpan pesanan PENDING + event
// shop.order.created dalam satu transaksi (transactional outbox). Sisa saga berjalan lewat event.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"sister-bab05/services/platform"
)

type app struct {
	db        *pgxpool.Pool
	nc        *nats.Conn
	log       *slog.Logger
	replica   string
	catalog   *remote
	inventory *remote
	down      atomic.Bool // replica "dimatikan" dari UI
	served    atomic.Int64

	srvMu sync.Mutex
	srv   *http.Server
	mux   http.Handler
}

func main() {
	log := platform.NewLogger("order")
	ctx := context.Background()
	db, err := platform.ConnectDB(ctx, platform.Env("DATABASE_URL", "postgres://sister:changeme@postgres:5432/orders"))
	if err != nil {
		log.Error("db", "err", err)
		return
	}
	if err := platform.Migrate(ctx, db,
		`CREATE TABLE IF NOT EXISTS orders (id TEXT PRIMARY KEY, idempotency_key TEXT UNIQUE,
		  customer TEXT NOT NULL, items JSONB NOT NULL, total INT NOT NULL, card TEXT NOT NULL,
		  status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', request_id TEXT NOT NULL DEFAULT '',
		  served_by TEXT NOT NULL, precheck TEXT NOT NULL DEFAULT '',
		  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		// Bayar dari rekening bank (kosong = kartu). Kolom tambahan, aman untuk data lama.
		`ALTER TABLE orders ADD COLUMN IF NOT EXISTS no_rekening TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS order_history (id BIGSERIAL PRIMARY KEY, order_id TEXT NOT NULL,
		  status TEXT NOT NULL, note TEXT NOT NULL, by_replica TEXT NOT NULL, at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		// Publication untuk Debezium (CDC mata kuliah Data Engineering).
		`DO $$ BEGIN
		  IF NOT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'dbz_orders') THEN
		    CREATE PUBLICATION dbz_orders FOR TABLE orders, order_history;
		  END IF;
		END $$`); err != nil {
		log.Error("migrate", "err", err)
		return
	}
	nc, js, err := platform.ConnectNATS(platform.Env("NATS_URL", "nats://nats:4222"), "order-"+platform.Env("REPLICA", "?"))
	if err != nil {
		log.Error("nats", "err", err)
		return
	}
	if err := platform.EnsureStream(ctx, js, "SHOP", []string{"shop.>"}); err != nil {
		log.Error("stream", "err", err)
		return
	}
	a := &app{db: db, nc: nc, log: log, replica: platform.Env("REPLICA", "order-?"),
		catalog:   newRemote("catalog", platform.Env("CATALOG_URL", "http://catalog:8080"), 500*time.Millisecond, 3, nc),
		inventory: newRemote("inventory", platform.Env("INVENTORY_URL", "http://inventory:8080"), 300*time.Millisecond, 2, nc)}

	// Relay outbox dan consumer berhenti saat replica "dimatikan"; replica lain mengambil alih
	// karena keduanya berbagi durable consumer "order" (pesan dibagi, bukan digandakan).
	go platform.RunRelay(ctx, db, js, log, &a.down)
	if err := platform.Consume(ctx, js, platform.ConsumerSpec{Stream: "SHOP", Durable: "order",
		Filters: []string{platform.SubjPaymentSucceeded, platform.SubjInventoryRejected, platform.SubjInventoryReleased}},
		log, &a.down, a.handleEvent); err != nil {
		log.Error("consumer", "err", err)
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		platform.WriteJSON(w, 200, map[string]any{"service": "order", "replica": a.replica, "db": db.Ping(r.Context()) == nil})
	})
	mux.HandleFunc("POST /orders", a.handleCreate)
	mux.HandleFunc("GET /orders", a.handleList)
	mux.HandleFunc("GET /orders/{id}", a.handleGet)
	a.mux = platform.Middleware(log, mux)
	a.startPublic()

	// Port admin terpisah (8081) tetap hidup walau port publik "dimatikan".
	admin := http.NewServeMux()
	admin.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		platform.WriteJSON(w, 200, map[string]any{"replica": a.replica, "up": !a.down.Load(), "served": a.served.Load(),
			"nats": nc.IsConnected()})
	})
	admin.HandleFunc("GET /breakers", func(w http.ResponseWriter, r *http.Request) {
		platform.WriteJSON(w, 200, map[string]any{"replica": a.replica, "up": !a.down.Load(),
			"breakers": []breakerView{a.catalog.view(), a.inventory.view()}})
	})
	admin.HandleFunc("POST /admin/state", a.handleState)
	log.Info("order siap", "addr", ":8080", "admin", ":8081")
	platform.Serve(":8081", platform.Middleware(log, admin))
	select {}
}

func (a *app) startPublic() {
	a.srvMu.Lock()
	defer a.srvMu.Unlock()
	if a.srv != nil {
		return
	}
	ln, err := net.Listen("tcp", ":8080")
	if err != nil {
		a.log.Error("listen", "err", err)
		return
	}
	a.srv = &http.Server{Handler: a.mux, ReadHeaderTimeout: 5 * time.Second}
	go a.srv.Serve(ln) //nolint:errcheck
}

func (a *app) stopPublic() {
	a.srvMu.Lock()
	defer a.srvMu.Unlock()
	if a.srv != nil {
		_ = a.srv.Close() // koneksi ditutup: nginx melihat "connection refused"
		a.srv = nil
	}
}

func (a *app) handleState(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Down bool `json:"down"`
	}
	if err := platform.ReadJSON(r, &in); err != nil {
		platform.WriteJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	a.down.Store(in.Down)
	if in.Down {
		a.stopPublic()
	} else {
		a.startPublic()
	}
	a.log.Warn("state replica diubah", "request_id", platform.RequestID(r.Context()), "down", in.Down)
	platform.Audit(a.nc, "order", map[string]any{"action": "replica", "note": fmt.Sprintf("%s down=%v", a.replica, in.Down)})
	platform.WriteJSON(w, 200, map[string]any{"replica": a.replica, "up": !in.Down})
}

type order struct {
	ID        string          `json:"id"`
	Customer  string          `json:"customer"`
	Items     []platform.Item `json:"items"`
	Total     int             `json:"total"`
	Card      string          `json:"card"`
	NoRek     string          `json:"no_rekening"`
	Status    string          `json:"status"`
	Reason    string          `json:"reason"`
	RequestID string          `json:"request_id"`
	ServedBy  string          `json:"served_by"`
	Precheck  string          `json:"precheck"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

const orderCols = `id, customer, items, total, card, status, reason, request_id, served_by, precheck, created_at, updated_at, no_rekening`

func scanOrder(row pgx.Row) (order, error) {
	var o order
	var items []byte
	err := row.Scan(&o.ID, &o.Customer, &items, &o.Total, &o.Card, &o.Status, &o.Reason, &o.RequestID,
		&o.ServedBy, &o.Precheck, &o.CreatedAt, &o.UpdatedAt, &o.NoRek)
	_ = json.Unmarshal(items, &o.Items)
	return o, err
}

func (a *app) handleCreate(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	a.served.Add(1)
	ctx := r.Context()
	w.Header().Set("X-Served-By", a.replica)
	var in struct {
		Customer string          `json:"customer"`
		Items    []platform.Item `json:"items"`
		Card     string          `json:"card"`
		// NoRekening: bayar dari rekening bank (service bank). Kosong = mode kartu lama.
		NoRekening string `json:"no_rekening"`
	}
	if err := platform.ReadJSON(r, &in); err != nil || len(in.Items) == 0 || len(in.Items) > 10 {
		platform.WriteJSON(w, 400, map[string]any{"error": "body harus berisi 1-10 items"})
		return
	}
	if in.Customer == "" {
		in.Customer = "pembeli-demo"
	}
	in.NoRekening = strings.TrimSpace(in.NoRekening)
	switch {
	case in.NoRekening != "":
		in.Card = "rekening"
	case in.Card != "declined":
		in.Card = "ok"
	}
	key := r.Header.Get("Idempotency-Key")
	reply := func(code int, body map[string]any) {
		body["served_by"] = a.replica
		body["duration_ms"] = time.Since(start).Milliseconds()
		body["breakers"] = []breakerView{a.catalog.view(), a.inventory.view()}
		platform.WriteJSON(w, code, body)
	}
	// Idempotency key: request ulang dengan key yang sama mengembalikan pesanan yang sama.
	if key != "" {
		if o, err := scanOrder(a.db.QueryRow(ctx, `SELECT `+orderCols+` FROM orders WHERE idempotency_key=$1`, key)); err == nil {
			reply(200, map[string]any{"order": o, "replayed": true, "note": "Idempotency-Key sudah pernah dipakai; pesanan lama dikembalikan, tidak dibuat ganda"})
			return
		}
	}

	// 1. Harga dari catalog (synchronous, wajib: tanpa harga pesanan tidak bisa dibuat).
	total := 0
	for i, it := range in.Items {
		if it.Qty < 1 || it.Qty > 5 {
			reply(400, map[string]any{"error": "qty harus 1-5"})
			return
		}
		b, tries, err := a.catalog.get(ctx, "/products/"+it.ProductID)
		if isStatus(err, 404) {
			reply(404, map[string]any{"error": "produk " + it.ProductID + " tidak ada"})
			return
		}
		if err != nil {
			a.log.Warn("catalog gagal", "request_id", platform.RequestID(ctx), "err", err, "tries", tries)
			reply(503, map[string]any{"error": "katalog sedang gangguan, coba lagi nanti", "detail": err.Error()})
			return
		}
		var p platform.Product
		_ = json.Unmarshal(b, &p)
		in.Items[i].Price = p.Price
		total += p.Price * it.Qty
	}

	// 2. Cek stok ke inventory (synchronous, opsional). Bila inventory lambat atau breaker open,
	//    pakai fallback: lewati pengecekan; saga (T2) tetap memastikan stok secara asynchronous.
	ids := make([]string, 0, len(in.Items))
	for _, it := range in.Items {
		ids = append(ids, it.ProductID)
	}
	precheck := "ok"
	b, tries, err := a.inventory.get(ctx, "/stock?precheck=1&ids="+strings.Join(ids, ","))
	if err != nil {
		precheck = fmt.Sprintf("fallback (%d percobaan): %v; stok dipastikan oleh saga", tries, err)
		a.log.Warn("precheck stok fallback", "request_id", platform.RequestID(ctx), "err", err, "tries", tries)
	} else {
		var stock []struct {
			ProductID string `json:"product_id"`
			Available int    `json:"available"`
		}
		_ = json.Unmarshal(b, &stock)
		avail := map[string]int{}
		for _, s := range stock {
			avail[s.ProductID] = s.Available
		}
		for _, it := range in.Items {
			if avail[it.ProductID] < it.Qty {
				reply(409, map[string]any{"error": fmt.Sprintf("stok %s tinggal %d", it.ProductID, avail[it.ProductID])})
				return
			}
		}
	}

	// 3. Satu transaksi lokal: pesanan PENDING + riwayat + event di outbox (T1).
	o := order{ID: "ord-" + platform.NewID()[:8], Customer: in.Customer, Items: in.Items, Total: total, Card: in.Card, NoRek: in.NoRekening,
		Status: StatusPending, RequestID: platform.RequestID(ctx), ServedBy: a.replica, Precheck: precheck}
	var keyArg any
	if key != "" {
		keyArg = key
	}
	err = platform.InTx(ctx, a.db, func(tx pgx.Tx) error {
		items, _ := json.Marshal(o.Items)
		if _, err := tx.Exec(ctx, `INSERT INTO orders (id, idempotency_key, customer, items, total, card, status, request_id, served_by, precheck, no_rekening)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, o.ID, keyArg, o.Customer, items, o.Total, o.Card, o.Status, o.RequestID, o.ServedBy, o.Precheck, o.NoRek); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO order_history (order_id, status, note, by_replica) VALUES ($1,$2,$3,$4)`,
			o.ID, o.Status, "T1 pesanan dibuat", a.replica); err != nil {
			return err
		}
		_, err := platform.Enqueue(ctx, tx, platform.SubjOrderCreated, platform.OrderEvent{OrderID: o.ID,
			Customer: o.Customer, Items: o.Items, Total: o.Total, Card: o.Card, NoRekening: o.NoRek})
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && key != "" {
		// Dua request dengan key sama tiba bersamaan: yang kalah mengembalikan milik yang menang.
		if prev, e := scanOrder(a.db.QueryRow(ctx, `SELECT `+orderCols+` FROM orders WHERE idempotency_key=$1`, key)); e == nil {
			reply(200, map[string]any{"order": prev, "replayed": true})
			return
		}
	}
	if err != nil {
		reply(500, map[string]any{"error": err.Error()})
		return
	}
	a.log.Info("pesanan dibuat", "request_id", o.RequestID, "order_id", o.ID, "total", o.Total, "precheck", precheck)
	reply(201, map[string]any{"order": o, "precheck": precheck})
}

func (a *app) handleList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Served-By", a.replica)
	rows, err := a.db.Query(r.Context(), `SELECT `+orderCols+` FROM orders ORDER BY created_at DESC LIMIT 25`)
	if err != nil {
		platform.WriteJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	defer rows.Close()
	out := []order{}
	for rows.Next() {
		if o, err := scanOrder(rows); err == nil {
			out = append(out, o)
		}
	}
	platform.WriteJSON(w, 200, map[string]any{"served_by": a.replica, "orders": out})
}

func (a *app) handleGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Served-By", a.replica)
	o, err := scanOrder(a.db.QueryRow(r.Context(), `SELECT `+orderCols+` FROM orders WHERE id=$1`, r.PathValue("id")))
	if err != nil {
		platform.WriteJSON(w, 404, map[string]any{"error": "pesanan tidak ditemukan"})
		return
	}
	type hist struct {
		Status string    `json:"status"`
		Note   string    `json:"note"`
		By     string    `json:"by"`
		At     time.Time `json:"at"`
	}
	rows, _ := a.db.Query(r.Context(), `SELECT status, note, by_replica, at FROM order_history WHERE order_id=$1 ORDER BY id`, o.ID)
	h := []hist{}
	if rows != nil {
		for rows.Next() {
			var x hist
			if rows.Scan(&x.Status, &x.Note, &x.By, &x.At) == nil {
				h = append(h, x)
			}
		}
		rows.Close()
	}
	platform.WriteJSON(w, 200, map[string]any{"order": o, "history": h, "served_by": a.replica})
}

// handleEvent adalah sisi order dari saga choreography. Idempotent dua lapis: dedup msg_id
// dan transisi status yang hanya berlaku dari PENDING.
func (a *app) handleEvent(ctx context.Context, d *platform.Delivery) error {
	var ev platform.OrderEvent
	if err := json.Unmarshal(d.Data, &ev); err != nil {
		return nil
	}
	note := ""
	dup, err := platform.ProcessOnce(ctx, a.db, "order", d.MsgID, true, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM orders WHERE id=$1 FOR UPDATE`, ev.OrderID).Scan(&status); err != nil {
			note = "pesanan tidak dikenal"
			return nil
		}
		step, ok := nextStep(status, d.Subject)
		if !ok {
			note = "diabaikan: status sudah " + status
			return nil
		}
		reason := step.Reason
		if ev.Reason != "" {
			reason += ": " + ev.Reason
		}
		if _, err := tx.Exec(ctx, `UPDATE orders SET status=$2, reason=$3, updated_at=now() WHERE id=$1`, ev.OrderID, step.Next, reason); err != nil {
			return err
		}
		label := "T4 dikonfirmasi"
		if step.Next == StatusCancelled {
			label = "C1 dibatalkan"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO order_history (order_id, status, note, by_replica) VALUES ($1,$2,$3,$4)`,
			ev.OrderID, step.Next, label+": "+reason, a.replica); err != nil {
			return err
		}
		ev.Reason = reason
		note = status + " -> " + step.Next
		_, err := platform.Enqueue(ctx, tx, step.Emit, ev)
		return err
	})
	action := "consumed"
	if dup {
		action, note = "duplicate", "msg_id sudah diproses"
	}
	if err != nil {
		action, note = "error", err.Error()
	}
	a.log.Info("event saga", "request_id", d.RequestID, "order_id", ev.OrderID, "subject", d.Subject, "note", note)
	platform.Audit(a.nc, "order", map[string]any{"action": action, "subject": d.Subject, "msg_id": d.MsgID,
		"order_id": ev.OrderID, "request_id": d.RequestID, "delivered": d.NumDelivered, "note": note})
	return err
}
