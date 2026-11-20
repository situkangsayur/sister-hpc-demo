// Layanan "app": service Go kecil yang dibangun Jenkins dan di-deploy Argo CD ke k3s.
// Versi (tag image) disuntikkan saat build lewat -ldflags "-X main.versi=...".
package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

var versi = "dev"

// PanjangMaksPesan adalah batas panjang pesan yang lolos validasi.
const PanjangMaksPesan = 120

// Jawaban adalah isi respons GET /.
type Jawaban struct {
	Versi string `json:"versi"`
	Pesan string `json:"pesan"`
	Pod   string `json:"pod"`
	Waktu string `json:"waktu"`
}

// validasiPesan menolak pesan kosong atau terlalu panjang. Unit test memanggilnya untuk
// konstanta Pesan, sehingga commit yang mengosongkan Pesan gagal di tahap "Unit test".
func validasiPesan(p string) error {
	if strings.TrimSpace(p) == "" {
		return errors.New("pesan tidak boleh kosong")
	}
	if utf8.RuneCountInString(p) > PanjangMaksPesan {
		return errors.New("pesan lebih dari 120 karakter")
	}
	return nil
}

func handler(pod string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Jawaban{
			Versi: versi, Pesan: Pesan, Pod: pod, Waktu: time.Now().UTC().Format(time.RFC3339),
		})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "app", "versi", versi)
	pod, _ := os.Hostname()
	srv := &http.Server{Addr: ":8080", Handler: handler(pod), ReadHeaderTimeout: 5 * time.Second}
	log.Info("app mulai", "addr", srv.Addr, "pod", pod)
	if err := srv.ListenAndServe(); err != nil {
		log.Error("server berhenti", "err", err)
		os.Exit(1)
	}
}
