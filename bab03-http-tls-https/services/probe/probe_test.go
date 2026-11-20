package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
)

func TestParseRouteDev(t *testing.T) {
	out := "172.20.0.3 dev eth1 src 172.20.0.5 uid 0 \\    cache "
	if d := ParseRouteDev(out); d != "eth1" {
		t.Fatalf("dev = %q", d)
	}
	if d := ParseRouteDev("tanpa interface"); d != "" {
		t.Fatalf("dev = %q, mau kosong", d)
	}
}

func TestMedianDanModel(t *testing.T) {
	if m := Median([]float64{5, 1, 3}); m != 3 {
		t.Fatalf("median ganjil %v", m)
	}
	if m := Median([]float64{4, 1, 3, 2}); m != 2.5 {
		t.Fatalf("median genap %v", m)
	}
	// Angka contoh buku: 30 aset -> h1 10 RTT, h2 4 RTT, h3 3 RTT.
	if ModelRTT("h1", 30) != 10 || ModelRTT("h2", 30) != 4 || ModelRTT("h3", 30) != 3 {
		t.Fatal("model RTT tidak sesuai contoh buku")
	}
}

// captureClientHello menangkap ClientHello asli dari crypto/tls lewat net.Pipe.
func captureClientHello(t *testing.T, sni string) []byte {
	c1, c2 := net.Pipe()
	go func() {
		_ = tls.Client(c1, &tls.Config{ServerName: sni}).Handshake()
	}()
	buf := make([]byte, 4096)
	_ = c2.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := c2.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	c2.Close()
	c1.Close()
	return buf[:n]
}

func TestClientHelloSNI(t *testing.T) {
	raw := captureClientHello(t, "origin-a")
	recs, sni := ParseTLSRecords("out", raw, false)
	if sni != "origin-a" {
		t.Fatalf("SNI = %q", sni)
	}
	if len(recs) != 1 || recs[0].Kind != "Handshake" || !strings.HasPrefix(recs[0].Detail, "ClientHello") {
		t.Fatalf("record = %+v", recs)
	}
}

func TestParseTLSRecordsEncrypted(t *testing.T) {
	// ChangeCipherSpec lalu Handshake: yang kedua harus dianggap terenkripsi.
	stream := []byte{20, 3, 3, 0, 1, 1, 22, 3, 3, 0, 2, 0x99, 0x98, 23, 3, 3, 0, 1, 0x55}
	recs, _ := ParseTLSRecords("in", stream, false)
	if len(recs) != 3 || recs[1].Detail != "Finished (terenkripsi)" || recs[2].Kind != "ApplicationData" {
		t.Fatalf("records = %+v", recs)
	}
}

// TestLoadPageH1H2 menjalankan simulasi browser terhadap server TLS lokal.
func TestLoadPageH1H2(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 12; i++ {
			fmt.Fprintf(w, "<link href=\"/asset/%d.css\">\n", i)
		}
	})
	mux.HandleFunc("/asset/", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		fmt.Fprint(w, "body{}")
	})
	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}} // seperti origin
	srv.StartTLS()
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())

	h1 := LoadPage(context.Background(), "h1", srv.URL, roots, 12, 0)
	if h1.Assets != 12 || len(h1.Requests) != 13 {
		t.Fatalf("h1: aset %d, request %d", h1.Assets, len(h1.Requests))
	}
	if h1.Conns < 2 || h1.Conns > 6 {
		t.Fatalf("h1 harus memakai 2..6 koneksi, dapat %d", h1.Conns)
	}
	opened := 0
	for _, r := range h1.Requests {
		if !r.Reused && r.TLSDone > 0 {
			opened++
		}
	}
	if opened != h1.Conns {
		t.Fatalf("setiap koneksi h1 harus punya tepat satu request pembuka: %d vs %d", opened, h1.Conns)
	}
	if h1.ALPN != "http/1.1" {
		t.Fatalf("h1 ALPN %q", h1.ALPN)
	}
	h2 := LoadPage(context.Background(), "h2", srv.URL, roots, 12, 0)
	if h2.Conns != 1 || h2.ALPN != "h2" {
		t.Fatalf("h2 harus satu koneksi ALPN h2, dapat %d %q", h2.Conns, h2.ALPN)
	}
	first := h2.Requests[0]
	if first.TLSStart < 0 || first.TLSDone < first.TLSStart || first.TTFB < first.TLSDone {
		t.Fatalf("urutan tahap HTML salah: %+v", first)
	}
	for _, r := range h2.Requests[1:] {
		if !r.Reused || r.Err != "" {
			t.Fatalf("aset h2 harus memakai koneksi yang sama: %+v", r)
		}
	}
}

func TestSniffPlainVsTLS(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })
	plain := httptest.NewServer(h)
	defer plain.Close()
	secure := httptest.NewTLSServer(h)
	defer secure.Close()
	roots := x509.NewCertPool()
	roots.AddCert(secure.Certificate())

	p := Sniff(context.Background(), "http", plain.URL+"/login", roots)
	if !p.SecretVisible {
		t.Fatal("pada HTTP polos password seharusnya terlihat")
	}
	s := Sniff(context.Background(), "https", secure.URL+"/login", roots)
	if s.Err != "" || s.SecretVisible {
		t.Fatalf("pada HTTPS password tidak boleh terlihat (err=%s)", s.Err)
	}
	// Server uji memakai alamat IP, jadi tanpa SNI; cukup pastikan ClientHello terurai.
	if len(s.Records) == 0 || !strings.HasPrefix(s.Records[0].Detail, "ClientHello") {
		t.Fatalf("record pertama seharusnya ClientHello: %+v", s.Records)
	}
}

// TestLoadPageH3 menjalankan server HTTP/3 lokal (quic-go) dan memeriksa satu koneksi QUIC.
func TestLoadPageH3(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 5; i++ {
			fmt.Fprintf(w, "<link href=\"/asset/%d.css\">\n", i)
		}
	})
	mux.HandleFunc("/asset/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.Proto) })
	tlsSrv := httptest.NewTLSServer(mux) // hanya untuk meminjam certificate uji
	defer tlsSrv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(tlsSrv.Certificate())
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h3 := &http3.Server{Handler: mux, TLSConfig: http3.ConfigureTLSConfig(&tls.Config{Certificates: tlsSrv.TLS.Certificates})}
	go func() { _ = h3.Serve(pc) }()
	defer h3.Close()
	ld := LoadPage(context.Background(), "h3", "https://"+pc.LocalAddr().String(), roots, 5, 0)
	if ld.Assets != 5 || ld.Conns != 1 || ld.ALPN != "h3" {
		t.Fatalf("h3: aset %d koneksi %d ALPN %q err %q", ld.Assets, ld.Conns, ld.ALPN, ld.Requests[0].Err)
	}
	if ld.Requests[0].TLSDone < ld.Requests[0].TLSStart || ld.Requests[0].Proto != "HTTP/3.0" {
		t.Fatalf("request pertama: %+v", ld.Requests[0])
	}
}
