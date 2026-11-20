// Package platform berisi bagian yang dipakai bersama oleh semua service toko daring:
// logger JSON, request id, koneksi Postgres dan NATS, outbox, consumer idempotent, retry.
package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"
)

// HeaderRequestID adalah header yang membawa request id dari APISIX sampai ke event NATS.
const HeaderRequestID = "X-Request-Id"

type ctxKey int

const keyRequestID ctxKey = 1

// Env membaca variabel lingkungan dengan nilai bawaan.
func Env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// EnvInt membaca variabel lingkungan bertipe int.
func EnvInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

// NewLogger membuat logger JSON satu baris dengan field service dan replica.
func NewLogger(service string) *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	l := slog.New(h).With("service", service)
	if r := os.Getenv("REPLICA"); r != "" {
		l = l.With("replica", r)
	}
	slog.SetDefault(l)
	return l
}

// NewID membuat id acak 128 bit berformat UUID v4.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// WithRequestID menyimpan request id di context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyRequestID, id)
}

// RequestID mengambil request id dari context (kosong bila tidak ada).
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(keyRequestID).(string)
	return v
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Middleware mengambil (atau membuat) request id, menaruhnya di context dan header
// respons, lalu menulis satu baris log JSON per request.
func Middleware(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if id == "" {
			id = NewID()
		}
		w.Header().Set(HeaderRequestID, id)
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		start := time.Now()
		next.ServeHTTP(rec, r.WithContext(WithRequestID(r.Context(), id)))
		if r.URL.Path == "/healthz" || r.URL.Path == "/breakers" {
			return // health check tidak perlu memenuhi log
		}
		log.Info("http request", "request_id", id, "method", r.Method, "path", r.URL.Path,
			"status", rec.status, "duration_ms", time.Since(start).Milliseconds())
	})
}

// WriteJSON menulis respons JSON.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ReadJSON membaca body JSON (maksimal 1 MB).
func ReadJSON(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

// Serve menjalankan server HTTP dengan timeout yang wajar.
func Serve(addr string, h http.Handler) *http.Server {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server berhenti", "addr", addr, "err", err)
			os.Exit(1)
		}
	}()
	return srv
}
