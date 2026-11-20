// Service echo: server echo TCP, UDP, TLS-over-TCP, dan QUIC untuk Bab 2.
//
//	:9000/tcp  echo per baris (uji dengan `nc host 9000`), mencatat recv()/send()
//	:9001/udp  echo datagram; "SINK:<id>:" dihitung untuk uji throughput
//	:9002/udp  QUIC (quic-go), stream diawali 'E' = echo, 'S' = sink
//	:9003/tcp  TLS 1.3 di atas TCP (pembanding handshake TCP+TLS vs QUIC)
//	:9004/tcp  sink TCP: baca sampai EOF lalu balas jumlah byte
//	:8081      API kontrol internal: tcpdump, trace socket, ss -tni
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/certs"
	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/httpx"
	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/logx"
	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/netutil"
	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/pcap"
	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/trace"
)

// ALPN dipakai bersama oleh server dan prober.
const ALPN = "bab02-echo"

type server struct {
	log    *slog.Logger
	ring   *trace.Ring
	sinks  *udpSinks
	labIP  string
	iface  string
	capMu  sync.Mutex
	active *pcap.Capture
}

func main() {
	log := logx.New("echo")
	s := &server{log: log, ring: trace.NewRing(300), sinks: newUDPSinks(), labIP: os.Getenv("LAB_IP")}

	// route ke subnet klien lewat router netem, contoh "10.202.2.0/24 via 10.202.3.254"
	if r := os.Getenv("ROUTE"); r != "" {
		parts := strings.Fields(r)
		if len(parts) == 3 && parts[1] == "via" {
			if err := netutil.AddRoute(context.Background(), parts[0], parts[2]); err != nil {
				log.Error("route gagal", "err", err)
			} else {
				log.Info("route dipasang", "route", r)
			}
		}
	}
	if s.labIP != "" {
		ifc, err := netutil.WaitIfaceByIP(s.labIP, 10*time.Second)
		if err != nil {
			log.Error("interface lab tidak ditemukan", "err", err)
		}
		s.iface = ifc
	}
	if s.iface == "" {
		s.iface = "any"
	}

	cert, err := certs.SelfSigned("echo", "localhost", s.labIP)
	if err != nil {
		log.Error("gagal membuat sertifikat", "err", err)
		os.Exit(1)
	}
	tlsConf := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}

	go s.serveTCPEcho(":9000")
	go s.serveUDP(":9001")
	go s.serveQUIC(":9002", tlsConf)
	go s.serveTLS(":9003", tlsConf)
	go s.serveTCPSink(":9004")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, 200, map[string]string{"status": "ok", "iface": s.iface})
	})
	mux.HandleFunc("POST /capture/start", s.handleCaptureStart)
	mux.HandleFunc("POST /capture/stop", s.handleCaptureStop)
	mux.HandleFunc("GET /trace", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, 200, s.ring.Get(r.URL.Query().Get("key")))
	})
	mux.HandleFunc("GET /ss", s.handleSS)
	log.Info("echo siap", "tcp", 9000, "udp", 9001, "quic", 9002, "tls", 9003, "sink", 9004, "iface", s.iface)
	srv := &http.Server{Addr: ":8081", Handler: httpx.Logging(log, mux), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("http berhenti", "err", err)
		os.Exit(1)
	}
}

// Filter tcpdump yang diizinkan (tidak menerima string bebas dari luar).
var captureFilters = map[string][]string{
	"tcp":  {"tcp", "port", "9000"},
	"tls":  {"tcp", "port", "9003"},
	"udp":  {"udp", "port", "9001"},
	"quic": {"udp", "port", "9002"},
}

func (s *server) handleCaptureStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Proto string `json:"proto"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, 400, "body tidak valid")
		return
	}
	f, ok := captureFilters[req.Proto]
	if !ok {
		httpx.Error(w, 400, "proto harus tcp|tls|udp|quic")
		return
	}
	s.capMu.Lock()
	defer s.capMu.Unlock()
	if s.active != nil {
		s.active.Stop()
		s.active = nil
	}
	c, err := pcap.Start(s.iface, f, 30*time.Second)
	if err != nil {
		httpx.Error(w, 500, err.Error())
		return
	}
	s.active = c
	httpx.JSON(w, 200, map[string]string{"status": "capturing", "iface": s.iface, "filter": strings.Join(f, " ")})
}

func (s *server) handleCaptureStop(w http.ResponseWriter, r *http.Request) {
	s.capMu.Lock()
	defer s.capMu.Unlock()
	if s.active == nil {
		httpx.Error(w, 409, "tidak ada capture aktif")
		return
	}
	out := s.active.Stop()
	s.active = nil
	httpx.JSON(w, 200, map[string]string{"raw": out})
}

func (s *server) handleSS(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ss", "-tni", "state", "established",
		"( sport = :9000 or sport = :9003 or sport = :9004 )").CombinedOutput()
	if err != nil {
		httpx.Error(w, 500, fmt.Sprintf("ss: %v", err))
		return
	}
	httpx.JSON(w, 200, map[string]string{"cmd": "ss -tni state established '( sport = :9000 or ... )'", "out": string(out)})
}

// tcpConn mencatat panggilan socket API untuk satu koneksi TCP.
func (s *server) serveTCPEcho(addr string) {
	ln, err := net.Listen("tcp", addr) // = socket() + bind() + listen()
	if err != nil {
		s.log.Error("listen tcp", "err", err)
		os.Exit(1)
	}
	for {
		c, err := ln.Accept() // menunggu klien yang sudah selesai three-way handshake
		if err != nil {
			continue
		}
		go s.handleTCPEcho(c)
	}
}

func preview(b []byte) string {
	if len(b) > 40 {
		return fmt.Sprintf("%q...", b[:40])
	}
	return fmt.Sprintf("%q", b)
}

func (s *server) handleTCPEcho(c net.Conn) {
	key := c.RemoteAddr().String()
	s.ring.Add(key, trace.Event{Side: "server", Call: "accept()", Detail: "socket baru untuk " + key})
	s.log.Info("tcp accept", "remote", key)
	defer func() {
		_ = c.Close()
		s.ring.Add(key, trace.Event{Side: "server", Call: "close()", Detail: "server menutup socket (kirim FIN)"})
	}()
	buf := make([]byte, 4096)
	var pending []byte
	for {
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Minute))
		n, err := c.Read(buf)
		if n > 0 {
			s.ring.Add(key, trace.Event{Side: "server", Call: "recv()", Bytes: n,
				Detail: fmt.Sprintf("recv() = %d byte %s", n, preview(buf[:n]))})
			pending = append(pending, buf[:n]...)
			// framing dengan pemisah '\n': satu recv() bisa berisi nol, satu, atau beberapa baris
			for {
				i := indexNL(pending)
				if i < 0 {
					break
				}
				line := pending[:i+1]
				if _, werr := c.Write(line); werr != nil {
					return
				}
				s.ring.Add(key, trace.Event{Side: "server", Call: "send()", Bytes: len(line),
					Detail: fmt.Sprintf("send() %d byte %s", len(line), preview(line))})
				pending = pending[i+1:]
			}
			if len(pending) > 64<<10 {
				s.log.Warn("baris terlalu panjang, koneksi ditutup", "remote", key)
				return
			}
		}
		if err != nil {
			s.ring.Add(key, trace.Event{Side: "server", Call: "recv()", Detail: "recv() = 0: klien menutup arah kirim (FIN)"})
			return
		}
	}
}

func indexNL(b []byte) int {
	for i, x := range b {
		if x == '\n' {
			return i
		}
	}
	return -1
}

func (s *server) serveTLS(addr string, conf *tls.Config) {
	ln, err := tls.Listen("tcp", addr, conf)
	if err != nil {
		s.log.Error("listen tls", "err", err)
		os.Exit(1)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(2 * time.Minute))
			buf := make([]byte, 4096)
			for {
				n, err := c.Read(buf)
				if n > 0 {
					if _, werr := c.Write(buf[:n]); werr != nil {
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()
	}
}

func (s *server) serveTCPSink(addr string) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.log.Error("listen sink", "err", err)
		os.Exit(1)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(2 * time.Minute))
			buf := make([]byte, 64<<10)
			total := 0
			for {
				n, err := c.Read(buf)
				total += n
				if err != nil {
					break
				}
			}
			fmt.Fprintf(c, "%d\n", total)
			s.log.Info("tcp sink selesai", "remote", c.RemoteAddr().String(), "bytes", total)
		}()
	}
}
