// origin adalah server aplikasi Bab 3. Satu proses membuka empat pintu:
//
//	:8080      HTTP/1.1 polos (backend nginx, dan contoh "tanpa TLS")
//	:8443/tcp  HTTPS dengan ALPN h2 dan http/1.1
//	:8443/udp  HTTP/3 di atas QUIC (quic-go)
//	:9443/tcp  HTTPS dengan mTLS: klien wajib membawa certificate dari CA demo
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/quic-go/quic-go/http3"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	instance := env("INSTANCE", "origin")
	level := slog.LevelInfo
	if os.Getenv("LOG_DEBUG") == "1" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})).
		With("service", "origin", "instance", instance)
	certDir := env("CERT_DIR", "/certs")

	app := NewApp(instance, log)
	h := app.Routes()

	cert, err := tls.LoadX509KeyPair(certDir+"/server.pem", certDir+"/server-key.pem")
	if err != nil {
		log.Error("gagal memuat certificate", "err", err)
		os.Exit(1)
	}
	caPEM, err := os.ReadFile(certDir + "/ca.pem")
	if err != nil {
		log.Error("gagal membaca ca.pem", "err", err)
		os.Exit(1)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	inter, err := os.ReadFile(certDir + "/intermediate.pem")
	if err == nil {
		pool.AppendCertsFromPEM(inter)
	}

	plain := &http.Server{Addr: ":8080", Handler: h, ReadHeaderTimeout: 5 * time.Second}

	tlsSrv := &http.Server{
		Addr:              ":8443",
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12, // 1.2 tetap dibuka agar bisa dibandingkan dengan 1.3
			NextProtos:   []string{"h2", "http/1.1"},
		},
	}

	h3Srv := &http3.Server{
		Addr:    ":8443",
		Handler: h,
		TLSConfig: http3.ConfigureTLSConfig(&tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS13, // QUIC selalu TLS 1.3
		}),
	}

	mtlsSrv := &http.Server{
		Addr:              ":9443",
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS13,
			ClientAuth:   tls.RequireAndVerifyClientCert, // klien wajib bersertifikat
			ClientCAs:    pool,                           // yang diterbitkan CA demo
		},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nama := r.TLS.PeerCertificates[0].Subject.CommonName
			log.Info("mTLS diterima", "client_cn", nama)
			fmt.Fprintf(w, "halo %s, identitas Anda terbukti lewat certificate\n", nama)
		}),
		// Handshake yang ditolak dicatat agar terlihat di Grafana.
		ErrorLog: slog.NewLogLogger(log.With("listener", "mtls").Handler(), slog.LevelWarn),
	}

	errc := make(chan error, 4)
	go func() { errc <- plain.ListenAndServe() }()
	go func() { errc <- tlsSrv.ListenAndServeTLS("", "") }()
	go func() { errc <- h3Srv.ListenAndServe() }()
	go func() { errc <- mtlsSrv.ListenAndServeTLS("", "") }()
	log.Info("origin siap", "http", ":8080", "https", ":8443 tcp+udp", "mtls", ":9443")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("listener berhenti", "err", err)
			os.Exit(1)
		}
	case <-sig:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = plain.Shutdown(ctx)
	_ = tlsSrv.Shutdown(ctx)
	_ = mtlsSrv.Shutdown(ctx)
	_ = h3Srv.Close()
}
