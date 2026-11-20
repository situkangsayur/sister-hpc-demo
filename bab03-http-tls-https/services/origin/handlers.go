package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// App menyimpan state kecil origin: versi katalog (untuk ETag), counter hit, dan mode "sakit".
type App struct {
	Instance string
	Log      *slog.Logger

	version atomic.Int64
	sick    atomic.Bool

	mu   sync.Mutex
	hits map[string]int
}

// NewApp membuat App dengan versi katalog awal 1.
func NewApp(instance string, log *slog.Logger) *App {
	a := &App{Instance: instance, Log: log, hits: map[string]int{}}
	a.version.Store(1)
	return a
}

func (a *App) count(key string) {
	a.mu.Lock()
	a.hits[key]++
	a.mu.Unlock()
}

func (a *App) snapshot() map[string]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]int, len(a.hits))
	for k, v := range a.hits {
		out[k] = v
	}
	return out
}

// queryInt membaca parameter angka dengan batas bawah dan atas.
func queryInt(r *http.Request, key string, def, lo, hi int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// think mensimulasikan server yang "berpikir" (query database dsb.) sebelum menjawab.
func think(r *http.Request) {
	if ms := queryInt(r, "think", 0, 0, 5000); ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}

// etagMatch membandingkan If-None-Match dengan ETag (weak comparison, RFC 9110 §13.1.2).
func etagMatch(ifNoneMatch, etag string) bool {
	strip := func(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "W/") }
	for _, cand := range strings.Split(ifNoneMatch, ",") {
		c := strip(cand)
		if c == "*" || c == strip(etag) {
			return true
		}
	}
	return false
}

// PageHTML membuat halaman dengan n aset kecil, seperti halaman web yang memuat banyak CSS/ikon.
func PageHTML(n, size, assetThink int) string {
	var b strings.Builder
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><title>Halaman uji Bab 3</title>\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "<link rel=\"stylesheet\" href=\"/asset/%d.css?size=%d&think=%d\">\n", i, size, assetThink)
	}
	b.WriteString("</head><body><h1>Halaman dengan banyak aset kecil</h1></body></html>\n")
	return b.String()
}

// Routes memasang semua handler origin.
func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	// Halaman uji untuk waterfall h1/h2/h3.
	mux.HandleFunc("GET /page", func(w http.ResponseWriter, r *http.Request) {
		think(r)
		n := queryInt(r, "n", 30, 1, 200)
		size := queryInt(r, "size", 2048, 16, 1<<20)
		at := queryInt(r, "asset_think", 0, 0, 2000)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprint(w, PageHTML(n, size, at))
	})
	mux.HandleFunc("GET /asset/{name}", func(w http.ResponseWriter, r *http.Request) {
		think(r)
		size := queryInt(r, "size", 2048, 16, 1<<20)
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Cache-Control", "no-store")
		body := strings.Repeat("/* aset demo */\n", size/16+1)[:size]
		fmt.Fprint(w, body)
	})

	// Form login: dipakai untuk membandingkan HTTP polos vs HTTPS (isi request terlihat atau tidak).
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		http.SetCookie(w, &http.Cookie{Name: "sesi", Value: "s-" + strconv.FormatInt(time.Now().UnixNano()%1e6, 10), HttpOnly: true})
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "selamat datang, %s (saldo Rp 1.250.000)\n", r.PostForm.Get("user"))
	})

	// Katalog: boleh di-cache 5 detik, lalu wajib divalidasi ulang dengan ETag.
	mux.HandleFunc("GET /api/katalog", func(w http.ResponseWriter, r *http.Request) {
		if a.sick.Load() {
			http.Error(w, "instance sakit", http.StatusServiceUnavailable)
			return
		}
		v := a.version.Load()
		etag := fmt.Sprintf("\"v%d\"", v)
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "public, max-age=5")
		if inm := r.Header.Get("If-None-Match"); inm != "" && etagMatch(inm, etag) {
			a.count("katalog_304")
			w.WriteHeader(http.StatusNotModified)
			return
		}
		a.count("katalog_200")
		think(r)
		items := make([]map[string]any, 0, 40)
		for i := 1; i <= 40; i++ {
			items = append(items, map[string]any{
				"id": i, "nama": fmt.Sprintf("Produk %02d versi %d", i, v),
				"deskripsi": "Deskripsi produk yang cukup panjang agar kompresi gzip terlihat manfaatnya. ",
				"harga":     10000 * i,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"versi": v, "instance": a.Instance, "produk": items})
	})

	// Aset statis dengan sidik isi di nama file: max-age panjang + immutable.
	mux.HandleFunc("GET /static/app.3f9a1c.js", func(w http.ResponseWriter, r *http.Request) {
		a.count("static")
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("ETag", "\"3f9a1c\"")
		fmt.Fprint(w, strings.Repeat("console.log('aset statis dengan sidik isi di nama file');\n", 60))
	})

	// Siapa yang menjawab: untuk melihat load balancing nginx. Tidak boleh di-cache.
	mux.HandleFunc("GET /api/siapa", func(w http.ResponseWriter, r *http.Request) {
		if a.sick.Load() {
			a.count("siapa_503")
			http.Error(w, "instance sakit", http.StatusServiceUnavailable)
			return
		}
		a.count("siapa")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"instance": a.Instance, "proto": r.Proto})
	})

	// Admin (hanya dari jaringan internal; nginx menolak /admin/).
	mux.HandleFunc("POST /admin/bump", func(w http.ResponseWriter, r *http.Request) {
		v := a.version.Add(1)
		a.Log.Info("versi katalog dinaikkan", "versi", v)
		_ = json.NewEncoder(w).Encode(map[string]int64{"versi": v})
	})
	mux.HandleFunc("POST /admin/sakit", func(w http.ResponseWriter, r *http.Request) {
		on := r.URL.Query().Get("on") == "1"
		a.sick.Store(on)
		a.Log.Info("mode sakit diubah", "sakit", on)
		_ = json.NewEncoder(w).Encode(map[string]any{"instance": a.Instance, "sakit": on})
	})
	mux.HandleFunc("GET /admin/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"instance": a.Instance, "versi": a.version.Load(), "sakit": a.sick.Load(), "hits": a.snapshot(),
		})
	})
	return a.middleware(mux)
}

// middleware memberi request_id, header pengenal instance dan protokol, lalu menulis log JSON.
func (a *App) middleware(next http.Handler) http.Handler {
	var seq atomic.Int64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-Id")
		if rid == "" {
			rid = fmt.Sprintf("%s-%d", a.Instance, seq.Add(1))
		}
		w.Header().Set("X-Request-Id", rid)
		w.Header().Set("X-Instance", a.Instance)
		w.Header().Set("X-Proto", r.Proto)
		if r.TLS != nil && r.ProtoMajor < 3 {
			// Iklan HTTP/3 (Bab 3: klien belajar dari Alt-Svc bahwa h3 tersedia).
			w.Header().Set("Alt-Svc", `h3=":8443"; ma=60`)
		}
		start := time.Now()
		next.ServeHTTP(w, r)
		lvl := slog.LevelInfo
		if strings.HasPrefix(r.URL.Path, "/asset/") || r.URL.Path == "/healthz" {
			lvl = slog.LevelDebug // aset waterfall sangat banyak; tidak dicatat secara default
		}
		a.Log.Log(r.Context(), lvl, "request", "request_id", rid, "method", r.Method,
			"path", r.URL.Path, "proto", r.Proto, "dur_ms", time.Since(start).Milliseconds())
	})
}
