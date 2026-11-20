package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Eksperimen 5: biaya goroutine vs thread OS, metrik runtime, dan garbage collector
// (Subbab "CSP di Go" tentang murahnya goroutine dan "Go: scheduler M:N dengan model G-M-P").

type procStatus struct {
	RSSKB   int64 `json:"rss_kb"`
	VSizeKB int64 `json:"vsize_kb"`
	Threads int64 `json:"threads"`
}

// readProcStatus membaca /proc/self/status (Linux). Di OS lain nilainya nol.
func readProcStatus() procStatus {
	var ps procStatus
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return ps
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(v)
		if len(fields) == 0 {
			continue
		}
		n, _ := strconv.ParseInt(fields[0], 10, 64)
		switch k {
		case "VmRSS":
			ps.RSSKB = n
		case "VmSize":
			ps.VSizeKB = n
		case "Threads":
			ps.Threads = n
		}
	}
	return ps
}

type metricsSnap struct {
	TMs           int64     `json:"t_ms"`
	Goroutines    int       `json:"goroutines"`
	GoMaxProcs    int       `json:"gomaxprocs"`
	NumCPU        int       `json:"num_cpu"`
	HeapAllocMB   float64   `json:"heap_alloc_mb"`
	HeapInuseMB   float64   `json:"heap_inuse_mb"`
	StackInuseMB  float64   `json:"stack_inuse_mb"`
	SysMB         float64   `json:"sys_mb"`
	NumGC         uint32    `json:"num_gc"`
	LastPauseUs   float64   `json:"last_pause_us"`
	PauseTotalMs  float64   `json:"pause_total_ms"`
	GCCPUFraction float64   `json:"gc_cpu_fraction"`
	NewPausesUs   []float64 `json:"new_pauses_us,omitempty"`
	procStatus
}

const mb = 1 << 20

var t0Process = time.Now()

func snapshotMetrics() metricsSnap {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return metricsFrom(&m)
}

func metricsFrom(m *runtime.MemStats) metricsSnap {
	s := metricsSnap{
		TMs:        time.Since(t0Process).Milliseconds(),
		Goroutines: runtime.NumGoroutine(), GoMaxProcs: runtime.GOMAXPROCS(0), NumCPU: runtime.NumCPU(),
		HeapAllocMB: float64(m.HeapAlloc) / mb, HeapInuseMB: float64(m.HeapInuse) / mb,
		StackInuseMB: float64(m.StackInuse) / mb, SysMB: float64(m.Sys) / mb,
		NumGC: m.NumGC, PauseTotalMs: float64(m.PauseTotalNs) / 1e6, GCCPUFraction: m.GCCPUFraction,
		procStatus: readProcStatus(),
	}
	if m.NumGC > 0 {
		s.LastPauseUs = float64(m.PauseNs[(m.NumGC+255)%256]) / 1e3
	}
	return s
}

// pausesSince mengembalikan jeda GC (mikrodetik) untuk GC ke-(from+1) sampai m.NumGC.
func pausesSince(m *runtime.MemStats, from uint32) []float64 {
	var out []float64
	if m.NumGC-from > 256 {
		from = m.NumGC - 256
	}
	for g := from + 1; g <= m.NumGC; g++ {
		out = append(out, float64(m.PauseNs[(g+255)%256])/1e3)
	}
	return out
}

func handleMetrics(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, snapshotMetrics())
}

func handleMetricsStream(w http.ResponseWriter, r *http.Request) {
	every := time.Duration(qInt(r, "interval_ms", 500, 200, 5000)) * time.Millisecond
	sse, err := newSSE(w)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	lastGC := m.NumGC
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		runtime.ReadMemStats(&m)
		s := metricsFrom(&m)
		s.NewPausesUs = pausesSince(&m, lastGC)
		lastGC = m.NumGC
		if sse.send("metrics", s) != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

// ---- spawn: banyak goroutine yang parkir vs banyak thread OS ----

type spawnGroup struct {
	kind    string
	n       int
	release chan struct{}
	wg      sync.WaitGroup
}

var (
	spawnMu  sync.Mutex
	spawned  *spawnGroup
	spawnMax = map[string]int{"goroutine": 500_000, "thread": 5000}
)

func releaseSpawned() int {
	spawnMu.Lock()
	g := spawned
	spawned = nil
	spawnMu.Unlock()
	if g == nil {
		return 0
	}
	close(g.release)
	g.wg.Wait()
	runtime.GC()
	debug.FreeOSMemory()
	return g.n
}

func handleSpawn(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kind"`
		N    int    `json:"n"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "body JSON tidak valid")
		return
	}
	if req.Kind != "thread" {
		req.Kind = "goroutine"
	}
	req.N = clamp(req.N, 1, spawnMax[req.Kind])
	if !lockExperiment(w) {
		return
	}
	defer expMu.Unlock()
	releaseSpawned()

	runtime.GC()
	debug.FreeOSMemory()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	p0 := readProcStatus()

	g := &spawnGroup{kind: req.Kind, n: req.N, release: make(chan struct{})}
	var started sync.WaitGroup
	started.Add(req.N)
	start := time.Now()
	for i := 0; i < req.N; i++ {
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			if req.Kind == "thread" {
				// Goroutine terkunci ke thread OS-nya; saat ia parkir, thread ikut tidur,
				// sehingga runtime harus membuat thread OS baru untuk goroutine berikutnya.
				// Goroutine yang keluar tanpa UnlockOSThread membuat thread-nya dihentikan.
				runtime.LockOSThread()
			}
			started.Done()
			<-g.release
		}()
	}
	started.Wait()
	spawnDur := time.Since(start)
	time.Sleep(200 * time.Millisecond) // beri waktu thread baru tercatat di /proc
	runtime.ReadMemStats(&m1)
	p1 := readProcStatus()
	spawnMu.Lock()
	spawned = g
	spawnMu.Unlock()

	n := float64(req.N)
	res := map[string]any{
		"kind": req.Kind, "n": req.N, "spawn_ms": ms(spawnDur),
		"spawn_us_per_unit":    float64(spawnDur.Nanoseconds()) / n / 1e3,
		"stack_bytes_per_unit": float64(int64(m1.StackInuse)-int64(m0.StackInuse)) / n,
		"sys_bytes_per_unit":   float64(int64(m1.Sys)-int64(m0.Sys)) / n,
		"rss_bytes_per_unit":   float64(p1.RSSKB-p0.RSSKB) * 1024 / n,
		"vsize_bytes_per_unit": float64(p1.VSizeKB-p0.VSizeKB) * 1024 / n,
		"threads_before":       p0.Threads, "threads_after": p1.Threads,
		"goroutines_after": runtime.NumGoroutine(),
		"rss_mb_before":    float64(p0.RSSKB) / 1024, "rss_mb_after": float64(p1.RSSKB) / 1024,
	}
	logger.Info("spawn", "request_id", reqID(r), "kind", req.Kind, "n", req.N, "threads_after", p1.Threads)
	writeJSON(w, http.StatusOK, res)
}

func handleSpawnRelease(w http.ResponseWriter, r *http.Request) {
	n := releaseSpawned()
	logger.Info("spawn dilepas", "request_id", reqID(r), "n", n)
	writeJSON(w, http.StatusOK, map[string]any{"released": n, "metrics": snapshotMetrics()})
}

// handleGCChurn membuat sampah dengan laju tertentu agar GC bekerja; jedanya terlihat di metrik.
func handleGCChurn(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seconds float64 `json:"seconds"`
		MBPerS  int     `json:"mb_per_s"`
		LiveMB  int     `json:"live_mb"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Seconds <= 0 {
		req.Seconds = 4
	}
	req.Seconds = min(req.Seconds, 15)
	if req.MBPerS == 0 {
		req.MBPerS = 300
	}
	if req.LiveMB == 0 {
		req.LiveMB = 64
	}
	req.MBPerS = clamp(req.MBPerS, 10, 2000)
	req.LiveMB = clamp(req.LiveMB, 1, 256)
	if !lockExperiment(w) {
		return
	}
	defer expMu.Unlock()

	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	const chunk = 64 << 10
	live := make([][]byte, max(1, req.LiveMB*mb/chunk)) // ring: data yang tetap hidup
	perTick := req.MBPerS * mb / chunk / 100            // 100 tick per detik
	end := time.Now().Add(time.Duration(req.Seconds * float64(time.Second)))
	var allocated int64
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	i := 0
	for time.Now().Before(end) {
		<-tick.C
		for k := 0; k < perTick; k++ {
			b := make([]byte, chunk)
			b[0] = byte(k)
			live[i%len(live)] = b // yang lama menjadi sampah
			i++
			allocated += chunk
		}
	}
	runtime.ReadMemStats(&m1)
	pauses := pausesSince(&m1, m0.NumGC)
	sorted := slices.Clone(pauses)
	slices.Sort(sorted)
	var maxP float64
	if len(sorted) > 0 {
		maxP = sorted[len(sorted)-1]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"seconds": req.Seconds, "allocated_mb": float64(allocated) / mb, "gc_count": m1.NumGC - m0.NumGC,
		"pause_p50_us": pct(sorted, 0.5), "pause_p99_us": pct(sorted, 0.99), "pause_max_us": maxP,
		"pause_total_ms":  float64(m1.PauseTotalNs-m0.PauseTotalNs) / 1e6,
		"gc_cpu_fraction": m1.GCCPUFraction,
	})
}
