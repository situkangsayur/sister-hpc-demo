// Paket logx menyiapkan log JSON satu baris (log/slog) sesuai KONVENSI repo.
package logx

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"
)

type kunciRID struct{}

// Siapkan memasang slog JSON sebagai logger bawaan dengan field service dan node.
func Siapkan(service, node string) *slog.Logger {
	l := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", service, "node", node)
	slog.SetDefault(l)
	return l
}

// RequestID mengambil request_id dari context.
func RequestID(ctx context.Context) string {
	s, _ := ctx.Value(kunciRID{}).(string)
	return s
}

// DenganRequestID menaruh request_id ke context.
func DenganRequestID(ctx context.Context, rid string) context.Context {
	return context.WithValue(ctx, kunciRID{}, rid)
}

type perekam struct {
	http.ResponseWriter
	kode   int
	ukuran int
}

func (p *perekam) WriteHeader(k int) { p.kode = k; p.ResponseWriter.WriteHeader(k) }
func (p *perekam) Write(b []byte) (int, error) {
	n, err := p.ResponseWriter.Write(b)
	p.ukuran += n
	return n, err
}

// Flush diteruskan agar SSE tetap jalan di balik middleware.
func (p *perekam) Flush() {
	if f, ok := p.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack diteruskan agar upgrade WebSocket tetap bisa di balik middleware.
func (p *perekam) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := p.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack tidak didukung")
	}
	p.kode = http.StatusSwitchingProtocols
	return h.Hijack()
}

// Unwrap dipakai http.ResponseController (mis. WebSocket hijack).
func (p *perekam) Unwrap() http.ResponseWriter { return p.ResponseWriter }

// HTTP mencatat setiap request: metode, path, status, ukuran, durasi, request_id
// (dari header X-Request-Id yang dipasang APISIX).
func HTTP(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mulai := time.Now()
		rid := r.Header.Get("X-Request-Id")
		ctx := DenganRequestID(r.Context(), rid)
		p := &perekam{ResponseWriter: w, kode: 200}
		h.ServeHTTP(p, r.WithContext(ctx))
		if r.URL.Path == "/healthz" {
			return
		}
		slog.InfoContext(ctx, "http", "method", r.Method, "path", r.URL.Path, "status", p.kode,
			"bytes", p.ukuran, "durasi_ms", float64(time.Since(mulai).Microseconds())/1000, "request_id", rid)
	})
}
