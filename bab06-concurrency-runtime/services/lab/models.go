package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Eksperimen 6: dua model runtime pembanding, ditulis di Go.
// (a) Actor model (Subbab "Actor Model"): setiap actor punya mailbox dan state privat;
//     supervisor menjalankan ulang actor yang crash ("let it crash").
// (b) Event loop (Subbab "Event Loop dan async/await"): satu goroutine menjalankan semua
//     callback; satu tugas berat membekukan semua, kecuali dipindah ke worker.

type acctMsg struct {
	kind string // "Setor" atau "Racun"
	n    int64
}

type acctActor struct {
	id       int
	mailbox  chan acctMsg
	balance  atomic.Int64 // state diekspor untuk visualisasi; hanya actor ini yang menulis
	handled  atomic.Int64
	restarts atomic.Int64
	crashed  atomic.Bool
}

// run adalah perilaku actor: ambil satu pesan, proses, ulangi. Pesan "Racun" membuat panic.
func (a *acctActor) run(ctx context.Context, processMs *knob) {
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-a.mailbox:
			if !ok {
				return
			}
			if m.kind == "Racun" {
				panic(fmt.Sprintf("actor %d menerima pesan racun", a.id))
			}
			if d := processMs.get(); d > 0 {
				time.Sleep(time.Duration(d) * time.Millisecond)
			}
			a.balance.Add(m.n)
			a.handled.Add(1)
		}
	}
}

// supervise menjalankan actor dan memulainya lagi bila panic (strategi one-for-one).
func (a *acctActor) supervise(ctx context.Context, processMs *knob, onCrash func(int, any)) {
	for ctx.Err() == nil {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					a.restarts.Add(1)
					a.crashed.Store(true)
					onCrash(a.id, rec)
					time.Sleep(300 * time.Millisecond) // jeda restart agar terlihat di UI
					a.crashed.Store(false)
				}
			}()
			a.run(ctx, processMs)
		}()
	}
}

type actorSnap struct {
	TMs      int64   `json:"t_ms"`
	Mailbox  []int   `json:"mailbox"`
	Handled  []int64 `json:"handled"`
	Balance  []int64 `json:"balance"`
	Restarts []int64 `json:"restarts"`
	Crashed  []bool  `json:"crashed"`
	Sent     int64   `json:"sent"`
	InFlight int64   `json:"in_flight"` // total pesan setor yang masih di mailbox
	Total    int64   `json:"total"`     // jumlah saldo semua actor
	Rate     int64   `json:"rate"`
	Process  int64   `json:"process_ms"`
	Hot      int64   `json:"hot"`
}

func handleActorStream(w http.ResponseWriter, r *http.Request) {
	k := qInt(r, "actors", 4, 2, 8)
	seconds := qInt(r, "seconds", 60, 3, 180)
	mailboxCap := qInt(r, "mailbox", 500, 10, 10000)

	id, sess := newSession()
	defer sessions.Delete(id)
	rate := sess.knob("rate", int64(qInt(r, "rate", 100, 1, 2000)), 1, 2000) // pesan/detik
	processMs := sess.knob("process_ms", int64(qInt(r, "process_ms", 5, 0, 500)), 0, 500)
	hot := sess.knob("hot", int64(qInt(r, "hot", 0, 0, 1)), 0, 1) // 1 = 70% pesan ke actor 0

	sse, err := newSSE(w)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(seconds)*time.Second)
	defer cancel()

	actors := make([]*acctActor, k)
	for i := range actors {
		actors[i] = &acctActor{id: i, mailbox: make(chan acctMsg, mailboxCap)}
	}
	t0 := time.Now()
	logf := func(format string, a ...any) {
		_ = sse.send("log", map[string]any{"t_ms": time.Since(t0).Milliseconds(), "msg": fmt.Sprintf(format, a...)})
	}
	sess.actions["poison"] = func(v int64) {
		i := clamp(int(v), 0, k-1)
		select {
		case actors[i].mailbox <- acctMsg{kind: "Racun"}:
			logf("pesan racun dikirim ke actor %d", i)
		default:
			logf("mailbox actor %d penuh, racun tidak terkirim", i)
		}
	}
	_ = sse.send("start", map[string]any{"session": id, "actors": k, "mailbox_cap": mailboxCap, "seconds": seconds})
	logger.Info("actor mulai", "request_id", reqID(r), "session", id, "actors", k)

	var wg sync.WaitGroup
	for _, a := range actors {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.supervise(ctx, processMs, func(id int, rec any) {
				logf("actor %d crash (%v); supervisor memulai ulang, state saldo dipulihkan", id, rec)
				logger.Warn("actor crash", "session", id, "actor", id, "panic", fmt.Sprint(rec))
			})
		}()
	}

	// Pengirim: mengirim pesan Setor(1) dengan laju `rate`. Bila mailbox penuh, pengirim tertahan.
	var sent atomic.Int64
	wg.Add(1)
	go func() {
		defer wg.Done()
		next := time.Now()
		for ctx.Err() == nil {
			next = next.Add(time.Second / time.Duration(rate.get()))
			if d := time.Until(next); d > 0 {
				time.Sleep(d)
			} else if d < -time.Second {
				next = time.Now() // tertinggal jauh: jangan mengejar
			}
			target := rand.IntN(k)
			if hot.get() == 1 && rand.Float64() < 0.7 {
				target = 0
			}
			select {
			case actors[target].mailbox <- acctMsg{kind: "Setor", n: 1}:
				sent.Add(1)
			case <-ctx.Done():
				return
			}
		}
	}()

	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-tick.C:
			s := actorSnap{TMs: time.Since(t0).Milliseconds(), Sent: sent.Load(),
				Rate: rate.get(), Process: processMs.get(), Hot: hot.get()}
			for _, a := range actors {
				s.Mailbox = append(s.Mailbox, len(a.mailbox))
				s.Handled = append(s.Handled, a.handled.Load())
				b := a.balance.Load()
				s.Balance = append(s.Balance, b)
				s.Total += b
				s.Restarts = append(s.Restarts, a.restarts.Load())
				s.Crashed = append(s.Crashed, a.crashed.Load())
			}
			s.InFlight = s.Sent - s.Total
			if sse.send("state", s) != nil {
				cancel()
			}
		}
	}
	wg.Wait()
	_ = sse.send("done", map[string]any{"sent": sent.Load()})
}

// ---- event loop ----

type loopTask struct {
	name      string
	scheduled time.Time
	fn        func(now time.Time)
}

type loopSnap struct {
	TMs      int64   `json:"t_ms"`
	LagAvgMs float64 `json:"lag_avg_ms"`
	LagMaxMs float64 `json:"lag_max_ms"`
	Queue    int     `json:"queue"`
	Ticks    int64   `json:"ticks"`
	Requests int64   `json:"requests"`
	ReqP95Ms float64 `json:"req_p95_ms"`
}

// busyFor membakar CPU selama d (tanpa tidur), meniru perhitungan berat di dalam callback.
func busyFor(d time.Duration) {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		sink.Add(cpuWork(2000))
	}
}

func handleEventLoopStream(w http.ResponseWriter, r *http.Request) {
	seconds := qInt(r, "seconds", 60, 3, 180)
	id, sess := newSession()
	defer sessions.Delete(id)

	sse, err := newSSE(w)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(seconds)*time.Second)
	defer cancel()
	t0 := time.Now()
	logf := func(format string, a ...any) {
		_ = sse.send("log", map[string]any{"t_ms": time.Since(t0).Milliseconds(), "msg": fmt.Sprintf(format, a...)})
	}

	queue := make(chan loopTask, 4096) // antrean callback (macrotask queue)
	post := func(t loopTask) {
		select {
		case queue <- t:
		default: // antrean penuh: buang, agar demo tidak macet
		}
	}

	var ticks, reqs atomic.Int64
	var mu sync.Mutex
	var lags, reqLat []float64

	// Aksi dari UI: sisipkan tugas berat ke loop, atau pindahkan ke worker (offload).
	sess.actions["block"] = func(v int64) {
		d := time.Duration(clamp(int(v), 10, 3000)) * time.Millisecond
		post(loopTask{name: "berat", scheduled: time.Now(), fn: func(time.Time) {
			logf("callback berat %d ms berjalan DI event loop: semua callback lain menunggu", d.Milliseconds())
			busyFor(d)
		}})
	}
	sess.actions["offload"] = func(v int64) {
		d := time.Duration(clamp(int(v), 10, 3000)) * time.Millisecond
		post(loopTask{name: "offload", scheduled: time.Now(), fn: func(time.Time) {
			logf("tugas berat %d ms dipindah ke worker; loop tetap melayani callback", d.Milliseconds())
			go func() {
				busyFor(d) // berjalan di goroutine lain (seperti worker thread libuv)
				post(loopTask{name: "selesai", scheduled: time.Now(), fn: func(time.Time) {
					logf("callback hasil worker diterima loop")
				}})
			}()
		}})
	}

	// Event loop: SATU goroutine menjalankan semua callback secara berurutan.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case t := <-queue:
				t.fn(time.Now())
			}
		}
	}()

	// Sumber event: timer 20 ms (lag diukur) dan "request" masuk setiap 50 ms.
	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := time.NewTicker(20 * time.Millisecond)
		req := time.NewTicker(50 * time.Millisecond)
		defer timer.Stop()
		defer req.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-timer.C:
				post(loopTask{name: "timer", scheduled: now, fn: func(run time.Time) {
					ticks.Add(1)
					mu.Lock()
					lags = append(lags, ms(run.Sub(now)))
					mu.Unlock()
				}})
			case now := <-req.C:
				post(loopTask{name: "request", scheduled: now, fn: func(run time.Time) {
					busyFor(time.Millisecond) // handler ringan
					reqs.Add(1)
					mu.Lock()
					reqLat = append(reqLat, ms(time.Since(now)))
					mu.Unlock()
				}})
			}
		}
	}()

	_ = sse.send("start", map[string]any{"session": id, "seconds": seconds})
	logger.Info("event loop mulai", "request_id", reqID(r), "session", id)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-tick.C:
			mu.Lock()
			s := loopSnap{TMs: time.Since(t0).Milliseconds(), Queue: len(queue), Ticks: ticks.Load(), Requests: reqs.Load()}
			for _, l := range lags {
				s.LagAvgMs += l
				s.LagMaxMs = max(s.LagMaxMs, l)
			}
			if len(lags) > 0 {
				s.LagAvgMs /= float64(len(lags))
			}
			if len(reqLat) > 0 {
				s.ReqP95Ms = pctUnsorted(reqLat, 0.95)
			}
			lags, reqLat = lags[:0], reqLat[:0]
			mu.Unlock()
			if sse.send("state", s) != nil {
				cancel()
			}
		}
	}
	wg.Wait()
	_ = sse.send("done", map[string]any{"ticks": ticks.Load()})
}

func pctUnsorted(xs []float64, p float64) float64 {
	s := slices.Clone(xs)
	slices.Sort(s)
	return pct(s, p)
}
