// Service klien Bab 4: "pemanggil" yang dipakai UI. Ia menjalankan permintaan
// yang sama lewat REST JSON, REST XML, SOAP, GraphQL, gRPC, dan gRPC-Web
// (lewat APISIX atau langsung), lalu mengembalikan payload mentah, ukuran byte,
// dan latency. Ia juga memperagakan panggilan andal: timeout, retry dengan
// exponential backoff + jitter, dan idempotency key.
//
// Pengukuran dilakukan di sini (di dalam jaringan Docker), bukan di browser,
// agar semua protokol diukur dari tempat yang sama.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "sister/bab04/gen/katalogv1"
	"sister/bab04/internal/logx"
)

func env(nama, bawaan string) string {
	if v := os.Getenv(nama); v != "" {
		return v
	}
	return bawaan
}

// Klien menyimpan koneksi ke gateway dan ke replica langsung.
type Klien struct {
	http         *http.Client
	gwHTTP       string
	langsungHTTP string
	gwGRPC       pb.KatalogServiceClient
	langsungGRPC pb.KatalogServiceClient
	nodes        []string
	apiKey       string
}

func sambungGRPC(target string) pb.KatalogServiceClient {
	c, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		slog.Error("grpc client", "target", target, "err", err)
		os.Exit(1)
	}
	return pb.NewKatalogServiceClient(c)
}

func main() {
	logx.Siapkan("klien", env("NODE_NAME", "klien"))
	k := &Klien{
		http:         &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 32, IdleConnTimeout: 90 * time.Second}},
		gwHTTP:       env("GATEWAY_HTTP", "http://apisix:9080"),
		langsungHTTP: env("LANGSUNG_HTTP", "http://katalog-1:8080"),
		gwGRPC:       sambungGRPC(env("GATEWAY_GRPC", "apisix:9080")),
		langsungGRPC: sambungGRPC(env("LANGSUNG_GRPC", "katalog-1:50051")),
		nodes:        strings.Split(env("KATALOG_NODES", "http://katalog-1:8080,http://katalog-2:8080"), ","),
		apiKey:       env("APISIX_DEMO_KEY", "changeme-demo-key"),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /bandingkan", k.bandingkan)
	mux.HandleFunc("POST /andal", k.andal)
	mux.HandleFunc("GET /gangguan", k.ambilGangguan)
	mux.HandleFunc("PUT /gangguan", k.setGangguan)
	mux.HandleFunc("GET /saldo", k.saldo)
	mux.HandleFunc("POST /saldo/reset", k.resetSaldo)
	mux.HandleFunc("POST /gateway/kunci", k.ujiKunci)
	mux.HandleFunc("POST /gateway/roundrobin", k.roundRobin)
	mux.HandleFunc("POST /nplus1", k.nPlus1)
	mux.HandleFunc("POST /harga", k.ubahHarga)
	mux.HandleFunc("PUT /ticker", k.ticker)
	mux.HandleFunc("GET /ticker", k.ticker)
	mux.HandleFunc("GET /grpc/pantau", k.pantauGRPC)
	mux.HandleFunc("POST /grpc/mode", k.modeGRPC)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })

	srv := &http.Server{Addr: env("HTTP_ADDR", ":8080"), Handler: logx.HTTP(mux), ReadHeaderTimeout: 5 * time.Second}
	ctx, henti := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer henti()
	go func() {
		slog.Info("klien siap", "addr", srv.Addr, "gateway", k.gwHTTP)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	c, b := context.WithTimeout(context.Background(), 3*time.Second)
	defer b()
	srv.Shutdown(c)
}

func tulisJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func bacaJSON(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<16)).Decode(v)
}

func msSejak(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000 }
