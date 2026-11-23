// Service monitor: "menara pengawas" untuk UI. Ia berlangganan semua subject NATS
// (shop.>, bank.>, audit.>, lab.>) untuk timeline dan status saga, memeriksa health setiap service
// dan replica, membaca status circuit breaker, mengatur fault injection, dan menjalankan
// lab delivery semantics (publish core NATS vs JetStream).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"sister-bab05/services/platform"
)

type app struct {
	st     *store
	nc     *nats.Conn
	js     jetstream.JetStream
	db     *pgxpool.Pool
	search *platform.SearchClient
	log    *slog.Logger
	client *http.Client

	labMu        sync.Mutex
	labPublished map[string]int
}

func main() {
	log := platform.NewLogger("monitor")
	ctx := context.Background()
	nc, js, err := platform.ConnectNATS(platform.Env("NATS_URL", "nats://nats:4222"), "monitor")
	if err != nil {
		log.Error("nats", "err", err)
		return
	}
	db, err := platform.ConnectDB(ctx, platform.Env("DATABASE_URL", "postgres://sister:changeme@postgres:5432/postgres"))
	if err != nil {
		log.Error("db", "err", err)
		return
	}
	a := &app{st: newStore(), nc: nc, js: js, db: db, log: log, client: &http.Client{Timeout: 1500 * time.Millisecond},
		search:       platform.NewSearchClient(platform.Env("OPENSEARCH_URLS", "http://opensearch-1:9200,http://opensearch-2:9200")),
		labPublished: map[string]int{}}
	for _, subj := range []string{"shop.>", "bank.>", "audit.>", "lab.>"} {
		if _, err := nc.Subscribe(subj, a.onMsg); err != nil {
			log.Error("subscribe", "err", err)
			return
		}
	}
	if err := platform.EnsureStream(ctx, js, "LAB", []string{"lab.js.>"}); err != nil {
		log.Error("stream LAB", "err", err)
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		platform.WriteJSON(w, 200, map[string]any{"service": "monitor", "nats": nc.IsConnected()})
	})
	mux.HandleFunc("GET /events", a.handleEvents)
	mux.HandleFunc("GET /saga", func(w http.ResponseWriter, r *http.Request) { platform.WriteJSON(w, 200, a.st.sagaList()) })
	mux.HandleFunc("GET /health", a.handleHealth)
	mux.HandleFunc("GET /breakers", a.handleBreakers)
	mux.HandleFunc("GET /faults", a.handleFaults)
	mux.HandleFunc("POST /faults/payment", a.forward("http://payment:8080/admin/fault"))
	mux.HandleFunc("POST /faults/inventory", a.forward("http://inventory:8080/admin/fault"))
	mux.HandleFunc("POST /faults/reset-stock", a.forward("http://inventory:8080/admin/reset-stock"))
	mux.HandleFunc("POST /faults/replica/{name}", a.handleReplica)
	mux.HandleFunc("POST /faults/reset", a.handleResetAll)
	mux.HandleFunc("GET /stock", a.proxyGet("http://inventory:8080/stock"))
	mux.HandleFunc("GET /notifications", a.proxyGet("http://notification:8080/notifications"))
	mux.HandleFunc("GET /opensearch", a.handleOpenSearch)
	mux.HandleFunc("GET /streams", a.handleStreams)
	mux.HandleFunc("GET /streams/{name}/last", a.handleLastMsg)
	mux.HandleFunc("GET /lab/stats", a.handleLabStats)
	mux.HandleFunc("POST /lab/publish", a.handleLabPublish)
	mux.HandleFunc("POST /lab/offline", a.forward("http://notification:8080/lab/offline"))
	mux.HandleFunc("POST /lab/reset", a.handleLabReset)
	log.Info("monitor siap", "addr", ":8080")
	platform.Serve(":8080", platform.Middleware(log, mux))
	select {}
}

func (a *app) onMsg(m *nats.Msg) {
	e := Event{At: time.Now().UTC(), Subject: m.Subject}
	switch {
	case len(m.Subject) > 5 && m.Subject[:5] == "shop.":
		e.Kind, e.From, e.To = "publish", publisherOf(m.Subject), consumersOf(m.Subject)
		if m.Header != nil {
			e.MsgID, e.RequestID = m.Header.Get(jetstream.MsgIDHeader), m.Header.Get(platform.HeaderRequestID)
		}
		var ev platform.OrderEvent
		if json.Unmarshal(m.Data, &ev) == nil {
			e.OrderID, e.Note = ev.OrderID, ev.Reason
		}
	case strings.HasPrefix(m.Subject, "bank."):
		// Event bank berbentuk envelope kontrak DE: {event_id, event_type, occurred_at, data}.
		e.Kind, e.From = "publish", "bank"
		if m.Header != nil {
			e.MsgID, e.RequestID = m.Header.Get(jetstream.MsgIDHeader), m.Header.Get(platform.HeaderRequestID)
		}
		e.OrderID, e.Note = bankNote(m.Data)
	case len(m.Subject) > 6 && m.Subject[:6] == "audit.":
		var f struct {
			Service   string `json:"service"`
			Replica   string `json:"replica"`
			Action    string `json:"action"`
			Subject   string `json:"subject"`
			MsgID     string `json:"msg_id"`
			OrderID   string `json:"order_id"`
			RequestID string `json:"request_id"`
			Delivered int    `json:"delivered"`
			Note      string `json:"note"`
		}
		_ = json.Unmarshal(m.Data, &f)
		e.Kind, e.From, e.Replica, e.Action, e.MsgID = "audit", f.Service, f.Replica, f.Action, f.MsgID
		e.OrderID, e.RequestID, e.Delivered, e.Note = f.OrderID, f.RequestID, f.Delivered, f.Note
		if f.Subject != "" {
			e.Subject = f.Subject
		}
	default:
		e.Kind, e.From, e.Note = "lab", "monitor", string(m.Data)
		e.To = []string{"notification"}
	}
	a.st.add(e)
}

func (a *app) handleEvents(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	evs, last := a.st.since(since, 300)
	platform.WriteJSON(w, 200, map[string]any{"events": evs, "last_seq": last})
}

// call melakukan request ke service internal sambil meneruskan request id.
func (a *app) call(ctx context.Context, method, url string, body []byte) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(platform.HeaderRequestID, platform.RequestID(ctx))
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b, resp.StatusCode, nil
}

func (a *app) forward(url string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if len(body) == 0 {
			body = []byte("{}")
		}
		b, st, err := a.call(r.Context(), http.MethodPost, url, body)
		if err != nil {
			platform.WriteJSON(w, 502, map[string]any{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st)
		_, _ = w.Write(b)
	}
}

func (a *app) proxyGet(url string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, st, err := a.call(r.Context(), http.MethodGet, url, nil)
		if err != nil {
			platform.WriteJSON(w, 502, map[string]any{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st)
		_, _ = w.Write(b)
	}
}

type target struct{ Name, Group, URL string }

var targets = []target{
	{"apisix", "gateway", "http://apisix:9080/gw/ping"},
	{"nginx", "load balancer", "http://order-lb/nginx-health"},
	{"order-1", "order (replica)", "http://order-1:8081/healthz"},
	{"order-2", "order (replica)", "http://order-2:8081/healthz"},
	{"catalog", "service", "http://catalog:8080/healthz"},
	{"inventory", "service", "http://inventory:8080/healthz"},
	{"payment", "service", "http://payment:8080/healthz"},
	{"bank", "service", "http://bank:8080/healthz"},
	{"notification", "service", "http://notification:8080/healthz"},
	{"nats", "broker", "http://nats:8222/healthz?js-enabled-only=true"},
	{"opensearch-1", "search", "http://opensearch-1:9200/"},
	{"opensearch-2", "search", "http://opensearch-2:9200/"},
}

type healthRow struct {
	Name      string `json:"name"`
	Group     string `json:"group"`
	Status    string `json:"status"` // up | down | simulated-down
	LatencyMs int64  `json:"latency_ms"`
	Detail    string `json:"detail,omitempty"`
}

func (a *app) handleHealth(w http.ResponseWriter, r *http.Request) {
	out := make([]healthRow, len(targets)+1)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t target) {
			defer wg.Done()
			start := time.Now()
			b, st, err := a.call(r.Context(), http.MethodGet, t.URL, nil)
			row := healthRow{Name: t.Name, Group: t.Group, Status: "up", LatencyMs: time.Since(start).Milliseconds()}
			switch {
			case err != nil:
				row.Status, row.Detail = "down", err.Error()
			case st >= 300:
				row.Status, row.Detail = "down", fmt.Sprintf("HTTP %d", st)
			default:
				var h struct {
					Up     *bool `json:"up"`
					Served int   `json:"served"`
				}
				if json.Unmarshal(b, &h) == nil && h.Up != nil {
					row.Detail = fmt.Sprintf("melayani %d checkout", h.Served)
					if !*h.Up {
						row.Status = "simulated-down"
						row.Detail = "port publik ditutup (disuntik dari UI)"
					}
				}
			}
			out[i] = row
		}(i, t)
	}
	start := time.Now()
	pg := healthRow{Name: "postgres", Group: "database", Status: "up"}
	if err := a.db.Ping(r.Context()); err != nil {
		pg.Status, pg.Detail = "down", err.Error()
	}
	pg.LatencyMs = time.Since(start).Milliseconds()
	pg.Detail = "catalog, orders, inventory, payment, notification, bank"
	wg.Wait()
	out[len(targets)] = pg
	platform.WriteJSON(w, 200, out)
}

func (a *app) handleBreakers(w http.ResponseWriter, r *http.Request) {
	out := []json.RawMessage{}
	for _, n := range []string{"order-1", "order-2"} {
		b, st, err := a.call(r.Context(), http.MethodGet, "http://"+n+":8081/breakers", nil)
		if err != nil || st != 200 {
			b, _ = json.Marshal(map[string]any{"replica": n, "error": fmt.Sprint(err, st)})
		}
		out = append(out, b)
	}
	platform.WriteJSON(w, 200, out)
}

func (a *app) handleFaults(w http.ResponseWriter, r *http.Request) {
	get := func(url string) json.RawMessage {
		b, st, err := a.call(r.Context(), http.MethodGet, url, nil)
		if err != nil || st != 200 {
			return json.RawMessage(`null`)
		}
		return b
	}
	reps := map[string]json.RawMessage{}
	for _, n := range []string{"order-1", "order-2"} {
		reps[n] = get("http://" + n + ":8081/healthz")
	}
	platform.WriteJSON(w, 200, map[string]any{"payment": get("http://payment:8080/admin/fault"),
		"inventory": get("http://inventory:8080/admin/fault"), "replicas": reps})
}

func (a *app) handleReplica(w http.ResponseWriter, r *http.Request) {
	n := r.PathValue("name")
	if n != "order-1" && n != "order-2" {
		platform.WriteJSON(w, 404, map[string]any{"error": "replica tidak dikenal"})
		return
	}
	a.forward("http://"+n+":8081/admin/state")(w, r)
}

func (a *app) handleResetAll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	errs := []string{}
	for _, c := range []struct{ url, body string }{
		{"http://payment:8080/admin/fault", `{"fail_rate":0}`},
		{"http://inventory:8080/admin/fault", `{"delay_ms":0,"crash_before_ack":false,"idempotent":true}`},
		{"http://order-1:8081/admin/state", `{"down":false}`},
		{"http://order-2:8081/admin/state", `{"down":false}`},
	} {
		if _, st, err := a.call(ctx, http.MethodPost, c.url, []byte(c.body)); err != nil || st != 200 {
			errs = append(errs, fmt.Sprint(c.url, " ", err, st))
		}
	}
	platform.WriteJSON(w, 200, map[string]any{"ok": len(errs) == 0, "errors": errs})
}

func (a *app) handleOpenSearch(w http.ResponseWriter, r *http.Request) {
	get := func(path string) json.RawMessage {
		b, _, st, err := a.search.Do(r.Context(), http.MethodGet, path, nil)
		if err != nil || st != 200 {
			return json.RawMessage(`null`)
		}
		return b
	}
	platform.WriteJSON(w, 200, map[string]any{
		"health": get("/_cluster/health"),
		"nodes":  get("/_cat/nodes?format=json&h=name,ip,heap.percent,heap.max,node.role,cluster_manager"),
		"shards": get("/_cat/shards/products?format=json&h=index,shard,prirep,state,docs,node"),
	})
}

// handleLabPublish menerbitkan n pesan ke core NATS (lab.core.task) atau JetStream (lab.js.task).
func (a *app) handleLabPublish(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode string `json:"mode"`
		N    int    `json:"n"`
	}
	if err := platform.ReadJSON(r, &in); err != nil || (in.Mode != "core" && in.Mode != "js") {
		platform.WriteJSON(w, 400, map[string]any{"error": "mode harus core atau js"})
		return
	}
	in.N = max(1, min(in.N, 100))
	ok := 0
	for i := 0; i < in.N; i++ {
		data := []byte(fmt.Sprintf(`tugas #%d (%s)`, i+1, in.Mode))
		var err error
		if in.Mode == "core" {
			err = a.nc.Publish("lab.core.task", data) // fire-and-forget: tidak ada konfirmasi
		} else {
			_, err = a.js.Publish(r.Context(), "lab.js.task", data) // menunggu PubAck dari stream
		}
		if err == nil {
			ok++
		}
	}
	_ = a.nc.Flush()
	a.labMu.Lock()
	a.labPublished[in.Mode] += ok
	a.labMu.Unlock()
	a.handleLabStats(w, r)
}

func (a *app) handleLabStats(w http.ResponseWriter, r *http.Request) {
	b, _, err := a.call(r.Context(), http.MethodGet, "http://notification:8080/lab/stats", nil)
	var recv map[string]any
	if err == nil {
		_ = json.Unmarshal(b, &recv)
	}
	a.labMu.Lock()
	pub := map[string]int{"core": a.labPublished["core"], "js": a.labPublished["js"]}
	a.labMu.Unlock()
	var pending uint64
	if c, err := a.js.Consumer(r.Context(), "LAB", "lab-worker"); err == nil {
		if info, err := c.Info(r.Context()); err == nil {
			pending = info.NumPending + uint64(info.NumAckPending)
		}
	}
	platform.WriteJSON(w, 200, map[string]any{"published": pub, "receiver": recv, "js_pending": pending})
}

func (a *app) handleLabReset(w http.ResponseWriter, r *http.Request) {
	_, _, _ = a.call(r.Context(), http.MethodPost, "http://notification:8080/lab/reset", []byte("{}"))
	a.labMu.Lock()
	a.labPublished = map[string]int{}
	a.labMu.Unlock()
	a.handleLabStats(w, r)
}

// bankNote meringkas event BANK untuk timeline; order_id diisi untuk transaksi bayar_belanja
// sehingga event bank ikut tersaring bersama saga order yang dibayarnya.
func bankNote(b []byte) (orderID, note string) {
	var env struct {
		EventID    string         `json:"event_id"`
		OccurredAt string         `json:"occurred_at"`
		Data       map[string]any `json:"data"`
	}
	if json.Unmarshal(b, &env) != nil {
		return "", ""
	}
	d := env.Data
	str := func(k string) string {
		if v, ok := d[k].(string); ok {
			return v
		}
		return ""
	}
	num := func(k string) string {
		if v, ok := d[k].(float64); ok {
			return strconv.FormatInt(int64(v), 10)
		}
		return "-"
	}
	switch {
	case d["jenis"] != nil && d["nominal"] != nil: // transaksi
		note = fmt.Sprintf("%s %s via %s, saldo %s, occurred_at %s", str("jenis"), num("nominal"), str("kanal"), num("saldo_setelah"), env.OccurredAt)
		orderID = str("ref_order_id")
	case d["kode_akad"] != nil:
		note = fmt.Sprintf("akad %s pokok %s status %s", str("kode_akad"), num("pokok"), str("status"))
	case d["no_rekening"] != nil:
		note = fmt.Sprintf("rekening %s %s", str("no_rekening"), str("jenis"))
	case d["cif"] != nil:
		note = fmt.Sprintf("nasabah %s %s, kota %s, segmen %s", str("cif"), str("nama"), str("kota"), str("segmen"))
	case d["wilayah"] != nil:
		note = fmt.Sprintf("cabang %s %s", str("kode"), str("kota"))
	}
	return orderID, note
}

// handleStreams: isi stream JetStream SHOP, BANK, LAB (jumlah pesan, byte, consumer).
func (a *app) handleStreams(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	for _, n := range []string{"SHOP", "BANK", "LAB"} {
		st, err := a.js.Stream(r.Context(), n)
		if err != nil {
			out = append(out, map[string]any{"name": n, "error": err.Error()})
			continue
		}
		info, err := st.Info(r.Context())
		if err != nil {
			continue
		}
		out = append(out, map[string]any{"name": n, "subjects": info.Config.Subjects, "messages": info.State.Msgs,
			"bytes": info.State.Bytes, "consumers": info.State.Consumers, "max_age_s": int64(info.Config.MaxAge.Seconds())})
	}
	platform.WriteJSON(w, 200, out)
}

// handleLastMsg: pesan terakhir untuk satu subject di stream (header + payload), mis.
// GET /streams/BANK/last?subject=bank.transaksi.dibuat. Untuk melihat bentuk event kontrak DE.
func (a *app) handleLastMsg(w http.ResponseWriter, r *http.Request) {
	name, subj := r.PathValue("name"), r.URL.Query().Get("subject")
	if name != "SHOP" && name != "BANK" {
		platform.WriteJSON(w, 404, map[string]any{"error": "stream harus SHOP atau BANK"})
		return
	}
	st, err := a.js.Stream(r.Context(), name)
	if err != nil {
		platform.WriteJSON(w, 502, map[string]any{"error": err.Error()})
		return
	}
	m, err := st.GetLastMsgForSubject(r.Context(), subj)
	if err != nil {
		platform.WriteJSON(w, 404, map[string]any{"error": err.Error()})
		return
	}
	platform.WriteJSON(w, 200, map[string]any{"subject": m.Subject, "seq": m.Sequence, "time": m.Time,
		"headers": m.Header, "data": json.RawMessage(m.Data)})
}
