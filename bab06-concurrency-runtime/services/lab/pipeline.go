package main

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Eksperimen 4: pipeline CSP (Subbab "CSP di Go" dan "Reactive Programming dan Backpressure").
// generator -> jobs (buffered) -> N worker (fan-out) -> channel per worker -> merge (fan-in)
// -> results (buffered) -> sink. Bila sink lambat, buffer penuh dan generator ikut tertahan:
// itulah backpressure alami dari channel ber-buffer terbatas.

type item struct {
	id      int64
	created time.Time
}

type pipelineSnap struct {
	TMs             int64   `json:"t_ms"`
	Produced        int64   `json:"produced"`
	Sunk            int64   `json:"sunk"`
	Processed       []int64 `json:"processed"`
	Busy            []int64 `json:"busy"` // id item yang sedang dikerjakan tiap worker, 0 = idle
	JobsLen         int     `json:"jobs_len"`
	JobsCap         int     `json:"jobs_cap"`
	OutsLen         []int   `json:"outs_len"`
	ResultsLen      int     `json:"results_len"`
	ResultsCap      int     `json:"results_cap"`
	ProduceRate     float64 `json:"produce_rate"`     // item/detik, jendela 1 detik
	SinkRate        float64 `json:"sink_rate"`        // item/detik, jendela 1 detik
	ProducerBlocked float64 `json:"producer_blocked"` // fraksi waktu generator tertahan saat kirim
	LatencyMs       float64 `json:"latency_ms"`       // rata-rata umur item saat tiba di sink
	ProduceMs       int64   `json:"produce_ms"`
	WorkMs          int64   `json:"work_ms"`
	SinkMs          int64   `json:"sink_ms"`
}

func handlePipelineStream(w http.ResponseWriter, r *http.Request) {
	nw := qInt(r, "workers", 3, 1, 16)
	buf := qInt(r, "buffer", 4, 0, 256)
	seconds := qInt(r, "seconds", 40, 3, 120)

	id, sess := newSession()
	defer sessions.Delete(id)
	produceMs := sess.knob("produce_ms", int64(qInt(r, "produce_ms", 20, 1, 1000)), 1, 1000)
	workMs := sess.knob("work_ms", int64(qInt(r, "work_ms", 50, 0, 2000)), 0, 2000)
	sinkMs := sess.knob("sink_ms", int64(qInt(r, "sink_ms", 10, 0, 2000)), 0, 2000)

	sse, err := newSSE(w)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(seconds)*time.Second)
	defer cancel()
	_ = sse.send("start", map[string]any{"session": id, "workers": nw, "buffer": buf, "seconds": seconds})
	logger.Info("pipeline mulai", "request_id", reqID(r), "session", id, "workers", nw, "buffer", buf)

	jobs := make(chan item, buf)
	outs := make([]chan item, nw)
	for i := range outs {
		outs[i] = make(chan item, buf)
	}
	results := make(chan item, buf)

	var produced, sunk, blockedNs, latSumNs, latN atomic.Int64
	processed := make([]atomic.Int64, nw)
	busy := make([]atomic.Int64, nw)
	sleepCtx := func(d time.Duration) bool {
		if d <= 0 {
			return ctx.Err() == nil
		}
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
			return true
		case <-ctx.Done():
			return false
		}
	}

	var all sync.WaitGroup

	// Tahap 1: generator
	all.Add(1)
	go func() {
		defer all.Done()
		defer close(jobs)
		var n int64
		for sleepCtx(time.Duration(produceMs.get()) * time.Millisecond) {
			n++
			it := item{id: n, created: time.Now()}
			s := time.Now()
			select {
			case jobs <- it: // tertahan di sini bila buffer jobs penuh (backpressure)
			case <-ctx.Done():
				return
			}
			blockedNs.Add(int64(time.Since(s)))
			produced.Add(1)
		}
	}()

	// Tahap 2: fan-out ke N worker, masing-masing punya channel keluaran sendiri
	for i := 0; i < nw; i++ {
		all.Add(1)
		go func() {
			defer all.Done()
			defer close(outs[i])
			for it := range jobs {
				busy[i].Store(it.id)
				if !sleepCtx(time.Duration(workMs.get()) * time.Millisecond) {
					return
				}
				busy[i].Store(0)
				select {
				case outs[i] <- it:
					processed[i].Add(1)
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Tahap 3: fan-in, menggabungkan N channel menjadi satu
	var mergeWg sync.WaitGroup
	for i := 0; i < nw; i++ {
		mergeWg.Add(1)
		go func() {
			defer mergeWg.Done()
			for it := range outs[i] {
				select {
				case results <- it:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	all.Add(1)
	go func() { defer all.Done(); mergeWg.Wait(); close(results) }()

	// Tahap 4: sink (konsumen akhir)
	all.Add(1)
	go func() {
		defer all.Done()
		for it := range results {
			if !sleepCtx(time.Duration(sinkMs.get()) * time.Millisecond) {
				return
			}
			sunk.Add(1)
			latSumNs.Add(int64(time.Since(it.created)))
			latN.Add(1)
		}
	}()

	t0 := time.Now()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	type hist struct {
		t         time.Time
		p, s, blk int64
	}
	window := []hist{{t: t0}}
	var lastLat float64
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-tick.C:
			now := time.Now()
			p, s, b := produced.Load(), sunk.Load(), blockedNs.Load()
			window = append(window, hist{t: now, p: p, s: s, blk: b})
			for len(window) > 2 && now.Sub(window[0].t) > time.Second {
				window = window[1:]
			}
			old := window[0]
			dt := now.Sub(old.t).Seconds()
			snap := pipelineSnap{
				TMs: now.Sub(t0).Milliseconds(), Produced: p, Sunk: s,
				Processed: make([]int64, nw), Busy: make([]int64, nw), OutsLen: make([]int, nw),
				JobsLen: len(jobs), JobsCap: cap(jobs), ResultsLen: len(results), ResultsCap: cap(results),
				ProduceMs: produceMs.get(), WorkMs: workMs.get(), SinkMs: sinkMs.get(),
			}
			if dt > 0 {
				snap.ProduceRate = float64(p-old.p) / dt
				snap.SinkRate = float64(s-old.s) / dt
				snap.ProducerBlocked = min(1, float64(b-old.blk)/float64(now.Sub(old.t)))
			}
			if n := latN.Swap(0); n > 0 {
				lastLat = float64(latSumNs.Swap(0)) / float64(n) / 1e6
			}
			snap.LatencyMs = lastLat // tetap tampilkan nilai terakhir bila tick ini tidak ada item tiba
			for i := 0; i < nw; i++ {
				snap.Processed[i] = processed[i].Load()
				snap.Busy[i] = busy[i].Load()
				snap.OutsLen[i] = len(outs[i])
			}
			if err := sse.send("state", snap); err != nil {
				cancel()
			}
		}
	}
	all.Wait()
	_ = sse.send("done", map[string]any{"produced": produced.Load(), "sunk": sunk.Load()})
	logger.Info("pipeline selesai", "request_id", reqID(r), "session", id, "produced", produced.Load(), "sunk", sunk.Load())
}
