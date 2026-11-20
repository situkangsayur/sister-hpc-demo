package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Eksperimen 1: penghitung bersama (Subbab "Thread dan Lock: Race Condition" dan studi kasus
// "Satu Soal, Tiga Model"). Banyak goroutine menambah satu saldo; hasil wajib tepat G*N.

var counterModes = []string{"unsafe", "mutex", "atomic", "channel", "actor"}

type plainCounter struct{ n int64 }

// incUnsafe sengaja tidak aman: c.n++ adalah baca-tambah-tulis tanpa sinkronisasi.
//
//go:noinline
func incUnsafe(c *plainCounter, iters int) {
	for i := 0; i < iters; i++ {
		c.n++
	}
}

// runCounter menjalankan satu percobaan dengan g goroutine, masing-masing iters kali tambah 1.
func runCounter(mode string, g, iters int) int64 {
	var wg sync.WaitGroup
	wg.Add(g)
	switch mode {
	case "unsafe":
		c := &plainCounter{}
		for t := 0; t < g; t++ {
			go func() { defer wg.Done(); incUnsafe(c, iters) }()
		}
		wg.Wait()
		return c.n

	case "mutex":
		var mu sync.Mutex
		var n int64
		for t := 0; t < g; t++ {
			go func() {
				defer wg.Done()
				for i := 0; i < iters; i++ {
					mu.Lock()
					n++ // critical section
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		return n

	case "atomic":
		var n atomic.Int64
		for t := 0; t < g; t++ {
			go func() {
				defer wg.Done()
				for i := 0; i < iters; i++ {
					n.Add(1) // satu instruksi atomik (LOCK XADD di amd64)
				}
			}()
		}
		wg.Wait()
		return n.Load()

	case "channel":
		// Satu goroutine pemilik saldo; yang lain hanya mengirim lewat channel (CSP).
		tambah := make(chan int64, 1024)
		selesai := make(chan int64)
		go func() {
			var saldo int64
			for d := range tambah {
				saldo += d
			}
			selesai <- saldo
		}()
		for t := 0; t < g; t++ {
			go func() {
				defer wg.Done()
				for i := 0; i < iters; i++ {
					tambah <- 1
				}
			}()
		}
		wg.Wait()
		close(tambah)
		return <-selesai

	case "actor":
		// Actor Penghitung: state privat, hanya diubah oleh pesan di mailbox.
		a := newCounterActor(1024)
		for t := 0; t < g; t++ {
			go func() {
				defer wg.Done()
				for i := 0; i < iters; i++ {
					a.mailbox <- counterMsg{kind: "Tambah", n: 1}
				}
			}()
		}
		wg.Wait()
		reply := make(chan int64, 1)
		a.mailbox <- counterMsg{kind: "Ambil", reply: reply}
		v := <-reply
		close(a.mailbox)
		return v
	}
	wg.Add(-g)
	return -1
}

type counterMsg struct {
	kind  string // "Tambah" atau "Ambil"
	n     int64
	reply chan<- int64
}

type counterActor struct{ mailbox chan counterMsg }

func newCounterActor(capacity int) *counterActor {
	a := &counterActor{mailbox: make(chan counterMsg, capacity)}
	go func() {
		var state int64 // hanya goroutine actor ini yang menyentuh state
		for m := range a.mailbox {
			switch m.kind {
			case "Tambah":
				state += m.n
			case "Ambil":
				m.reply <- state
			}
		}
	}()
	return a
}

type raceTrial struct {
	Got        int64   `json:"got"`
	Lost       int64   `json:"lost"`
	DurationMs float64 `json:"duration_ms"`
}

type raceModeResult struct {
	Mode       string      `json:"mode"`
	Expected   int64       `json:"expected"`
	Trials     []raceTrial `json:"trials"`
	AllCorrect bool        `json:"all_correct"`
	MedianMs   float64     `json:"median_ms"`
}

type raceRequest struct {
	Modes      []string `json:"modes"`
	Goroutines int      `json:"goroutines"`
	Iters      int      `json:"iters"`
	Trials     int      `json:"trials"`
}

func handleRace(w http.ResponseWriter, r *http.Request) {
	req := raceRequest{Goroutines: 1000, Iters: 1000, Trials: 5}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "body JSON tidak valid")
			return
		}
	}
	req.Goroutines = clamp(req.Goroutines, 1, 10000)
	req.Iters = clamp(req.Iters, 1, 100000)
	req.Trials = clamp(req.Trials, 1, 10)
	if int64(req.Goroutines)*int64(req.Iters) > 20_000_000 {
		writeErr(w, http.StatusBadRequest, "goroutines*iters maksimal 20 juta")
		return
	}
	if len(req.Modes) == 0 {
		req.Modes = counterModes
	}
	for _, m := range req.Modes {
		if !slices.Contains(counterModes, m) {
			writeErr(w, http.StatusBadRequest, "mode tidak dikenal: "+m)
			return
		}
	}
	if !lockExperiment(w) {
		return
	}
	defer expMu.Unlock()

	expected := int64(req.Goroutines) * int64(req.Iters)
	out := make([]raceModeResult, 0, len(req.Modes))
	for _, m := range req.Modes {
		res := raceModeResult{Mode: m, Expected: expected, AllCorrect: true}
		durs := make([]float64, 0, req.Trials)
		for t := 0; t < req.Trials; t++ {
			start := time.Now()
			got := runCounter(m, req.Goroutines, req.Iters)
			d := float64(time.Since(start).Microseconds()) / 1000
			res.Trials = append(res.Trials, raceTrial{Got: got, Lost: expected - got, DurationMs: d})
			durs = append(durs, d)
			if got != expected {
				res.AllCorrect = false
			}
		}
		res.MedianMs = median(durs)
		out = append(out, res)
	}
	logger.Info("race selesai", "request_id", reqID(r), "goroutines", req.Goroutines, "iters", req.Iters)
	writeJSON(w, http.StatusOK, map[string]any{
		"goroutines": req.Goroutines, "iters": req.Iters, "gomaxprocs": runtime.GOMAXPROCS(0),
		"results": out,
	})
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// handleRaceDetector menjalankan biner racedemo yang dibangun dengan `go build -race`
// dan mengembalikan laporan race detector apa adanya.
func handleRaceDetector(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if !slices.Contains([]string{"unsafe", "mutex", "atomic", "channel"}, req.Mode) {
		req.Mode = "unsafe"
	}
	bin := envOr("RACEDEMO_BIN", "/app/racedemo")
	if _, err := os.Stat(bin); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "biner racedemo tidak tersedia: "+bin)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-mode", req.Mode, "-g", "4", "-n", "2000")
	// halt_on_error=0: laporkan semua race; exitcode 66 bila ada race.
	cmd.Env = append(os.Environ(), "GORACE=halt_on_error=0 exitcode=66 history_size=1")
	outB, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	text := string(outB)
	reports := strings.Count(text, "WARNING: DATA RACE")
	lines := strings.Split(text, "\n")
	if len(lines) > 80 {
		lines = append(lines[:80], "... (dipotong)")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode": req.Mode, "command": "GORACE=… ./racedemo -mode " + req.Mode + " -g 4 -n 2000   (dibangun dengan go build -race)",
		"exit_code": exitCode, "race_reports": reports, "output": strings.Join(lines, "\n"),
	})
}
