package main

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
)

// Netem mengatur delay dan packet loss buatan (tc netem) di interface probe yang menuju origin.
// RTT antar container di satu host hampir nol, sehingga perbedaan h1/h2/h3 baru terlihat
// bila jaringan "dijauhkan". Delay hanya dipasang di arah keluar probe, jadi RTT bertambah
// sebesar delay tersebut. Butuh capability NET_ADMIN (lihat compose.yaml).
type Netem struct {
	mu      sync.Mutex
	Target  string  `json:"target"`
	Dev     string  `json:"dev"`
	DelayMs int     `json:"delay_ms"`
	LossPct float64 `json:"loss_pct"`
	Err     string  `json:"err,omitempty"`
}

// ParseRouteDev mengambil nama interface dari keluaran `ip -o route get`.
func ParseRouteDev(out string) string {
	f := strings.Fields(out)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == "dev" {
			return f[i+1]
		}
	}
	return ""
}

func (n *Netem) dev() (string, error) {
	if n.Dev != "" {
		return n.Dev, nil
	}
	ips, err := net.LookupHost(n.Target)
	if err != nil || len(ips) == 0 {
		return "", fmt.Errorf("lookup %s: %v", n.Target, err)
	}
	out, err := exec.Command("ip", "-o", "route", "get", ips[0]).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ip route get: %v: %s", err, out)
	}
	d := ParseRouteDev(string(out))
	if d == "" {
		return "", fmt.Errorf("interface tidak ditemukan: %s", out)
	}
	n.Dev = d
	return d, nil
}

// Set memasang (atau mencabut, bila keduanya nol) qdisc netem.
func (n *Netem) Set(delayMs int, lossPct float64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	dev, err := n.dev()
	if err != nil {
		n.Err = err.Error()
		return err
	}
	var cmd *exec.Cmd
	if delayMs <= 0 && lossPct <= 0 {
		cmd = exec.Command("tc", "qdisc", "del", "dev", dev, "root")
	} else {
		args := []string{"qdisc", "replace", "dev", dev, "root", "netem", "delay", fmt.Sprintf("%dms", delayMs)}
		if lossPct > 0 {
			args = append(args, "loss", fmt.Sprintf("%.1f%%", lossPct))
		}
		cmd = exec.Command("tc", args...)
	}
	if out, err := cmd.CombinedOutput(); err != nil && !(delayMs <= 0 && lossPct <= 0) {
		n.Err = fmt.Sprintf("tc: %v: %s", err, strings.TrimSpace(string(out)))
		return fmt.Errorf("%s", n.Err)
	}
	n.DelayMs, n.LossPct, n.Err = delayMs, lossPct, ""
	return nil
}

// Snapshot mengembalikan salinan state untuk JSON.
func (n *Netem) Snapshot() map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	return map[string]any{"target": n.Target, "dev": n.Dev, "delay_ms": n.DelayMs, "loss_pct": n.LossPct, "err": n.Err}
}
