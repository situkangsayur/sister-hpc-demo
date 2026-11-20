// Package httpx berisi helper HTTP kecil yang dipakai semua service bab 7.
package httpx

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// JSON menulis v sebagai JSON dengan status code tertentu.
func JSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Err menulis pesan error dalam bentuk JSON {"error": "..."}.
func Err(w http.ResponseWriter, code int, msg string) {
	JSON(w, code, map[string]string{"error": msg})
}

// Decode membaca body JSON ke v; body kosong dianggap valid.
func Decode(r *http.Request, v any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	return json.NewDecoder(r.Body).Decode(v)
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }

// Log membungkus handler: memberi request_id dan mencatat setiap request selain polling GET.
func Log(l *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			b := make([]byte, 6)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-Id", id)
		sw := &statusWriter{ResponseWriter: w, code: 200}
		start := time.Now()
		next.ServeHTTP(sw, r)
		// GET dipakai UI untuk polling tiap detik; hanya dicatat bila gagal agar log tidak banjir.
		if r.Method != http.MethodGet || sw.code >= 400 {
			l.Info("request", "request_id", id, "method", r.Method, "path", r.URL.Path,
				"status", sw.code, "ms", time.Since(start).Milliseconds())
		}
	})
}

// Env membaca variabel lingkungan dengan nilai bawaan.
func Env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
