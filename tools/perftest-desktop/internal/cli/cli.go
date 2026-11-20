// Package cli adalah mode baris perintah perftest (tanpa layar). Dipakai oleh binary
// cmd/perftest dan juga oleh aplikasi desktop (perftest-desktop run ...).
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/situkangsayur/sister-hpc-demo/tools/perftest-desktop/engine"
	"github.com/situkangsayur/sister-hpc-demo/tools/perftest-desktop/presets"
)

const usage = `perftest: load test REST/HTTP, GraphQL, gRPC, dan WebSocket untuk demo Sistem Terdistribusi.

Pemakaian:
  perftest run --config skenario.yaml [opsi]     jalankan skenario dari file YAML
  perftest run --preset 4.1 [opsi]               jalankan preset (nomor dari "perftest presets")
  perftest presets [--bab N]                     daftar preset endpoint tiap bab
  perftest grpc-list --target host:port [--tls]  daftar method gRPC lewat server reflection

Opsi run:
  --host ALAMAT        pengganti ${BIND_ADDR} (default: env BIND_ADDR atau 127.0.0.1)
  --concurrency N      timpa concurrency
  --duration 30s       timpa durasi
  --requests N         timpa jumlah request (durasi dikosongkan bila --duration tidak diberikan)
  --rps R              timpa target RPS (0 = secepat mungkin)
  --json FILE          simpan hasil lengkap JSON
  --csv FILE           simpan timeline CSV
  --max-error-rate F   keluar dengan kode 1 bila rasio error > F (0..1; default 1 = tidak dicek)
  --quiet              tanpa baris live per detik
`

// Main menjalankan CLI dan mengembalikan exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:], stdout, stderr)
	case "presets":
		return cmdPresets(args[1:], stdout, stderr)
	case "grpc-list":
		return cmdGRPCList(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "perintah tidak dikenal %q\n\n%s", args[0], usage)
	return 2
}

// IsCLI melaporkan apakah argumen meminta mode CLI (dipakai binary desktop).
func IsCLI(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "run", "presets", "grpc-list", "help", "-h", "--help":
		return true
	}
	return false
}

// presetIndex memberi nomor "bab.urutan" (mis. 4.2) untuk tiap preset.
func presetIndex(ps []presets.Preset) []string {
	seen := map[int]int{}
	out := make([]string, len(ps))
	for i, p := range ps {
		seen[p.Bab]++
		out[i] = fmt.Sprintf("%d.%d", p.Bab, seen[p.Bab])
	}
	return out
}

func cmdPresets(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("presets", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bab := fs.Int("bab", -1, "hanya bab ini")
	file := fs.String("file", "", "file preset YAML lain")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ps, err := loadPresets(*file)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	idx := presetIndex(ps)
	for i, p := range ps {
		if *bab >= 0 && p.Bab != *bab {
			continue
		}
		addr := p.Config.URL
		if addr == "" {
			addr = p.Config.Target + " " + p.Config.GRPCMethod
		}
		fmt.Fprintf(stdout, "%-5s %-8s %-62s %s\n", idx[i], p.Config.Kind, p.Name, addr)
	}
	return 0
}

func loadPresets(file string) ([]presets.Preset, error) {
	if file != "" {
		return presets.LoadFile(file)
	}
	return presets.Builtin(), nil
}

func cmdGRPCList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("grpc-list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	target := fs.String("target", "", "host:port")
	useTLS := fs.Bool("tls", false, "pakai TLS")
	insecure := fs.Bool("insecure", false, "terima sertifikat self-signed")
	if err := fs.Parse(args); err != nil || *target == "" {
		fmt.Fprintln(stderr, "butuh --target host:port")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ms, err := engine.ListGRPCMethods(ctx, os.ExpandEnv(*target), *useTLS, *insecure)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	for _, m := range ms {
		fmt.Fprintf(stdout, "%s\n    request %s contoh %s\n", m.Method, m.Input, m.Example)
	}
	return 0
}

func cmdRun(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "file skenario YAML")
	preset := fs.String("preset", "", "nomor preset, mis. 4.1")
	presetFile := fs.String("presets-file", "", "file preset YAML lain")
	host := fs.String("host", "", "pengganti ${BIND_ADDR}")
	conc := fs.Int("concurrency", 0, "")
	dur := fs.String("duration", "", "")
	reqs := fs.Int("requests", -1, "")
	rps := fs.Float64("rps", -1, "")
	jsonOut := fs.String("json", "", "")
	csvOut := fs.String("csv", "", "")
	maxErr := fs.Float64("max-error-rate", 1, "")
	quiet := fs.Bool("quiet", false, "")
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var cfg engine.Config
	switch {
	case *cfgPath != "":
		c, err := engine.LoadConfig(*cfgPath)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		cfg = c
	case *preset != "":
		ps, err := loadPresets(*presetFile)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		found := false
		for i, id := range presetIndex(ps) {
			if id == *preset {
				cfg, found = ps[i].Config, true
			}
		}
		if !found {
			fmt.Fprintf(stderr, "preset %q tidak ada; lihat: perftest presets\n", *preset)
			return 1
		}
	default:
		fmt.Fprintln(stderr, "butuh --config FILE atau --preset N")
		return 2
	}
	if *conc > 0 {
		cfg.Concurrency = *conc
	}
	if *reqs >= 0 {
		cfg.Requests = *reqs
		if *dur == "" && *reqs > 0 {
			cfg.Duration = ""
		}
	}
	if *dur != "" {
		cfg.Duration = *dur
	}
	if *rps >= 0 {
		cfg.RPS = *rps
	}
	vars := map[string]string{}
	if *host != "" {
		vars["BIND_ADDR"] = *host
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ex := cfg.Expand(vars)
	fmt.Fprintf(stdout, "perftest %s %s%s%s  concurrency=%d duration=%s requests=%d rps=%g\n",
		ex.Kind, ex.URL, ex.Target, prefixed(" ", ex.GRPCMethod), max(ex.Concurrency, 1), orDash(ex.Duration), ex.Requests, ex.RPS)
	if !*quiet {
		fmt.Fprintf(stdout, "%7s %9s %8s %7s %9s %9s %9s\n", "t(s)", "rps", "ok", "err", "p50(ms)", "p95(ms)", "p99(ms)")
	}
	rep, err := engine.Run(ctx, cfg, engine.Options{Vars: vars, OnTick: func(p engine.Point) {
		if *quiet {
			return
		}
		fmt.Fprintf(stdout, "%7.1f %9.1f %8d %7d %9.2f %9.2f %9.2f\n", p.T, p.RPS, p.OK, p.Errors, p.P50, p.P95, p.P99)
	}})
	if err != nil {
		fmt.Fprintln(stderr, "gagal:", err)
		return 1
	}
	printSummary(stdout, rep)
	if *jsonOut != "" {
		if err := writeFile(*jsonOut, rep.WriteJSON); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "JSON  :", *jsonOut)
	}
	if *csvOut != "" {
		if err := writeFile(*csvOut, rep.WriteCSV); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "CSV   :", *csvOut)
	}
	if rep.Total == 0 {
		fmt.Fprintln(stderr, "tidak ada request yang selesai")
		return 1
	}
	if rate := float64(rep.Errors) / float64(rep.Total); rate > *maxErr {
		fmt.Fprintf(stderr, "rasio error %.3f melebihi --max-error-rate %.3f\n", rate, *maxErr)
		return 1
	}
	return 0
}

func prefixed(p, s string) string {
	if s == "" {
		return ""
	}
	return p + s
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func writeFile(path string, fn func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := fn(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func printSummary(w io.Writer, r engine.Report) {
	l := r.Latency
	fmt.Fprintf(w, "\n== Ringkasan (%.2f s%s)\n", r.ElapsedS, map[bool]string{true: ", dihentikan", false: ""}[r.Stopped])
	fmt.Fprintf(w, "request: %d  ok: %d  error: %d  throughput: %.1f req/s\n", r.Total, r.OK, r.Errors, r.RPS)
	fmt.Fprintf(w, "latency ms: min %.2f  mean %.2f  p50 %.2f  p90 %.2f  p95 %.2f  p99 %.2f  max %.2f\n",
		l.Min, l.Mean, l.P50, l.P90, l.P95, l.P99, l.Max)
	if len(r.ErrorsByKind) > 0 {
		keys := make([]string, 0, len(r.ErrorsByKind))
		for k := range r.ErrorsByKind {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + "=" + strconv.FormatUint(r.ErrorsByKind[k], 10)
		}
		fmt.Fprintln(w, "error per jenis:", strings.Join(parts, "  "))
	}
}
