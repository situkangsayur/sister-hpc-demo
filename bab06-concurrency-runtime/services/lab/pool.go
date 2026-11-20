package main

import (
	"encoding/json"
	"net/http"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Eksperimen 3: worker pool (Subbab "Thread Pool dan Virtual Thread" dan "Worker pool").
// Pekerjaan CPU-bound hanya bertambah cepat sampai jumlah worker = GOMAXPROCS;
// pekerjaan I/O-bound (menunggu) terus bertambah cepat dengan lebih banyak worker.

var cpuItersPerMs atomic.Int64

// cpuWork adalah kerja CPU murni (xorshift) yang tidak bisa dihilangkan compiler.
//
//go:noinline
func cpuWork(n int64) uint64 {
	x := uint64(n) | 1
	for i := int64(0); i < n; i++ {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
	}
	return x
}

var sink atomic.Uint64

// calibrateCPU mengukur berapa iterasi cpuWork yang setara 1 ms pada satu core.
func calibrateCPU() {
	const n = 5_000_000
	best := time.Hour
	for i := 0; i < 3; i++ {
		start := time.Now()
		sink.Add(cpuWork(n))
		best = min(best, time.Since(start))
	}
	per := int64(float64(n) / (float64(best.Microseconds()) / 1000))
	cpuItersPerMs.Store(max(per, 1000))
}

type poolPoint struct {
	Workers      int     `json:"workers"`
	ElapsedMs    float64 `json:"elapsed_ms"`
	Throughput   float64 `json:"throughput"` // job per detik
	LatP50Ms     float64 `json:"lat_p50_ms"`
	LatP95Ms     float64 `json:"lat_p95_ms"`
	ServiceAvgMs float64 `json:"service_avg_ms"` // waktu dinding per job, tanpa antre
}

type poolSeries struct {
	GoMaxProcs int         `json:"gomaxprocs"`
	Points     []poolPoint `json:"points"`
}

// runPool menjalankan `jobs` job sekaligus (semua masuk antrean pada t0) dengan `workers` worker.
func runPool(kind string, workers, jobs int, jobMs float64) poolPoint {
	type job struct{ enq time.Time }
	type res struct{ lat, svc time.Duration }
	jobsCh := make(chan job, jobs)
	out := make(chan res, jobs)
	iters := int64(jobMs * float64(cpuItersPerMs.Load()))
	sleep := time.Duration(jobMs * float64(time.Millisecond))

	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobsCh {
				s := time.Now()
				if kind == "cpu" {
					sink.Add(cpuWork(iters))
				} else {
					time.Sleep(sleep) // pura-pura menunggu I/O: goroutine parkir, P bebas
				}
				e := time.Now()
				out <- res{lat: e.Sub(j.enq), svc: e.Sub(s)}
			}
		}()
	}
	for i := 0; i < jobs; i++ {
		jobsCh <- job{enq: start}
	}
	close(jobsCh)
	wg.Wait()
	elapsed := time.Since(start)
	close(out)

	lats := make([]float64, 0, jobs)
	var svc time.Duration
	for r := range out {
		lats = append(lats, ms(r.lat))
		svc += r.svc
	}
	slices.Sort(lats)
	return poolPoint{
		Workers: workers, ElapsedMs: ms(elapsed),
		Throughput:   float64(jobs) / elapsed.Seconds(),
		LatP50Ms:     pct(lats, 0.50),
		LatP95Ms:     pct(lats, 0.95),
		ServiceAvgMs: ms(svc) / float64(jobs),
	}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(p * float64(len(sorted)-1))
	return sorted[i]
}

type poolRequest struct {
	Kind       string  `json:"kind"`
	Jobs       int     `json:"jobs"`
	JobMs      float64 `json:"job_ms"`
	Workers    []int   `json:"workers"`
	GoMaxProcs []int   `json:"gomaxprocs"`
}

func handlePoolSweep(w http.ResponseWriter, r *http.Request) {
	var req poolRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "body JSON tidak valid")
			return
		}
	}
	ncpu := runtime.NumCPU()
	if req.Kind != "io" {
		req.Kind = "cpu"
	}
	if req.Jobs == 0 {
		req.Jobs = 200
	}
	req.Jobs = clamp(req.Jobs, 10, 2000)
	if req.JobMs <= 0 {
		req.JobMs = map[string]float64{"cpu": 2, "io": 10}[req.Kind]
	}
	req.JobMs = min(max(req.JobMs, 0.1), 50)
	if len(req.Workers) == 0 {
		req.Workers = []int{1, 2, 4, 8, 16, 32}
		if req.Kind == "io" {
			req.Workers = append(req.Workers, 64, 128)
		}
	}
	if len(req.GoMaxProcs) == 0 {
		req.GoMaxProcs = []int{1, ncpu}
		if ncpu > 2 {
			req.GoMaxProcs = []int{1, 2, ncpu}
		}
	}
	if len(req.Workers) > 12 || len(req.GoMaxProcs) > 4 {
		writeErr(w, http.StatusBadRequest, "maksimal 12 nilai workers dan 4 nilai gomaxprocs")
		return
	}
	for i := range req.Workers {
		req.Workers[i] = clamp(req.Workers[i], 1, 1024)
	}
	for i := range req.GoMaxProcs {
		req.GoMaxProcs[i] = clamp(req.GoMaxProcs[i], 1, ncpu)
	}
	// Batasi perkiraan durasi terburuk (1 worker) agar demo tidak menggantung.
	if float64(req.Jobs)*req.JobMs*float64(len(req.GoMaxProcs)) > 30000 {
		writeErr(w, http.StatusBadRequest, "terlalu berat: jobs*job_ms*len(gomaxprocs) maksimal 30000 ms")
		return
	}
	if !lockExperiment(w) {
		return
	}
	defer expMu.Unlock()

	orig := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(orig)
	start := time.Now()
	series := make([]poolSeries, 0, len(req.GoMaxProcs))
	for _, p := range req.GoMaxProcs {
		runtime.GOMAXPROCS(p)
		s := poolSeries{GoMaxProcs: p}
		for _, wk := range req.Workers {
			s.Points = append(s.Points, runPool(req.Kind, wk, req.Jobs, req.JobMs))
		}
		series = append(series, s)
	}
	logger.Info("pool sweep selesai", "request_id", reqID(r), "kind", req.Kind, "jobs", req.Jobs,
		"dur_ms", time.Since(start).Milliseconds())
	writeJSON(w, http.StatusOK, map[string]any{
		"kind": req.Kind, "jobs": req.Jobs, "job_ms": req.JobMs, "num_cpu": ncpu,
		"series": series,
	})
}
