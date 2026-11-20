// Package httpx berisi helper kecil HTTP JSON yang dipakai semua service.
package httpx

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// JSON menulis v sebagai JSON dengan status code tertentu.
func JSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Error menulis {"error": msg}.
func Error(w http.ResponseWriter, code int, msg string) {
	JSON(w, code, map[string]string{"error": msg})
}

// Decode membaca body JSON (maks 64 KB) ke v.
func Decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 64<<10)
	return json.NewDecoder(r.Body).Decode(v)
}

// NewID membuat request_id acak pendek.
func NewID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }

// Hijack diteruskan agar upgrade WebSocket tetap bekerja di balik middleware log.
func (s *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("ResponseWriter tidak mendukung Hijack")
	}
	s.code = http.StatusSwitchingProtocols
	return h.Hijack()
}

// Unwrap untuk http.ResponseController.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Logging mencatat setiap request sebagai satu baris JSON (kecuali /healthz).
func Logging(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = NewID()
		}
		w.Header().Set("X-Request-Id", id)
		sw := &statusWriter{ResponseWriter: w, code: 200}
		t0 := time.Now()
		next.ServeHTTP(sw, r)
		log.Info("http", "request_id", id, "method", r.Method, "path", r.URL.Path,
			"status", sw.code, "dur_ms", float64(time.Since(t0).Microseconds())/1000)
	})
}
