// Service node: salah satu dari tiga node identik di belakang nginx.
// Port utama (8080) melayani klien lewat load balancer; port admin (9090) hanya untuk
// menyuntik kegagalan dan tidak dilewatkan nginx.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

type app struct {
	node  string
	store *Store
	fault *FaultBox
	log   *slog.Logger
}

func main() {
	node := env("NODE_NAME", "node")
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "node", "node", node)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	store, err := Buka(ctx, env("DATABASE_URL", ""))
	cancel()
	if err != nil {
		log.Error("gagal terhubung ke database", "err", err.Error())
		os.Exit(1)
	}
	a := &app{node: node, store: store, fault: NewFaultBox(), log: log}
	utama := &mainServer{addr: ":" + env("PORT", "8080"), handler: a.routes(), log: log}
	utama.start()
	log.Info("node siap", "port", env("PORT", "8080"), "admin_port", env("ADMIN_PORT", "9090"))

	admin := http.NewServeMux()
	admin.HandleFunc("GET /admin/fault", func(w http.ResponseWriter, r *http.Request) {
		tulisJSON(w, 200, a.fault.Get())
	})
	admin.HandleFunc("POST /admin/fault", func(w http.ResponseWriter, r *http.Request) {
		var f Fault
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&f); err != nil {
			tulisJSON(w, 400, map[string]string{"error": "JSON tidak valid"})
			return
		}
		if f.Mode == "" {
			f.Mode = ModeNormal
		}
		if err := f.Validate(); err != nil {
			tulisJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		a.fault.Set(f)
		if f.Mode == ModeMati {
			utama.stop()
		} else {
			utama.start()
		}
		log.Warn("fault diubah", "mode", f.Mode, "latency_ms", f.LatencyMs, "jitter_ms", f.JitterMs,
			"drop_request_pct", f.DropRequestPct, "drop_response_pct", f.DropResponsePct, "skew_ms", f.SkewMs)
		tulisJSON(w, 200, f)
	})
	admin.HandleFunc("POST /admin/crash", func(w http.ResponseWriter, r *http.Request) {
		log.Error("crash disimulasikan, proses keluar dengan kode 1")
		tulisJSON(w, 200, map[string]string{"status": "crash"})
		go func() { time.Sleep(100 * time.Millisecond); os.Exit(1) }()
	})
	srv := &http.Server{Addr: ":" + env("ADMIN_PORT", "9090"), Handler: admin, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("admin server berhenti", "err", err.Error())
		os.Exit(1)
	}
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	// Heartbeat: dipakai monitor. Mengembalikan jam node agar monitor bisa memperkirakan offset.
	mux.HandleFunc("GET /sehat", func(w http.ResponseWriter, r *http.Request) {
		tulisJSON(w, 200, map[string]any{"node": a.node, "ok": true, "waktu_node_ms": a.fault.Now().UnixMilli()})
	})
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("pong"))
	})
	// Baca: idempotent, aman diulang (nginx boleh retry ke node lain).
	mux.HandleFunc("GET /api/sapa", func(w http.ResponseWriter, r *http.Request) {
		total, err := a.store.Total(r.Context())
		if err != nil {
			a.gagalDB(w, r, err)
			return
		}
		tulisJSON(w, 200, map[string]any{"node": a.node, "total": total, "waktu_node_ms": a.fault.Now().UnixMilli()})
	})
	// Tulis: tidak idempotent. Bila jawabannya hilang, klien tidak tahu apakah data sudah tercatat.
	mux.HandleFunc("POST /api/catat", func(w http.ResponseWriter, r *http.Request) {
		id, err := a.store.Catat(r.Context(), a.node, r.Header.Get("X-Request-ID"), a.fault.Now())
		if err != nil {
			a.gagalDB(w, r, err)
			return
		}
		tulisJSON(w, 201, map[string]any{"node": a.node, "id": id})
	})
	mux.HandleFunc("GET /api/statistik", func(w http.ResponseWriter, r *http.Request) {
		per, err := a.store.PerNode(r.Context())
		if err != nil {
			a.gagalDB(w, r, err)
			return
		}
		mundur, err := a.store.UrutanTerbalik(r.Context())
		if err != nil {
			a.gagalDB(w, r, err)
			return
		}
		var total int64
		for _, c := range per {
			total += c
		}
		tulisJSON(w, 200, map[string]any{"node": a.node, "per_node": per, "total": total, "urutan_jam_mundur": mundur})
	})
	mux.HandleFunc("POST /api/kosongkan", func(w http.ResponseWriter, r *http.Request) {
		if err := a.store.Kosongkan(r.Context()); err != nil {
			a.gagalDB(w, r, err)
			return
		}
		a.log.Warn("data demo dikosongkan", "request_id", r.Header.Get("X-Request-ID"))
		tulisJSON(w, 200, map[string]any{"ok": true})
	})
	// Kerja kecil yang sama dengan fungsi lokal di monitor, untuk membandingkan
	// panggilan fungsi (monolith) dengan panggilan jaringan (layanan terdistribusi).
	mux.HandleFunc("GET /api/hitung", func(w http.ResponseWriter, r *http.Request) {
		x, _ := strconv.Atoi(r.URL.Query().Get("x"))
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strconv.Itoa(Kerja(x))))
	})
	return a.chaos(mux)
}

// Kerja adalah fungsi kecil yang identik dengan versi lokal di monitor.
func Kerja(x int) int {
	s := 0
	for i := 0; i < 64; i++ {
		s += (x + i) * (x ^ i) % 97
	}
	return s
}

func (a *app) gagalDB(w http.ResponseWriter, r *http.Request, err error) {
	a.log.Error("database gagal", "err", err.Error(), "request_id", r.Header.Get("X-Request-ID"))
	tulisJSON(w, 503, map[string]string{"error": "database tidak tersedia", "node": a.node})
}

// chaos menerapkan fault ke setiap request di port utama, lalu mencatat log satu baris.
func (a *app) chaos(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mulai := time.Now()
		reqID := r.Header.Get("X-Request-ID")
		f := a.fault.Get()
		d := f.Decide(randFloat)
		w.Header().Set("X-Node", a.node)
		hasil := "ok"
		defer func() {
			if r.URL.Path == "/sehat" || r.URL.Path == "/ping" || r.URL.Path == "/api/hitung" {
				return // heartbeat dan uji kecepatan terlalu sering untuk dicatat per request
			}
			a.log.Info("request", "method", r.Method, "path", r.URL.Path, "hasil", hasil,
				"durasi_ms", time.Since(mulai).Milliseconds(), "request_id", reqID, "fault_mode", f.Mode)
		}()
		if d.Hang {
			hasil = "hang"
			<-r.Context().Done() // tidak pernah menjawab; menunggu klien menyerah
			return
		}
		if d.Delay > 0 {
			select {
			case <-time.After(d.Delay):
			case <-r.Context().Done():
				hasil = "dibatalkan_klien"
				return
			}
		}
		if d.DropRequest {
			hasil = "drop_request"
			putus(w)
			return
		}
		if d.DropResponse {
			// Request tetap diproses (misalnya data sudah tercatat), tetapi jawabannya dibuang.
			buf := &bufferWriter{header: http.Header{}}
			next.ServeHTTP(buf, r)
			hasil = "drop_response"
			putus(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// putus memutus koneksi TCP tanpa mengirim response HTTP apa pun.
func putus(w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		panic(http.ErrAbortHandler)
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetLinger(0) // kirim RST, bukan FIN
	}
	_ = conn.Close()
}

type bufferWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (b *bufferWriter) Header() http.Header         { return b.header }
func (b *bufferWriter) Write(p []byte) (int, error) { return b.body.Write(p) }
func (b *bufferWriter) WriteHeader(s int)           { b.status = s }

// mainServer bisa dimatikan dan dinyalakan lagi (mode "mati") tanpa mematikan proses.
type mainServer struct {
	mu      sync.Mutex
	addr    string
	handler http.Handler
	srv     *http.Server
	log     *slog.Logger
}

func (m *mainServer) start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.srv != nil {
		return
	}
	var ln net.Listener
	var err error
	for i := 0; i < 50; i++ { // port lama mungkin belum lepas
		if ln, err = net.Listen("tcp", m.addr); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		m.log.Error("gagal membuka port utama", "err", err.Error())
		os.Exit(1)
	}
	srv := &http.Server{Handler: m.handler, ReadHeaderTimeout: 5 * time.Second}
	m.srv = srv
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.log.Error("server utama berhenti", "err", err.Error())
		}
	}()
}

func (m *mainServer) stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.srv == nil {
		return
	}
	_ = m.srv.Close() // tutup listener dan semua koneksi aktif
	m.srv = nil
}

func tulisJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
