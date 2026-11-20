// Service router: container dengan NET_ADMIN yang meneruskan packet antara
// lan-klien (prober) dan lan-server (echo), dan memasang `tc netem` di kedua
// arah. Inilah "jaringan jelek" yang dapat diatur dari UI.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/httpx"
	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/logx"
	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/netem"
	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/netutil"
)

type router struct {
	log     *slog.Logger
	mu      sync.Mutex
	params  netem.Params
	toSrv   string // interface ke lan-server: egress = arah klien -> server
	toKlien string // interface ke lan-klien: egress = arah server -> klien
}

func main() {
	log := logx.New("router")
	r := &router{log: log}
	var err error
	if r.toKlien, err = netutil.WaitIfaceByIP(os.Getenv("KLIEN_SIDE_IP"), 15*time.Second); err != nil {
		log.Error("interface lan-klien", "err", err)
		os.Exit(1)
	}
	if r.toSrv, err = netutil.WaitIfaceByIP(os.Getenv("SERVER_SIDE_IP"), 15*time.Second); err != nil {
		log.Error("interface lan-server", "err", err)
		os.Exit(1)
	}
	if out, err := exec.Command("sysctl", "net.ipv4.ip_forward").CombinedOutput(); err == nil {
		log.Info("forwarding", "sysctl", string(out))
	}
	// mulai dari keadaan bersih (aturan netem lama ikut hilang bersama container, tetapi tetap pastikan)
	_ = r.apply(netem.Params{})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /netem", func(w http.ResponseWriter, req *http.Request) {
		httpx.JSON(w, 200, r.status(req.Context()))
	})
	mux.HandleFunc("POST /netem", func(w http.ResponseWriter, req *http.Request) {
		var p netem.Params
		if err := httpx.Decode(req, &p); err != nil {
			httpx.Error(w, 400, "body tidak valid")
			return
		}
		if err := p.Validate(); err != nil {
			httpx.Error(w, 400, err.Error())
			return
		}
		if err := r.apply(p); err != nil {
			httpx.Error(w, 500, err.Error())
			return
		}
		log.Info("netem diubah", "netem", p.String(), "c2s_iface", r.toSrv, "s2c_iface", r.toKlien)
		httpx.JSON(w, 200, r.status(req.Context()))
	})
	log.Info("router siap", "c2s_iface", r.toSrv, "s2c_iface", r.toKlien)
	srv := &http.Server{Addr: ":8082", Handler: httpx.Logging(log, mux), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("http berhenti", "err", err)
		os.Exit(1)
	}
}

func (r *router) apply(p netem.Params) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx := context.Background()
	for _, ifc := range []string{r.toSrv, r.toKlien} {
		if err := netem.Apply(ctx, ifc, p); err != nil {
			return err
		}
	}
	r.params = p
	return nil
}

type status struct {
	Params  netem.Params `json:"params"`
	Summary string       `json:"summary"`
	C2S     ifaceStat    `json:"c2s"`
	S2C     ifaceStat    `json:"s2c"`
}

type ifaceStat struct {
	Iface string `json:"iface"`
	TC    string `json:"tc"` // keluaran `tc -s qdisc show`: termasuk jumlah packet yang dibuang
}

func (r *router) status(ctx context.Context) status {
	r.mu.Lock()
	p := r.params
	r.mu.Unlock()
	show := func(ifc string) string {
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		out, err := exec.CommandContext(c, "tc", "-s", "qdisc", "show", "dev", ifc).CombinedOutput()
		if err != nil {
			return "error: " + err.Error()
		}
		return string(out)
	}
	return status{Params: p, Summary: p.String(),
		C2S: ifaceStat{Iface: r.toSrv, TC: show(r.toSrv)},
		S2C: ifaceStat{Iface: r.toKlien, TC: show(r.toKlien)}}
}
