// Command demo-target adalah server contoh lokal untuk mencoba perftest tanpa menyalakan demo
// bab mana pun: HTTP (latency acak dan sebagian error 5xx), GraphQL sederhana, gRPC (health +
// server reflection), dan WebSocket echo. Port default 50098 (HTTP) dan 50099 (gRPC), bagian dari
// rentang shared 50000-50099 yang tidak dipakai container observability.
package main

import (
	"encoding/json"
	"flag"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

func main() {
	httpAddr := flag.String("http", "127.0.0.1:50098", "alamat HTTP/GraphQL/WebSocket")
	grpcAddr := flag.String("grpc", "127.0.0.1:50099", "alamat gRPC")
	errPct := flag.Float64("error-rate", 0.02, "rasio respons 503 pada /api/lambat")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "demo-target")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/cepat", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	// latency log-normal kira-kira median 20 ms, ekor panjang; sebagian 503
	mux.HandleFunc("/api/lambat", func(w http.ResponseWriter, r *http.Request) {
		d := time.Duration(20*rand.ExpFloat64()*float64(time.Millisecond)) + 5*time.Millisecond
		time.Sleep(d)
		if rand.Float64() < *errPct {
			http.Error(w, "sibuk", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("POST /graphql", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		w.Header().Set("Content-Type", "application/json")
		if !strings.Contains(in.Query, "halo") {
			_, _ = w.Write([]byte(`{"errors":[{"message":"hanya field halo yang dikenal"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"halo":"dunia"}}`))
	})
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			typ, b, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if err := c.Write(r.Context(), typ, b); err != nil {
				return
			}
		}
	})

	l, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		log.Error("listen grpc", "err", err)
		os.Exit(1)
	}
	gs := grpc.NewServer()
	hs := health.NewServer()
	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(gs, hs)
	reflection.Register(gs)
	go func() { _ = gs.Serve(l) }()

	log.Info("demo-target siap", "http", *httpAddr, "grpc", *grpcAddr)
	srv := &http.Server{Addr: *httpAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("http", "err", err)
		os.Exit(1)
	}
}
