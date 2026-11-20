// katalog: aplikasi Go kecil yang di-deploy ke k3s dengan beberapa replica.
// Sengaja butuh waktu "pemanasan" (STARTUP_DELAY_MS) sebelum membuka port, supaya peran
// readinessProbe terlihat: tanpa probe, Service mengirim request ke pod yang belum siap.
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/situkangsayur/sister-hpc-demo/bab07/services/internal/httpx"
	"github.com/situkangsayur/sister-hpc-demo/bab07/services/internal/logx"
)

func main() {
	log := logx.New("katalog")
	pod := httpx.Env("POD_NAME", "lokal")
	version := httpx.Env("VERSION", "v1")
	delay, _ := strconv.Atoi(httpx.Env("STARTUP_DELAY_MS", "3000"))
	log.Info("pemanasan", "pod", pod, "version", version, "delay_ms", delay)
	time.Sleep(time.Duration(delay) * time.Millisecond)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /item/{id}", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, 200, map[string]string{"pod": pod, "version": version, "id": r.PathValue("id"), "nama": "Kopi Gayo 250 g"})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	srv := &http.Server{Addr: ":" + httpx.Env("PORT", "8080"), Handler: mux}
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
		<-ch
		// Tetap melayani sebentar: endpoint Service perlu waktu untuk mengeluarkan pod ini.
		log.Info("SIGTERM diterima, graceful shutdown", "pod", pod)
		time.Sleep(2 * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	log.Info("siap", "pod", pod, "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("berhenti", "err", err.Error())
	}
}
