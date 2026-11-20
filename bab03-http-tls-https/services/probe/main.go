// probe menjalankan semua pengukuran Bab 3 dari sisi server, lalu UI hanya menampilkan hasilnya.
// Dengan begitu browser tidak pernah berbicara langsung dengan certificate self-signed demo.
//
// Endpoint utama:
//
//	GET  /api/compare   waterfall h1 vs h2 vs h3 (median dari beberapa run)
//	GET  /api/netem     POST /api/netem  delay/loss buatan
//	GET  /api/tls       rantai certificate, versi TLS, cipher suite, ALPN
//	GET  /api/mtls      tiga percobaan mTLS
//	GET  /api/sniff     apa yang terlihat penyadap: HTTP polos vs HTTPS
//	GET  /api/cache     satu request lewat nginx (cache, ETag, gzip)
//	POST /api/cache/bump, GET /api/hsts, GET /api/lb, POST /api/lb/sakit
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// Server menyatukan konfigurasi dan dependensi probe.
type Server struct {
	log       *slog.Logger
	roots     *x509.CertPool
	clientCrt *tls.Certificate
	originTLS string // https://origin-a:8443
	originH   string // origin-a (host untuk SNI)
	originRaw string // http://origin-a:8080
	mtlsURL   string
	nginxAddr string
	front     *Front
	netem     *Netem
	measure   sync.Mutex // satu pengukuran pada satu waktu agar hasil tidak saling mengganggu
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func qInt(r *http.Request, k string, def, lo, hi int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(k))
	if err != nil {
		return def
	}
	return max(lo, min(hi, v))
}

// ProtoResult adalah ringkasan satu protokol pada /api/compare.
type ProtoResult struct {
	Proto      string    `json:"proto"`
	MedianMs   float64   `json:"median_ms"`
	TotalsMs   []float64 `json:"totals_ms"`
	ModelRTT   int       `json:"model_rtt"`
	ModelMs    float64   `json:"model_ms"`
	Conns      int       `json:"conns"`
	TLSVersion string    `json:"tls_version"`
	ALPN       string    `json:"alpn"`
	Sample     Load      `json:"sample"`
}

func (s *Server) compare(w http.ResponseWriter, r *http.Request) {
	n := qInt(r, "n", 30, 1, 120)
	think := qInt(r, "think", 0, 0, 3000)
	runs := qInt(r, "runs", 5, 1, 15)
	protos := strings.Split(env2(r.URL.Query().Get("protos"), "h1,h2,h3"), ",")
	s.measure.Lock()
	defer s.measure.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	loads := map[string][]Load{}
	for i := 0; i < runs; i++ {
		for _, p := range protos { // diselang-seling agar gangguan sesaat terbagi rata
			loads[p] = append(loads[p], LoadPage(ctx, p, s.originTLS, s.roots, n, think))
		}
	}
	delay := s.netem.Snapshot()["delay_ms"].(int)
	var out []ProtoResult
	for _, p := range protos {
		ls := loads[p]
		pr := ProtoResult{Proto: p, ModelRTT: ModelRTT(p, n)}
		pr.ModelMs = float64(pr.ModelRTT * delay)
		for _, l := range ls {
			pr.TotalsMs = append(pr.TotalsMs, l.TotalMs)
		}
		pr.MedianMs = Median(pr.TotalsMs)
		// Sampel waterfall: run yang total waktunya paling dekat dengan median.
		best := 0
		for i, l := range ls {
			if abs(l.TotalMs-pr.MedianMs) < abs(ls[best].TotalMs-pr.MedianMs) {
				best = i
			}
		}
		pr.Sample = ls[best]
		pr.Conns, pr.TLSVersion, pr.ALPN = pr.Sample.Conns, pr.Sample.TLSVersion, pr.Sample.ALPN
		out = append(out, pr)
		s.log.Info("pengukuran selesai", "request_id", rid(r), "proto", p, "assets", n, "median_ms", pr.MedianMs, "conns", pr.Conns)
	}
	writeJSON(w, 200, map[string]any{"assets": n, "think_ms": think, "runs": runs, "netem": s.netem.Snapshot(), "results": out})
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func env2(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func (s *Server) netemHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			DelayMs int     `json:"delay_ms"`
			LossPct float64 `json:"loss_pct"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, 400, map[string]string{"err": err.Error()})
			return
		}
		body.DelayMs = max(0, min(500, body.DelayMs))
		body.LossPct = max(0, min(30, body.LossPct))
		if err := s.netem.Set(body.DelayMs, body.LossPct); err != nil {
			writeJSON(w, 500, s.netem.Snapshot())
			return
		}
		s.log.Info("netem diubah", "request_id", rid(r), "delay_ms", body.DelayMs, "loss_pct", body.LossPct)
	}
	writeJSON(w, 200, s.netem.Snapshot())
}

func (s *Server) tlsHandler(w http.ResponseWriter, r *http.Request) {
	ver, err := tlsVersion(r.URL.Query().Get("max"))
	if err != nil {
		writeJSON(w, 400, map[string]string{"err": err.Error()})
		return
	}
	addr, sni := s.originH+":8443", s.originH
	target := "origin (Go crypto/tls)"
	if r.URL.Query().Get("target") == "nginx" {
		addr, sni, target = s.nginxAddr, strings.Split(s.nginxAddr, ":")[0], "nginx (OpenSSL)"
	}
	writeJSON(w, 200, InspectTLS(r.Context(), target, addr, sni, ver, s.roots))
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	mux.HandleFunc("GET /api/compare", s.compare)
	mux.HandleFunc("/api/netem", s.netemHandler)
	mux.HandleFunc("GET /api/tls", s.tlsHandler)
	mux.HandleFunc("GET /api/mtls", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"url": s.mtlsURL, "attempts": TryMTLS(r.Context(), s.mtlsURL, s.roots, s.clientCrt)})
	})
	mux.HandleFunc("GET /api/sniff", func(w http.ResponseWriter, r *http.Request) {
		mode := r.URL.Query().Get("mode")
		u := s.originRaw + "/login"
		if mode != "http" {
			mode, u = "https", s.originTLS+"/login"
		}
		writeJSON(w, 200, Sniff(r.Context(), mode, u, s.roots))
	})
	mux.HandleFunc("GET /api/cache", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path != "/static/app.3f9a1c.js" {
			path = "/api/katalog"
		}
		res := s.front.Get(r.Context(), path, r.URL.Query().Get("inm"), r.URL.Query().Get("gzip") == "1")
		s.log.Info("cache request", "request_id", rid(r), "path", path, "status", res.Status,
			"cache_status", res.Headers["X-Cache-Status"], "origin_delta", res.OriginDelta)
		writeJSON(w, 200, res)
	})
	mux.HandleFunc("POST /api/cache/bump", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"origins": s.front.Bump(r.Context())})
	})
	mux.HandleFunc("GET /api/hsts", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.front.HSTS(r.Context()))
	})
	mux.HandleFunc("GET /api/lb", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.front.LB(r.Context(), qInt(r, "n", 12, 1, 50)))
	})
	mux.HandleFunc("GET /api/lb/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"stats": s.front.Stats(r.Context())})
	})
	mux.HandleFunc("POST /api/lb/sakit", func(w http.ResponseWriter, r *http.Request) {
		res, err := s.front.SetSick(r.Context(), r.URL.Query().Get("instance"), r.URL.Query().Get("on") == "1")
		if err != nil {
			writeJSON(w, 400, map[string]string{"err": err.Error()})
			return
		}
		s.log.Info("mode sakit", "request_id", rid(r), "instance", r.URL.Query().Get("instance"), "on", r.URL.Query().Get("on"))
		writeJSON(w, 200, map[string]any{"result": res})
	})
	return s.middleware(mux)
}

type ridKey struct{}

func rid(r *http.Request) string {
	v, _ := r.Context().Value(ridKey{}).(string)
	return v
}

func (s *Server) middleware(next http.Handler) http.Handler {
	var seq atomic.Int64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = fmt.Sprintf("probe-%d", seq.Add(1))
		}
		w.Header().Set("X-Request-Id", id)
		r = r.WithContext(context.WithValue(r.Context(), ridKey{}, id))
		t0 := time.Now()
		next.ServeHTTP(w, r)
		if r.URL.Path != "/healthz" {
			s.log.Info("request", "request_id", id, "method", r.Method, "path", r.URL.Path,
				"dur_ms", time.Since(t0).Milliseconds())
		}
	})
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "probe")
	certDir := env("CERT_DIR", "/certs")
	caPEM, err := os.ReadFile(certDir + "/ca.pem")
	if err != nil {
		log.Error("gagal membaca ca.pem", "err", err)
		os.Exit(1)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	var clientCrt *tls.Certificate
	if c, err := tls.LoadX509KeyPair(certDir+"/client.pem", certDir+"/client-key.pem"); err == nil {
		clientCrt = &c
	}
	originHost := env("ORIGIN_HOST", "origin-a")
	nginxHost := env("NGINX_HOST", "nginx")
	origins := strings.Split(env("ORIGINS", "http://origin-a:8080,http://origin-b:8080"), ",")
	s := &Server{
		log: log, roots: roots, clientCrt: clientCrt,
		originTLS: "https://" + originHost + ":8443", originH: originHost,
		originRaw: "http://" + originHost + ":8080", mtlsURL: "https://" + originHost + ":9443/",
		nginxAddr: nginxHost + ":443",
		front:     NewFront("https://"+nginxHost, "http://"+nginxHost, origins, roots),
		netem:     &Netem{Target: originHost},
	}
	delay, _ := strconv.Atoi(env("NETEM_DELAY_MS", "0"))
	loss, _ := strconv.ParseFloat(env("NETEM_LOSS_PCT", "0"), 64)
	// Origin mungkin belum siap saat probe hidup; coba beberapa kali.
	for i := 0; i < 20; i++ {
		if err = s.netem.Set(delay, loss); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		log.Warn("netem tidak terpasang; pengukuran tetap jalan tanpa delay buatan", "err", err)
	} else {
		log.Info("netem terpasang", "dev", s.netem.Dev, "delay_ms", delay, "loss_pct", loss)
	}
	addr := env("LISTEN", ":8090")
	log.Info("probe siap", "addr", addr)
	srv := &http.Server{Addr: addr, Handler: s.routes(), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("server berhenti", "err", err)
		os.Exit(1)
	}
}
