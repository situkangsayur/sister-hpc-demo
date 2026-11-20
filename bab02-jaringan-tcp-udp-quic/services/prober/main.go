// Service prober: klien pengukur untuk TCP, UDP, dan QUIC (Bab 2).
//
// Prober berada di lan-klien; echo berada di lan-server; di antaranya ada
// router netem. Prober mengukur waktu handshake, RTT per message, packet loss,
// throughput, head-of-line blocking, dan merekam timeline satu koneksi
// (panggilan socket API + tangkapan tcpdump di kedua sisi).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/httpx"
	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/logx"
	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/netem"
	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/netutil"
)

// ALPN harus sama dengan service echo.
const ALPN = "bab02-echo"

type config struct {
	echoIP    string // alamat echo di lan-server (jalur data, lewat router)
	echoCtl   string // API kontrol echo (lewat network front, tanpa netem)
	routerCtl string
	labIP     string // alamat prober di lan-klien
	iface     string // interface lan-klien, untuk tcpdump sisi klien
}

type app struct {
	log *slog.Logger
	cfg config

	mu      sync.Mutex
	current *Run
	history []RunSummary
	nextID  int
	busy    sync.Mutex // hanya satu eksperimen trace/HOL pada satu waktu
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	log := logx.New("prober")
	a := &app{log: log, cfg: config{
		echoIP:    env("ECHO_LAB_IP", "10.202.3.10"),
		echoCtl:   env("ECHO_CTL", "http://echo:8081"),
		routerCtl: env("ROUTER_CTL", "http://router:8082"),
		labIP:     env("LAB_IP", "10.202.2.10"),
	}}
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
	ifc, err := netutil.WaitIfaceByIP(a.cfg.labIP, 10*time.Second)
	if err != nil {
		log.Error("interface lan-klien tidak ditemukan", "err", err)
		ifc = "any"
	}
	a.cfg.iface = ifc

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/status", a.handleStatus)
	mux.HandleFunc("GET /api/netem", a.proxy("GET", a.cfg.routerCtl+"/netem"))
	mux.HandleFunc("POST /api/netem", a.proxy("POST", a.cfg.routerCtl+"/netem"))
	mux.HandleFunc("GET /api/ss", a.proxy("GET", a.cfg.echoCtl+"/ss"))
	mux.HandleFunc("POST /api/run", a.handleRunStart)
	mux.HandleFunc("GET /api/run", a.handleRunGet)
	mux.HandleFunc("GET /api/history", func(w http.ResponseWriter, _ *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		httpx.JSON(w, 200, append([]RunSummary{}, a.history...))
	})
	mux.HandleFunc("POST /api/hol", a.handleHOL)
	mux.HandleFunc("POST /api/trace", a.handleTrace)

	log.Info("prober siap", "echo", a.cfg.echoIP, "iface", a.cfg.iface)
	srv := &http.Server{Addr: ":8080", Handler: httpx.Logging(log, mux), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("http berhenti", "err", err)
		os.Exit(1)
	}
}

var ctlClient = &http.Client{Timeout: 15 * time.Second}

// proxy meneruskan request ke API internal (router/echo) apa adanya.
func (a *app) proxy(method, url string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body io.Reader
		if method == "POST" {
			b, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
			body = bytes.NewReader(b)
		}
		req, _ := http.NewRequestWithContext(r.Context(), method, url, body)
		req.Header.Set("Content-Type", "application/json")
		resp, err := ctlClient.Do(req)
		if err != nil {
			httpx.Error(w, 502, err.Error())
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}

func ctlJSON(ctx context.Context, method, url string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ctlClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("%s %s: %d %s", method, url, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (a *app) currentNetem(ctx context.Context) netem.Params {
	var st struct {
		Params netem.Params `json:"params"`
	}
	_ = ctlJSON(ctx, "GET", a.cfg.routerCtl+"/netem", nil, &st)
	return st.Params
}

func (a *app) handleStatus(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, 200, map[string]any{
		"echo_ip": a.cfg.echoIP, "prober_ip": a.cfg.labIP, "iface": a.cfg.iface,
		"netem": a.currentNetem(r.Context()),
	})
}
