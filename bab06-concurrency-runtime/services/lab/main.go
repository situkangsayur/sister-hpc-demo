// Command lab adalah backend "Concurrency Lab" untuk Bab 6 (Concurrency dan Runtime, slide P06).
// Setiap eksperimen dijalankan dari UI; hasilnya dikirim sebagai JSON atau dialirkan lewat SSE.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var logger = slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "lab")

func main() {
	addr := envOr("LAB_ADDR", ":8080")
	calibrateCPU()

	mux := http.NewServeMux()
	routes(mux)

	srv := &http.Server{
		Addr:              addr,
		Handler:           withRequestID(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info("lab siap", "addr", addr, "cpu_iters_per_ms", cpuItersPerMs.Load())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server berhenti", "err", err)
		os.Exit(1)
	}
}

func routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", handleHealth)

	// 1. Race condition dan "satu soal, banyak model"
	mux.HandleFunc("POST /api/race", handleRace)
	mux.HandleFunc("POST /api/race/detector", handleRaceDetector)

	// 2. Deadlock dan wait-for graph
	mux.HandleFunc("GET /api/deadlock/stream", handleDeadlockStream)

	// 3. Worker pool: throughput dan latency vs jumlah worker
	mux.HandleFunc("POST /api/pool/sweep", handlePoolSweep)

	// 4. Pipeline CSP: fan-out/fan-in dan backpressure
	mux.HandleFunc("GET /api/pipeline/stream", handlePipelineStream)

	// 5. Biaya goroutine vs thread OS, metrik runtime, GC
	mux.HandleFunc("POST /api/spawn", handleSpawn)
	mux.HandleFunc("POST /api/spawn/release", handleSpawnRelease)
	mux.HandleFunc("POST /api/gc/churn", handleGCChurn)
	mux.HandleFunc("GET /api/metrics", handleMetrics)
	mux.HandleFunc("GET /api/metrics/stream", handleMetricsStream)

	// 6. Actor model dan event loop
	mux.HandleFunc("GET /api/actor/stream", handleActorStream)
	mux.HandleFunc("GET /api/eventloop/stream", handleEventLoopStream)

	// Kendali live untuk stream yang sedang berjalan (pipeline, actor, event loop)
	mux.HandleFunc("POST /api/session/{id}", handleSessionSet)
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "lab", "metrics": snapshotMetrics()})
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
