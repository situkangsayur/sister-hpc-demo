// Service katalog Bab 4: satu domain (produk, penjual, saldo) di Postgres,
// dilayani lewat setiap protokol yang dibahas di bab ini.
//
//	HTTP :8080   REST JSON/XML  /v1/...        OpenAPI + Swagger UI  /docs/
//	             SOAP 1.1       POST /soap      WSDL                  GET /soap?wsdl
//	             GraphQL        POST /graphql   GraphiQL              /docs/graphiql.html
//	             SSE            /v1/harga/stream, long polling /v1/harga/tunggu
//	             WebSocket      /v1/chat
//	gRPC :50051  katalog.v1.KatalogService (unary + 3 mode streaming), health, reflection
//
// Variabel: DATABASE_URL (wajib), NODE_NAME, HTTP_ADDR, GRPC_ADDR.
package main

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	pb "sister/bab04/gen/katalogv1"
	"sister/bab04/internal/gangguan"
	"sister/bab04/internal/gql"
	"sister/bab04/internal/grpcsvc"
	"sister/bab04/internal/logx"
	"sister/bab04/internal/realtime"
	"sister/bab04/internal/rest"
	"sister/bab04/internal/soap"
	"sister/bab04/internal/toko"
)

//go:embed docs
var docsFS embed.FS

func env(nama, bawaan string) string {
	if v := os.Getenv(nama); v != "" {
		return v
	}
	return bawaan
}

func main() {
	host, _ := os.Hostname()
	node := env("NODE_NAME", host)
	logx.Siapkan("katalog", node)

	ctx, henti := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer henti()

	url := os.Getenv("DATABASE_URL")
	t, err := toko.Buka(ctx, url)
	if err != nil {
		slog.Error("membuka database", "err", err)
		os.Exit(1)
	}
	defer t.Tutup()
	g := &gangguan.Penjaga{DB: t.DB()}
	hub := realtime.BaruHub(ctx, url, node, t)

	mux := http.NewServeMux()
	(&rest.Server{Toko: t, Gangguan: g, Node: node}).Daftarkan(mux)
	(&soap.Server{Toko: t}).Daftarkan(mux)
	hub.Daftarkan(mux)
	skema, err := gql.Skema(t)
	if err != nil {
		slog.Error("skema graphql", "err", err)
		os.Exit(1)
	}
	mux.Handle("POST /graphql", gql.Handler(skema, t, node))
	sub, _ := fs.Sub(docsFS, "docs")
	mux.Handle("GET /docs/", http.StripPrefix("/docs/", http.FileServerFS(sub)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		c, b := context.WithTimeout(r.Context(), 2*time.Second)
		defer b()
		if err := t.Ping(c); err != nil {
			http.Error(w, "db: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /v1/info", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(rest.KodeJSON(map[string]any{"node": node, "koneksi_realtime": hub.JumlahPelanggan()}))
	})

	tandai := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Node", node)
			h.ServeHTTP(w, r)
		})
	}
	httpSrv := &http.Server{Addr: env("HTTP_ADDR", ":8080"), Handler: logx.HTTP(tandai(mux)), ReadHeaderTimeout: 5 * time.Second}

	lis, err := net.Listen("tcp", env("GRPC_ADDR", ":50051"))
	if err != nil {
		slog.Error("listen grpc", "err", err)
		os.Exit(1)
	}
	grpcSrv := grpc.NewServer(grpc.UnaryInterceptor(grpcsvc.Unary(node)), grpc.StreamInterceptor(grpcsvc.Stream(node)))
	pb.RegisterKatalogServiceServer(grpcSrv, &grpcsvc.Server{Toko: t, Gangguan: g, Hub: hub, Node: node})
	healthpb.RegisterHealthServer(grpcSrv, health.NewServer())
	reflection.Register(grpcSrv)

	gagal := make(chan error, 2)
	go func() { gagal <- httpSrv.ListenAndServe() }()
	go func() { gagal <- grpcSrv.Serve(lis) }()
	slog.Info("katalog siap", "http", httpSrv.Addr, "grpc", lis.Addr().String())

	select {
	case err := <-gagal:
		slog.Error("server berhenti", "err", err)
	case <-ctx.Done():
	}
	c, b := context.WithTimeout(context.Background(), 5*time.Second)
	defer b()
	httpSrv.Shutdown(c)
	// stream gRPC (PantauHarga) bisa berjalan selamanya: beri batas, lalu paksa berhenti
	selesai := make(chan struct{})
	go func() { grpcSrv.GracefulStop(); close(selesai) }()
	select {
	case <-selesai:
	case <-c.Done():
		grpcSrv.Stop()
	}
}
