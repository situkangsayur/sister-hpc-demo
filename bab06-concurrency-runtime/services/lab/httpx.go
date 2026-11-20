package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type ctxKey string

const requestIDKey ctxKey = "request_id"

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// withRequestID memberi setiap request sebuah request_id dan menulis satu baris log JSON.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = newID()
		}
		w.Header().Set("X-Request-Id", id)
		start := time.Now()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
		if r.URL.Path != "/api/health" {
			logger.Info("request", "request_id", id, "method", r.Method, "path", r.URL.Path,
				"query", r.URL.RawQuery, "dur_ms", time.Since(start).Milliseconds())
		}
	})
}

func reqID(r *http.Request) string {
	v, _ := r.Context().Value(requestIDKey).(string)
	return v
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// qInt membaca parameter query integer dengan nilai bawaan dan batas bawah/atas.
func qInt(r *http.Request, key string, def, lo, hi int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		v = def
	}
	return clamp(v, lo, hi)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// sseWriter mengirim Server-Sent Events. Setiap event langsung di-flush.
type sseWriter struct {
	w  http.ResponseWriter
	fl http.Flusher
	mu sync.Mutex
}

func newSSE(w http.ResponseWriter) (*sseWriter, error) {
	fl, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming tidak didukung")
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl.Flush()
	return &sseWriter{w: w, fl: fl}, nil
}

func (s *sseWriter) send(event string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, b); err != nil {
		return err
	}
	s.fl.Flush()
	return nil
}

// Sesi: stream yang berjalan lama (pipeline, actor, event loop) mendaftarkan "knob" yang
// bisa diubah dari UI selagi stream berjalan, lewat POST /api/session/{id}?knob=nilai.
type session struct {
	knobs map[string]*knob
	// actions dipanggil untuk knob bertipe aksi (mis. "inject", "poison").
	actions map[string]func(int64)
}

type knob struct {
	v      atomic.Int64
	lo, hi int64
}

func (k *knob) get() int64 { return k.v.Load() }

var sessions sync.Map // id -> *session

func newSession() (string, *session) {
	id := newID()
	s := &session{knobs: map[string]*knob{}, actions: map[string]func(int64){}}
	sessions.Store(id, s)
	return id, s
}

func (s *session) knob(name string, init, lo, hi int64) *knob {
	k := &knob{lo: lo, hi: hi}
	k.v.Store(init)
	s.knobs[name] = k
	return k
}

func handleSessionSet(w http.ResponseWriter, r *http.Request) {
	v, ok := sessions.Load(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "sesi tidak ditemukan atau sudah selesai")
		return
	}
	s := v.(*session)
	applied := map[string]int64{}
	for name, vals := range r.URL.Query() {
		n, err := strconv.ParseInt(vals[0], 10, 64)
		if err != nil {
			continue
		}
		if k, ok := s.knobs[name]; ok {
			n = max(k.lo, min(k.hi, n))
			k.v.Store(n)
			applied[name] = n
		} else if fn, ok := s.actions[name]; ok {
			fn(n)
			applied[name] = n
		}
	}
	if len(applied) == 0 {
		writeErr(w, http.StatusBadRequest, "tidak ada knob yang dikenali")
		return
	}
	logger.Info("knob diubah", "request_id", reqID(r), "session", r.PathValue("id"), "applied", applied)
	writeJSON(w, http.StatusOK, map[string]any{"applied": applied})
}

// expMu memastikan eksperimen berat (race, pool, spawn) tidak berjalan bersamaan,
// karena GOMAXPROCS dan jumlah goroutine bersifat global untuk satu proses.
var expMu sync.Mutex

func lockExperiment(w http.ResponseWriter) bool {
	if !expMu.TryLock() {
		writeErr(w, http.StatusConflict, "eksperimen lain sedang berjalan, coba lagi sebentar")
		return false
	}
	return true
}
