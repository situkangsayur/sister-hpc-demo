// Package netem membungkus perintah `tc qdisc ... netem` (Bab 2, subbab tc netem).
//
// Aturan dipasang di egress sebuah interface. Router demo memasangnya di kedua
// interface (arah klien->server dan server->klien), sehingga RTT naik kira-kira
// dua kali nilai delay, sama seperti contoh `tc netem` pada `lo` di buku.
package netem

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Params adalah kondisi jaringan yang ditiru untuk satu arah.
type Params struct {
	DelayMs  int     `json:"delay_ms"`  // delay tetap per packet
	JitterMs int     `json:"jitter_ms"` // variasi delay (+/-)
	LossPct  float64 `json:"loss_pct"`  // peluang packet dibuang, persen
	RateMbit float64 `json:"rate_mbit"` // batas bandwidth; 0 = tanpa batas
}

// Validate memeriksa rentang nilai agar perintah tc tidak aneh-aneh.
func (p Params) Validate() error {
	switch {
	case p.DelayMs < 0 || p.DelayMs > 2000:
		return errors.New("delay_ms harus 0..2000")
	case p.JitterMs < 0 || p.JitterMs > 1000:
		return errors.New("jitter_ms harus 0..1000")
	case p.JitterMs > 0 && p.DelayMs == 0:
		return errors.New("jitter butuh delay > 0")
	case p.LossPct < 0 || p.LossPct > 50:
		return errors.New("loss_pct harus 0..50")
	case p.RateMbit < 0 || p.RateMbit > 10000:
		return errors.New("rate_mbit harus 0..10000")
	}
	return nil
}

// IsZero bernilai true bila tidak ada gangguan sama sekali.
func (p Params) IsZero() bool {
	return p.DelayMs == 0 && p.JitterMs == 0 && p.LossPct == 0 && p.RateMbit == 0
}

// String menghasilkan ringkasan seperti "delay 50ms loss 5%".
func (p Params) String() string {
	if p.IsZero() {
		return "normal (tanpa netem)"
	}
	return strings.Join(p.netemArgs(false), " ")
}

func (p Params) netemArgs(withLimit bool) []string {
	var a []string
	if p.DelayMs > 0 {
		a = append(a, "delay", fmt.Sprintf("%dms", p.DelayMs))
		if p.JitterMs > 0 {
			a = append(a, fmt.Sprintf("%dms", p.JitterMs))
		}
	}
	if p.LossPct > 0 {
		a = append(a, "loss", strconv.FormatFloat(p.LossPct, 'f', -1, 64)+"%")
	}
	if p.RateMbit > 0 {
		a = append(a, "rate", fmt.Sprintf("%dkbit", int(p.RateMbit*1000)))
	}
	if withLimit && len(a) > 0 {
		// antrean lebih panjang agar delay besar x rate tinggi tidak membuang packet sendiri
		a = append(a, "limit", "10000")
	}
	return a
}

// Commands mengembalikan argumen tc yang dijalankan berurutan untuk iface.
// Perintah pertama (del) boleh gagal bila belum ada qdisc.
func Commands(iface string, p Params) [][]string {
	cmds := [][]string{{"qdisc", "del", "dev", iface, "root"}}
	if !p.IsZero() {
		c := append([]string{"qdisc", "add", "dev", iface, "root", "netem"}, p.netemArgs(true)...)
		cmds = append(cmds, c)
	}
	return cmds
}

// Apply memasang (atau menghapus bila nol) aturan netem di iface.
func Apply(ctx context.Context, iface string, p Params) error {
	if err := p.Validate(); err != nil {
		return err
	}
	for i, c := range Commands(iface, p) {
		out, err := run(ctx, "tc", c...)
		if err != nil && i > 0 {
			return fmt.Errorf("tc %s: %v: %s", strings.Join(c, " "), err, out)
		}
	}
	return nil
}

// Show menjalankan `tc qdisc show dev iface`.
func Show(ctx context.Context, iface string) string {
	out, err := run(ctx, "tc", "qdisc", "show", "dev", iface)
	if err != nil {
		return "error: " + err.Error()
	}
	return strings.TrimSpace(out)
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}
