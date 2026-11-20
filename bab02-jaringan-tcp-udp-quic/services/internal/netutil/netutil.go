// Package netutil: helper perintah `ip` untuk routing antar network demo.
package netutil

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var reAddr = regexp.MustCompile(`^\d+:\s+(\S+)\s+inet\s+(\d+\.\d+\.\d+\.\d+)/`)

// ParseIfaceByIP mencari nama interface pemilik ip dari keluaran `ip -o -4 addr show`.
func ParseIfaceByIP(out, ip string) string {
	for _, line := range strings.Split(out, "\n") {
		m := reAddr.FindStringSubmatch(strings.TrimSpace(line))
		if m != nil && m[2] == ip {
			return strings.SplitN(m[1], "@", 2)[0]
		}
	}
	return ""
}

// IfaceByIP mengembalikan interface yang memiliki alamat ip.
func IfaceByIP(ctx context.Context, ip string) (string, error) {
	out, err := run(ctx, "ip", "-o", "-4", "addr", "show")
	if err != nil {
		return "", fmt.Errorf("ip addr: %v: %s", err, out)
	}
	if ifc := ParseIfaceByIP(out, ip); ifc != "" {
		return ifc, nil
	}
	return "", fmt.Errorf("tidak ada interface dengan alamat %s", ip)
}

// AddRoute menjalankan `ip route replace <subnet> via <gw>`.
func AddRoute(ctx context.Context, subnet, gw string) error {
	out, err := run(ctx, "ip", "route", "replace", subnet, "via", gw)
	if err != nil {
		return fmt.Errorf("ip route replace %s via %s: %v: %s", subnet, gw, err, out)
	}
	return nil
}

// WaitIfaceByIP mencoba IfaceByIP berulang sampai timeout (interface network kedua
// kadang baru terpasang sesaat setelah container start).
func WaitIfaceByIP(ip string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		ifc, err := IfaceByIP(context.Background(), ip)
		if err == nil || time.Now().After(deadline) {
			return ifc, err
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}
